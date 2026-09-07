// Copyright 2026 imobo. Licenca: privada.

// Package claudeia calcula o custo de uma chamada a Claude ANTES e DEPOIS dela.
//
// MOTIVACAO: no fluxo do RENAVE o lojista manda a foto do documento pelo
// WhatsApp e a gente le os campos com visao. Isso e uma chamada paga por token,
// e o numero de tokens de uma imagem NAO depende do que esta escrito nela, so do
// tamanho em pixels. Ou seja, mandar a foto de 12 megapixels que o celular tirou
// custa varias vezes mais que a mesma foto reduzida, sem ler nada a mais.
// Reduzir antes de mandar e a economia mais barata que existe neste fluxo, e
// para decidir o tamanho e preciso saber a conta.
//
// A regra e da documentacao oficial (platform.claude.com/docs/en/build-with-claude/vision,
// secao "Resolution and token cost"): Claude ve a imagem em blocos de 28x28
// pixels, e cada bloco e um token visual. Entao
//
//	tokens = teto(largura/28) * teto(altura/28)
//
// com um limite por modelo, aplicado reduzindo a imagem antes de contar.
//
// Os numeros deste arquivo sao conferidos no teste contra a tabela publicada na
// mesma pagina, linha por linha. Se a Anthropic mudar a regra, o teste quebra.
package claudeia

// Tier e o limite de resolucao nativa do modelo.
type Tier string

const (
	// TierAlta vale para Claude 4.7 e modelos posteriores (inclui Opus 5,
	// Sonnet 5 e a familia Fable): lado maior 2576 px, teto de 4784 tokens.
	TierAlta Tier = "alta"
	// TierPadrao vale para os demais modelos (inclui Haiku 4.5): lado maior
	// 1568 px, teto de 1568 tokens.
	TierPadrao Tier = "padrao"
)

// limites de cada tier, conforme a tabela oficial.
func (t Tier) limites() (ladoMaior, tetoTokens int) {
	if t == TierAlta {
		return 2576, 4784
	}
	return 1568, 1568
}

// TokensDeImagem devolve quantos tokens visuais uma imagem de largura x altura
// custa no tier informado, ja aplicando a reducao que a API faz sozinha quando a
// imagem estoura o lado maior ou o teto de tokens.
//
// Serve para decidir em que tamanho reduzir a foto antes de mandar: da para
// varrer tamanhos e ver onde o custo para de subir.
func TokensDeImagem(largura, altura int, t Tier) int {
	if largura <= 0 || altura <= 0 {
		return 0
	}
	l, a := reduzirParaOTier(largura, altura, t)
	return teto(l, 28) * teto(a, 28)
}

// reduzirParaOTier aplica a mesma reducao que a API: encolhe preservando a
// proporcao ate caber no lado maior E no teto de tokens.
func reduzirParaOTier(largura, altura int, t Tier) (int, int) {
	ladoMaior, tetoTokens := t.limites()

	// 1) encolhe pelo lado maior.
	if m := max(largura, altura); m > ladoMaior {
		f := float64(ladoMaior) / float64(m)
		largura = maxInt(1, int(float64(largura)*f))
		altura = maxInt(1, int(float64(altura)*f))
	}
	// 2) se ainda estoura o teto de tokens, encolhe de novo. A busca e por
	// bisseccao no fator de escala porque a conta tem teto e nao e continua.
	if teto(largura, 28)*teto(altura, 28) <= tetoTokens {
		return largura, altura
	}
	lo, hi := 0.0, 1.0
	for range 60 {
		mid := (lo + hi) / 2
		l := maxInt(1, int(float64(largura)*mid))
		a := maxInt(1, int(float64(altura)*mid))
		if teto(l, 28)*teto(a, 28) <= tetoTokens {
			lo = mid
		} else {
			hi = mid
		}
	}
	return maxInt(1, int(float64(largura)*lo)), maxInt(1, int(float64(altura)*lo))
}

func teto(v, d int) int { return (v + d - 1) / d }
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
