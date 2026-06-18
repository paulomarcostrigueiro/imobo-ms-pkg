// Copyright 2026 imobo. Licença: privada.

// Package moneycents implementa o tipo Cents (BIGINT centavos) usado em toda
// representacao monetaria da imobo-platform.
//
// Esta lib e a contrapartida Go da `imobo-money` (Java) referenciada nas ADRs:
//   - ADR-001 (partidas dobradas + BIGINT centavos): regra fundadora.
//   - ADR-005 (errata BIGINT em toda entidade monetaria): generalizacao.
//   - ADR-014 (Strangler Fig Java to Go): contexto da existencia desta lib.
//
// REGRAS INVIOLAVEIS:
//   - NUNCA usar float64 para valores monetarios criticos (apenas para entrada
//     nao-critica via FromReais com aviso).
//   - Aritmetica e exata em soma, subtracao e multiplicacao por inteiro.
//   - Divisao retorna quociente + resto (forca o programador a decidir o metodo
//     de rateio). Ver DivInt e exemplo de rateio entre socios em cents_test.go.
//   - Representacao decimal brasileira: virgula decimal, ponto separador de milhar.
//   - Limite teorico: int64 = 9.223 * 10^18 centavos = ~92 quatrilhoes de reais.
package moneycents

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Cents representa um valor monetario em centavos como BIGINT (int64).
//
// Exemplo:
//
//	Cents(123456) representa R$ 1.234,56
//	Cents(0)      representa R$ 0,00
//	Cents(-50)    representa -R$ 0,50 (saldo negativo permitido em algumas semanticas).
//
// Um saldo de conta pode ser negativo, mas valores de transacao (boleto, repasse)
// devem ser validados como positivos pela camada de dominio (ver ADR-001 secao 2:
// "valor sempre > 0 + natureza explicita CHAR(1)").
type Cents int64

// Erros canonicos retornados por FromString.
var (
	// ErrEmptyString indica que a string de entrada estava vazia ou continha
	// apenas espacos em branco.
	ErrEmptyString = errors.New("moneycents: string vazia")

	// ErrInvalidFormat indica que a string nao corresponde a um numero monetario
	// valido (contem letras, multiplos pontos/virgulas decimais, etc).
	ErrInvalidFormat = errors.New("moneycents: formato invalido")

	// ErrOverflow indica que o valor convertido excede o limite int64 (~92
	// quatrilhoes de reais — irrelevante na pratica, mas detectavel).
	ErrOverflow = errors.New("moneycents: overflow int64")

	// ErrTooManyDecimals indica que a string tinha mais de 2 casas decimais
	// (centavos so suportam 2 casas).
	ErrTooManyDecimals = errors.New("moneycents: mais de 2 casas decimais")
)

// FromString converte uma string monetaria em Cents.
//
// Formatos aceitos:
//   - "1234.56"     -> 123456
//   - "1234,56"     -> 123456 (formato BR)
//   - "1.234,56"    -> 123456 (formato BR com separador de milhar)
//   - "1,234.56"    -> 123456 (formato US com separador de milhar)
//   - "0"           -> 0
//   - "0.01"        -> 1
//   - "-1234,56"    -> -123456
//   - "  1234.56  " -> 123456 (espacos sao removidos)
//
// Retorna ErrEmptyString, ErrInvalidFormat, ErrOverflow ou ErrTooManyDecimals
// em casos de erro.
//
// Heuristica BR vs US: se a string tem AMBOS '.' e ',', o ULTIMO separador e
// considerado decimal. Se tem so um, vira-se decimal apenas se houver no maximo
// 2 digitos depois dele E nao for separador de milhar evidente.
func FromString(s string) (Cents, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ErrEmptyString
	}

	// Sinal explicito.
	negative := false
	if s[0] == '-' {
		negative = true
		s = s[1:]
	} else if s[0] == '+' {
		s = s[1:]
	}
	if s == "" {
		return 0, ErrInvalidFormat
	}

	// Identifica posicao do separador decimal.
	lastDot := strings.LastIndex(s, ".")
	lastComma := strings.LastIndex(s, ",")

	var decimalSep, thousandSep rune
	switch {
	case lastDot == -1 && lastComma == -1:
		// Sem separador, numero inteiro.
		decimalSep = 0
		thousandSep = 0
	case lastDot == -1:
		// So virgula. Se tiver mais de uma virgula, ambiguo.
		if strings.Count(s, ",") > 1 {
			// Multiplas virgulas: tratar como separador de milhar (US) sem decimal.
			decimalSep = 0
			thousandSep = ','
		} else {
			decimalSep = ','
			thousandSep = 0
		}
	case lastComma == -1:
		// So ponto. Se mais de um ponto, e separador de milhar.
		if strings.Count(s, ".") > 1 {
			decimalSep = 0
			thousandSep = '.'
		} else {
			decimalSep = '.'
			thousandSep = 0
		}
	case lastDot > lastComma:
		// Ponto e o ultimo: formato US (1,234.56).
		decimalSep = '.'
		thousandSep = ','
	default:
		// Virgula e o ultimo: formato BR (1.234,56).
		decimalSep = ','
		thousandSep = '.'
	}

	// Remove separadores de milhar.
	if thousandSep != 0 {
		s = strings.ReplaceAll(s, string(thousandSep), "")
	}

	// Quebra inteiro/decimal.
	var intPart, decPart string
	if decimalSep != 0 {
		idx := strings.IndexRune(s, decimalSep)
		intPart = s[:idx]
		decPart = s[idx+1:]
	} else {
		intPart = s
		decPart = ""
	}

	// intPart pode ser vazio se input for "0,50" -> intPart="0", ok. Mas ",50"
	// passa intPart vazio. Vamos tolerar tratando como 0.
	if intPart == "" {
		intPart = "0"
	}

	// Valida que intPart so tem digitos.
	for _, r := range intPart {
		if r < '0' || r > '9' {
			return 0, ErrInvalidFormat
		}
	}
	// Valida decPart so tem digitos.
	for _, r := range decPart {
		if r < '0' || r > '9' {
			return 0, ErrInvalidFormat
		}
	}

	// Centavos so tem 2 casas. Mais que isso e erro (operador deve arredondar
	// explicitamente antes de chamar a lib).
	if len(decPart) > 2 {
		return 0, ErrTooManyDecimals
	}
	// Pad ate 2 casas.
	for len(decPart) < 2 {
		decPart += "0"
	}

	// Converte string -> int64 com checagem de overflow.
	intVal, err := parsePositiveInt64(intPart)
	if err != nil {
		return 0, err
	}
	decVal, err := parsePositiveInt64(decPart)
	if err != nil {
		return 0, err
	}

	// total = intVal * 100 + decVal. Checa overflow.
	if intVal > (math.MaxInt64-decVal)/100 {
		return 0, ErrOverflow
	}
	total := intVal*100 + decVal

	if negative {
		// -math.MinInt64 nao representa em int64. Como math.MaxInt64 == -math.MinInt64-1,
		// total positivo nao pode causar overflow ao negativar.
		return Cents(-total), nil
	}
	return Cents(total), nil
}

