// Copyright 2026 imobo. Licenca: privada.

package veiculobr

import "strings"

// FormatoPlaca distingue os dois padroes que circulam no Brasil.
type FormatoPlaca string

const (
	// FormatoAntigo e AAA1234 (padrao ate 2018, ainda maioria da frota usada).
	FormatoAntigo FormatoPlaca = "antigo"
	// FormatoMercosul e AAA1A23.
	FormatoMercosul FormatoPlaca = "mercosul"
)

// ---- CHASSI (VIN, 17 caracteres).

// Chassi normaliza e valida o chassi. E o unico campo presente em TODO registro
// de estoque do RENAVE (nos 597 conferidos na homologacao), inclusive nos
// veiculos sem placa, entao e a chave mais confiavel para casar documento com
// veiculo.
//
// A regra que faz o trabalho pesado: a ISO 3779 proibe I, O e Q no VIN de
// proposito, para nao confundir com 1 e 0. Se o extrator devolveu um "O", ele
// leu um zero. A correcao e certa, nao e palpite.
//
// NAO conferimos o digito verificador da posicao 9 (o check digit do padrao
// norte-americano FMVSS 115). Ele NAO e obrigatorio em veiculo fabricado no
// Brasil, e exigi-lo reprovaria chassi legitimo. Trocar falso negativo por falso
// positivo aqui seria pior: o lojista ficaria travado com o documento certo na
// mao.
func Chassi(s string) Resultado {
	orig := s
	v := limpar(s)
	if v == "" {
		return falha(orig, StatusVazio, "chassi nao foi lido no documento")
	}
	if len(v) != 17 {
		return falha(orig, StatusTamanhoInvalido,
			"chassi tem "+itoa(len(v))+" caracteres, o correto sao 17 (foto cortada ou com reflexo?)")
	}
	var corr []Correcao
	out := []rune(v)
	for i, r := range out {
		var sub rune
		switch r {
		case 'I':
			sub = '1'
		case 'O', 'Q':
			sub = '0'
		default:
			continue
		}
		corr = append(corr, Correcao{
			Posicao: i + 1, De: r, Para: sub,
			Motivo: "a ISO 3779 proibe I, O e Q no chassi",
		})
		out[i] = sub
	}
	return montar(orig, string(out), corr)
}

// ---- PLACA (7 caracteres, antiga ou Mercosul).

// Placa normaliza e valida a placa, corrigindo pelas restricoes de posicao.
// Aceita os dois formatos; na amostra da homologacao vieram 362 Mercosul e 162
// antigas, ou seja, os dois seguem valendo e nenhum pode ser descartado.
//
// A posicao 5 e a fronteira entre os formatos: letra = Mercosul, digito =
// antiga. Justamente por isso a posicao 5 NAO tem correcao possivel — corrigir
// ali seria escolher o formato no lugar do documento.
func Placa(s string) Resultado {
	orig := s
	v := limpar(s)
	if v == "" {
		return falha(orig, StatusVazio, "placa nao foi lida no documento")
	}
	if len(v) != 7 {
		return falha(orig, StatusTamanhoInvalido,
			"placa tem "+itoa(len(v))+" caracteres, o correto sao 7")
	}
	var corr []Correcao

	// posicoes 1-3: sempre letras, nos dois formatos.
	pref, c1, ok := exigirLetras(v[:3], 0, "as tres primeiras posicoes da placa sao sempre letras")
	if !ok {
		return falha(orig, StatusCaractereInvalido, "as tres primeiras posicoes da placa precisam ser letras")
	}
	corr = append(corr, c1...)

	// posicao 4: sempre digito, nos dois formatos.
	p4, c2, ok := exigirDigitos(v[3:4], 3, "a quarta posicao da placa e sempre um numero")
	if !ok {
		return falha(orig, StatusCaractereInvalido, "a quarta posicao da placa precisa ser um numero")
	}
	corr = append(corr, c2...)

	// posicao 5: decide o formato. Sem correcao aqui, de proposito.
	p5 := rune(v[4])
	switch {
	case letra(p5): // Mercosul
	case digito(p5): // antiga
	default:
		return falha(orig, StatusCaractereInvalido, "quinta posicao da placa ilegivel")
	}

	// posicoes 6-7: sempre digitos, nos dois formatos.
	fim, c3, ok := exigirDigitos(v[5:], 5, "as duas ultimas posicoes da placa sao sempre numeros")
	if !ok {
		return falha(orig, StatusCaractereInvalido, "as duas ultimas posicoes da placa precisam ser numeros")
	}
	corr = append(corr, c3...)

	return montar(orig, pref+p4+string(p5)+fim, corr)
}

