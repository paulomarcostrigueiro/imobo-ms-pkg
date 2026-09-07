// Copyright 2026 imobo. Licenca: privada.

package claudeia

import "testing"

// TestTokensDeImagem_ContraATabelaOficial roda a tabela publicada em
// platform.claude.com/docs/en/build-with-claude/vision, secao "Resolution and
// token cost". E ela que da o direito de usar esses numeros para orcar o custo
// do fluxo de leitura de documento. Se a Anthropic mudar a regra, este teste
// quebra e a gente fica sabendo antes da fatura.
func TestTokensDeImagem_ContraATabelaOficial(t *testing.T) {
	casos := []struct {
		largura, altura int
		padrao, alta    int
	}{
		{200, 200, 64, 64},
		{1000, 1000, 1296, 1296},
		{1092, 1092, 1521, 1521},
		{1920, 1080, 1560, 2691},
		{2000, 1500, 1564, 3888},
		{3840, 2160, 1560, 4784},
	}
	for _, c := range casos {
		if got := TokensDeImagem(c.largura, c.altura, TierPadrao); got != c.padrao {
			t.Errorf("%dx%d padrao = %d, a documentacao diz %d", c.largura, c.altura, got, c.padrao)
		}
		if got := TokensDeImagem(c.largura, c.altura, TierAlta); got != c.alta {
			t.Errorf("%dx%d alta = %d, a documentacao diz %d", c.largura, c.altura, got, c.alta)
		}
	}
}

func TestTokensDeImagem_RespeitaOTeto(t *testing.T) {
	// Foto de 12 MP direto do celular, que e o caso real do WhatsApp.
	if got := TokensDeImagem(4000, 3000, TierAlta); got > 4784 {
		t.Errorf("4000x3000 = %d tokens, estourou o teto de 4784", got)
	}
	if got := TokensDeImagem(4000, 3000, TierPadrao); got > 1568 {
		t.Errorf("4000x3000 = %d tokens, estourou o teto de 1568", got)
	}
}

func TestTokensDeImagem_EntradaImprestavel(t *testing.T) {
	for _, c := range [][2]int{{0, 100}, {100, 0}, {-1, -1}} {
		if got := TokensDeImagem(c[0], c[1], TierAlta); got != 0 {
			t.Errorf("%dx%d = %d, queria 0", c[0], c[1], got)
		}
	}
}