// parsePositiveInt64 converte string puramente numerica em int64 com checagem
// de overflow. Nao aceita sinal.
func parsePositiveInt64(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	var result int64
	for _, r := range s {
		d := int64(r - '0')
		// Checa overflow antes de multiplicar.
		if result > (math.MaxInt64-d)/10 {
			return 0, ErrOverflow
		}
		result = result*10 + d
	}
	return result, nil
}

// FromReais converte float64 em reais para Cents.
//
// AVISO: float64 e impreciso. Use APENAS para entrada de dados nao-criticos
// (por exemplo, parsing tolerante de planilha legada). Para qualquer dado
// que afete ledger, repasse, boleto ou imposto, use FromString.
//
// Implementacao: arredonda para o centavo mais proximo (banker's NAO — usa
// arredondamento de meio-pra-cima, math.Round).
func FromReais(r float64) Cents {
	return Cents(math.Round(r * 100))
}

// String formata o Cents no formato decimal canonico com ponto: "1234.56".
//
// Util para serializacao tecnica e debug. Para exibicao ao usuario brasileiro,
// use FormatBR ou FormatBRCurrency.
func (c Cents) String() string {
	negative := c < 0
	abs := int64(c)
	if negative {
		abs = -abs
	}
	intPart := abs / 100
	decPart := abs % 100
	if negative {
		return fmt.Sprintf("-%d.%02d", intPart, decPart)
	}
	return fmt.Sprintf("%d.%02d", intPart, decPart)
}

// FormatBR formata no padrao brasileiro com virgula decimal e ponto separador
// de milhar: "1.234,56".
//
// Nao inclui o prefixo "R$". Para isso use FormatBRCurrency.
func (c Cents) FormatBR() string {
	negative := c < 0
	abs := int64(c)
	if negative {
		abs = -abs
	}
	intPart := abs / 100
	decPart := abs % 100

	intStr := insertThousandSeparator(intPart, '.')
	if negative {
		return fmt.Sprintf("-%s,%02d", intStr, decPart)
	}
	return fmt.Sprintf("%s,%02d", intStr, decPart)
}

// FormatBRCurrency formata como moeda brasileira completa: "R$ 1.234,56".
//
// Para valor negativo: "-R$ 1.234,56" (sinal antes do simbolo, padrao Brasil).
func (c Cents) FormatBRCurrency() string {
	if c < 0 {
		return "-R$ " + (-c).FormatBR()
	}
	return "R$ " + c.FormatBR()
}