// FormatoDaPlaca diz se a placa ja normalizada e antiga ou Mercosul. So faz
// sentido chamar depois de Placa devolver Valido.
func FormatoDaPlaca(placa string) FormatoPlaca {
	if len(placa) == 7 && letra(rune(placa[4])) {
		return FormatoMercosul
	}
	return FormatoAntigo
}

// ---- RENAVAM (11 digitos, com digito verificador).

// Renavam normaliza, corrige e CONFERE o digito verificador.
//
// Este e o unico dos cinco campos com DV, ou seja, o unico onde da para saber
// que a leitura esta errada sem perguntar para o SERPRO. O algoritmo abaixo foi
// conferido contra 524 RENAVAMs reais devolvidos pela homologacao do RENAVE:
// fechou nos 524.
//
// Entrada com menos de 11 digitos e completada com zeros a esquerda, porque
// RENAVAM antigo tinha 9 digitos e muito documento e sistema ainda imprime sem
// os zeros. Isso NAO e um chute: o zero a esquerda faz parte do numero.
func Renavam(s string) Resultado {
	orig := s
	v := limpar(s)
	if v == "" {
		return falha(orig, StatusVazio, "RENAVAM nao foi lido no documento")
	}
	if len(v) > 11 {
		return falha(orig, StatusTamanhoInvalido,
			"RENAVAM tem "+itoa(len(v))+" digitos, o maximo sao 11")
	}
	v, corr, ok := exigirDigitos(v, 0, "o RENAVAM e composto so de numeros")
	if !ok {
		return falha(orig, StatusCaractereInvalido, "o RENAVAM tem caractere que nao e numero")
	}
	if len(v) < 11 {
		v = strings.Repeat("0", 11-len(v)) + v
	}
	if int(v[10]-'0') != dvRenavam(v[:10]) {
		return falha(orig, StatusDVInvalido,
			"o digito verificador do RENAVAM nao fecha — a leitura do documento saiu errada")
	}
	return montar(orig, v, corr)
}

