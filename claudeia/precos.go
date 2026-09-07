// Copyright 2026 imobo. Licenca: privada.

package claudeia

import (
	"fmt"

	"github.com/paulomarcostrigueiro/imobo-ms-pkg/moneycents"
)

// MicroDolar e um milionesimo de dolar. E a unidade natural aqui: os precos da
// Anthropic sao em dolar por milhao de tokens, entao o preco de UM token cai
// exatamente em micro-dolares inteiros e a conta toda fica em inteiro, sem
// float. US$ 5,00 por milhao de tokens = 5 micro-dolares por token.
//
// Mesma disciplina do moneycents (ADR-001): dinheiro nao anda em float64.
type MicroDolar int64

// Preco descreve quanto custa um modelo, por token.
type Preco struct {
	// Entrada e o custo de um token de entrada (prompt, imagem, documento).
	Entrada MicroDolar
	// Saida e o custo de um token gerado pelo modelo.
	Saida MicroDolar
	// Tier e o limite de resolucao de imagem do modelo, que muda quanto uma
	// mesma foto custa. Ver imagem.go.
	Tier Tier
}

// Modelos que consideramos para leitura de documento, com o preco de tabela da
// API primeira-parte da Anthropic (nao vale para Bedrock nem Vertex, que tem
// preco proprio).
//
// FONTE: claude.com/pricing, valores de 2026-06-24. Preco muda; quando a conta
// importar de verdade, confira na pagina antes de prometer numero para cliente.
//
// A escolha entre eles nao e so preco. Haiku 4.5 e do tier de resolucao PADRAO,
// ou seja, a mesma foto entra nele com no maximo 1568 tokens visuais contra 4784
// nos outros. Isso corta o custo alem do preco por token, mas tambem corta a
// resolucao com que o modelo enxerga o documento, que e justamente o que decide
// se ele le certo o codigo de seguranca de 11 digitos.
var (
	// Opus5 e o modelo mais capaz da linha Opus.
	Opus5 = Preco{Entrada: 5, Saida: 25, Tier: TierAlta}
	// Sonnet5 equilibra custo e capacidade.
	Sonnet5 = Preco{Entrada: 2, Saida: 10, Tier: TierAlta}
	// Haiku45 e o mais barato e o mais rapido, em tier de resolucao padrao.
	Haiku45 = Preco{Entrada: 1, Saida: 5, Tier: TierPadrao}
)

// tabela liga o id do modelo ao preco.
var tabela = map[string]Preco{
	"claude-opus-5":     Opus5,
	"claude-sonnet-5":   Sonnet5,
	"claude-haiku-4-5":  Haiku45,
	"claude-opus-4-8":   {Entrada: 5, Saida: 25, Tier: TierAlta},
	"claude-opus-4-7":   {Entrada: 5, Saida: 25, Tier: TierAlta},
	"claude-sonnet-4-6": {Entrada: 3, Saida: 15, Tier: TierPadrao},
	"claude-fable-5":    {Entrada: 10, Saida: 50, Tier: TierAlta},
	"claude-fable-5-1":  {Entrada: 10, Saida: 50, Tier: TierAlta},
}

// PrecoDe devolve o preco de um modelo pelo id. Erro quando o modelo nao esta na
// tabela: preferimos falhar a cobrar um numero inventado do cliente.
func PrecoDe(modelo string) (Preco, error) {
	p, ok := tabela[modelo]
	if !ok {
		return Preco{}, fmt.Errorf("claudeia: modelo %q nao esta na tabela de precos", modelo)
	}
	return p, nil
}

// Uso e o que a API devolve no campo `usage` da resposta. E a MEDICAO, nao a
// estimativa: e por isso que o cliente HTTP precisa ler esse campo.
type Uso struct {
	// TokensEntrada e input_tokens.
	TokensEntrada int
	// TokensSaida e output_tokens.
	TokensSaida int
	// TokensCacheEscrita e cache_creation_input_tokens (cobrado com acrescimo).
	TokensCacheEscrita int
	// TokensCacheLeitura e cache_read_input_tokens (cobrado com desconto).
	TokensCacheLeitura int
}

// Custo calcula o custo em micro-dolares de um uso medido.
//
// LIMITE: nao aplica o acrescimo da escrita de cache nem o desconto da leitura;
// trata os dois pelo preco cheio de entrada. No fluxo de leitura de documento
// isso nao muda nada, porque o prompt e curto demais para atingir o minimo que a
// API exige para cachear. Se algum dia um caminho aqui passar a usar cache de
// verdade, esta conta precisa ser refeita.
func (p Preco) Custo(u Uso) MicroDolar {
	entrada := int64(u.TokensEntrada + u.TokensCacheEscrita + u.TokensCacheLeitura)
	return MicroDolar(entrada)*p.Entrada + MicroDolar(u.TokensSaida)*p.Saida
}

// CustoEstimado calcula o custo ANTES da chamada, a partir do tamanho da imagem
// e de uma estimativa de tokens de texto. Serve para escolher o modelo e o
// tamanho da foto sem gastar nada.
func (p Preco) CustoEstimado(larguraImagem, alturaImagem, tokensTexto, tokensSaida int) MicroDolar {
	return p.Custo(Uso{
		TokensEntrada: TokensDeImagem(larguraImagem, alturaImagem, p.Tier) + tokensTexto,
		TokensSaida:   tokensSaida,
	})
}

// EmCentavosBRL converte para centavos de real, arredondando para cima.
//
// A taxa entra como parametro de proposito: cambio e configuracao que muda todo
// dia, nao constante de codigo. Arredondamos para CIMA porque este numero vira
// custo repassado, e errar para menos e prejuizo silencioso.
//
// taxaCentavosPorDolar e quantos centavos de real vale um dolar (ex.: 540 para
// R$ 5,40).
func (m MicroDolar) EmCentavosBRL(taxaCentavosPorDolar int64) moneycents.Cents {
	if taxaCentavosPorDolar <= 0 {
		return 0
	}
	num := int64(m) * taxaCentavosPorDolar
	const microPorDolar = 1_000_000
	c := num / microPorDolar
	if num%microPorDolar != 0 {
		c++
	}
	return moneycents.Cents(c)
}

// String formata em dolares com seis casas, que e a precisao real de uma
// chamada barata. "US$ 0,008100" diz mais que "US$ 0,01".
func (m MicroDolar) String() string {
	sinal := ""
	v := int64(m)
	if v < 0 {
		sinal, v = "-", -v
	}
	return fmt.Sprintf("%sUS$ %d,%06d", sinal, v/1_000_000, v%1_000_000)
}