// insertThousandSeparator insere o separador (ex: '.') a cada 3 digitos pela
// direita: 1234567 -> "1.234.567".
func insertThousandSeparator(n int64, sep rune) string {
	if n < 0 {
		// Esta funcao recebe sempre absoluto, mas defesa em profundidade.
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}

	var b strings.Builder
	// Quantos digitos antes do primeiro separador.
	first := len(s) % 3
	if first == 0 {
		first = 3
	}
	b.WriteString(s[:first])
	for i := first; i < len(s); i += 3 {
		b.WriteRune(sep)
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// Add retorna a soma de dois valores Cents.
//
// Em caso de overflow int64, dispara panic com mensagem clara. Em pratica, isso
// e impossivel para valores monetarios reais (limite ~92 quatrilhoes de reais).
func (c Cents) Add(other Cents) Cents {
	result := int64(c) + int64(other)
	// Detecta overflow: se ambos operandos tem mesmo sinal e o resultado tem
	// sinal oposto, overflow.
	if (int64(c) > 0 && int64(other) > 0 && result < 0) ||
		(int64(c) < 0 && int64(other) < 0 && result > 0) {
		panic(fmt.Sprintf("moneycents: overflow em Add(%d, %d)", c, other))
	}
	return Cents(result)
}

// Sub retorna a diferenca entre dois valores Cents (c - other).
//
// Em caso de overflow int64, dispara panic com mensagem clara.
func (c Cents) Sub(other Cents) Cents {
	result := int64(c) - int64(other)
	// Detecta overflow: sinal oposto entre operandos e resultado tem sinal
	// diferente do esperado.
	if (int64(c) > 0 && int64(other) < 0 && result < 0) ||
		(int64(c) < 0 && int64(other) > 0 && result > 0) {
		panic(fmt.Sprintf("moneycents: overflow em Sub(%d, %d)", c, other))
	}
	return Cents(result)
}

// MulInt multiplica o valor por um inteiro.
//
// Util para casos como: "12 parcelas de R$ 500,00" -> Cents(50000).MulInt(12).
// Em caso de overflow int64, dispara panic.
func (c Cents) MulInt(n int64) Cents {
	if c == 0 || n == 0 {
		return 0
	}
	result := int64(c) * n
	// Detecta overflow: result/n deve ser igual ao operando original.
	if result/n != int64(c) {
		panic(fmt.Sprintf("moneycents: overflow em MulInt(%d, %d)", c, n))
	}
	return Cents(result)
}

// DivInt divide o valor por um inteiro, retornando quociente e resto em centavos.
//
// IMPORTANTE: ADR-001 secao 2.4 exige que rateio entre socios seja explicito.
// Esta funcao FORCA o programador a decidir o que fazer com o resto.
//
// Exemplo (rateio de R$ 100,00 entre 3 socios):
//
//	q, r := Cents(10000).DivInt(3)
//	// q = 3333 (R$ 33,33 cada socio)
//	// r = 1    (R$ 0,01 sobra)
//	// Politica: dar a sobra ao primeiro socio (caso ADR-001).
//
// Panic se n == 0.
func (c Cents) DivInt(n int64) (quotient, remainder Cents) {
	if n == 0 {
		panic("moneycents: divisao por zero em DivInt")
	}
	q := int64(c) / n
	r := int64(c) % n
	return Cents(q), Cents(r)
}

// MulPercent multiplica o valor por um percentual em basis points (bps).
//
// 1 basis point = 0,01% = 1/10000.
//   - 150 bps  = 1,5%
//   - 1000 bps = 10%
//   - 10000 bps = 100%
//
// O resultado e arredondado para baixo (truncamento), seguindo a convencao
// fiscal brasileira para impostos retidos (IRRF, ISS): a Receita arredonda
// favor do contribuinte. Caso especial: se o programador precisar de
// arredondamento meio-pra-cima, deve fazer manualmente.
//
// Exemplo (taxa de administracao de 8% sobre R$ 1.000,00):
//
//	taxa := Cents(100000).MulPercent(800)  // 800 bps = 8%
//	// taxa = 8000 (R$ 80,00)
func (c Cents) MulPercent(bps int) Cents {
	if c == 0 || bps == 0 {
		return 0
	}
	// Calculo: c * bps / 10000.
	// Para evitar overflow em valores extremos, usa-se int64 explicito.
	result := int64(c) * int64(bps)
	// Checagem de overflow: result/bps deve igualar c.
	if result/int64(bps) != int64(c) {
		panic(fmt.Sprintf("moneycents: overflow em MulPercent(%d, %d bps)", c, bps))
	}
	return Cents(result / 10000)
}

// IsZero retorna true se o valor e exatamente zero centavos.
func (c Cents) IsZero() bool { return c == 0 }

// IsPositive retorna true se o valor e estritamente maior que zero.
func (c Cents) IsPositive() bool { return c > 0 }

// IsNegative retorna true se o valor e estritamente menor que zero.
func (c Cents) IsNegative() bool { return c < 0 }

// Abs retorna o valor absoluto. Cents(-100).Abs() == Cents(100).
//
// Em caso de math.MinInt64 (~ -92 quatrilhoes), o resultado satura em
// math.MaxInt64 para evitar overflow silencioso.
func (c Cents) Abs() Cents {
	if c == math.MinInt64 {
		return Cents(math.MaxInt64)
	}
	if c < 0 {
		return -c
	}
	return c
}

// Negate retorna o valor com sinal invertido.
func (c Cents) Negate() Cents {
	if c == math.MinInt64 {
		return Cents(math.MaxInt64)
	}
	return -c
}