// dvRenavam calcula o digito verificador dos 10 primeiros digitos.
//
// Algoritmo do DENATRAN: pesos 3,2,9,8,7,6,5,4,3,2 da esquerda para a direita
// (equivale a inverter a base e aplicar 2,3,4,...,9,2,3), somar, multiplicar por
// 10 e tirar o resto por 11; resto 10 vale zero.
func dvRenavam(base string) int {
	pesos := [10]int{3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
	soma := 0
	for i := 0; i < 10; i++ {
		soma += int(base[i]-'0') * pesos[i]
	}
	dv := (soma * 10) % 11
	if dv >= 10 {
		return 0
	}
	return dv
}

// ---- CRV: numero e codigo de seguranca.

// NumeroCRV confere o numero do CRV: 12 digitos (12/12 em 524 registros reais).
//
// SEM digito verificador conhecido. A unica defesa aqui e tamanho e tipo. Um
// digito trocado pelo OCR passa direto.
func NumeroCRV(s string) Resultado { return digitosFixos(s, 12, "numero do CRV") }

// CodigoSegurancaCRV confere o codigo de seguranca do CRV: 11 digitos.
//
// SEM digito verificador conhecido, e este e o campo mais critico do fluxo:
// e o segredo impresso no verso do CRV, o que ninguem digita certo e sem o qual
// a entrada no RENAVE nao acontece. Como nao da para validar localmente, a
// interface TEM de exibi-lo para conferencia humana antes de gravar.
//
// CUIDADO, erro real e comum: o CRLV-e (documento de licenciamento anual) tem um
// codigo de seguranca PROPRIO, diferente deste. Quem fotografa o CRLV achando
// que e o CRV manda o codigo errado e a operacao e recusada.
func CodigoSegurancaCRV(s string) Resultado { return digitosFixos(s, 11, "codigo de seguranca do CRV") }

// CPF confere os 11 digitos e os dois digitos verificadores.
func CPF(s string) Resultado {
	orig := s
	v := somenteDigitos(s)
	if v == "" {
		return falha(orig, StatusVazio, "CPF nao informado")
	}
	if len(v) != 11 {
		return falha(orig, StatusTamanhoInvalido, "CPF tem "+itoa(len(v))+" digitos, o correto sao 11")
	}
	if todosIguais(v) || !dvCPF(v) {
		return falha(orig, StatusDVInvalido, "o digito verificador do CPF nao fecha")
	}
	return montar(orig, v, nil)
}

// CNPJ confere os 14 digitos e os dois digitos verificadores.
func CNPJ(s string) Resultado {
	orig := s
	v := somenteDigitos(s)
	if v == "" {
		return falha(orig, StatusVazio, "CNPJ nao informado")
	}
	if len(v) != 14 {
		return falha(orig, StatusTamanhoInvalido, "CNPJ tem "+itoa(len(v))+" digitos, o correto sao 14")
	}
	if todosIguais(v) || !dvCNPJ(v) {
		return falha(orig, StatusDVInvalido, "o digito verificador do CNPJ nao fecha")
	}
	return montar(orig, v, nil)
}

// digitosFixos e o caso comum: N digitos, sem DV, corrigindo letras confundidas.
func digitosFixos(s string, n int, nome string) Resultado {
	orig := s
	v := limpar(s)
	if v == "" {
		return falha(orig, StatusVazio, nome+" nao foi lido no documento")
	}
	if len(v) != n {
		return falha(orig, StatusTamanhoInvalido,
			nome+" tem "+itoa(len(v))+" digitos, o correto sao "+itoa(n))
	}
	v, corr, ok := exigirDigitos(v, 0, "o "+nome+" e composto so de numeros")
	if !ok {
		return falha(orig, StatusCaractereInvalido, "o "+nome+" tem caractere que nao e numero")
	}
	return montar(orig, v, corr)
}

func todosIguais(s string) bool {
	for i := 1; i < len(s); i++ {
		if s[i] != s[0] {
			return false
		}
	}
	return true
}

// dvCPF confere os dois ultimos digitos pelo modulo 11 com pesos decrescentes:
// o primeiro DV usa pesos 10..2 sobre os 9 primeiros digitos, o segundo usa
// pesos 11..2 sobre os 10 primeiros.
func dvCPF(v string) bool {
	calc := func(ate int) int {
		soma, peso := 0, ate+1
		for i := 0; i < ate; i++ {
			soma += int(v[i]-'0') * (peso - i)
		}
		r := soma % 11
		if r < 2 {
			return 0
		}
		return 11 - r
	}
	return int(v[9]-'0') == calc(9) && int(v[10]-'0') == calc(10)
}

func dvCNPJ(v string) bool {
	pesos1 := []int{5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
	pesos2 := []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}
	dv := func(pesos []int) int {
		soma := 0
		for i, p := range pesos {
			soma += int(v[i]-'0') * p
		}
		r := soma % 11
		if r < 2 {
			return 0
		}
		return 11 - r
	}
	return int(v[12]-'0') == dv(pesos1) && int(v[13]-'0') == dv(pesos2)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
