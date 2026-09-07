// Copyright 2026 imobo. Licenca: privada.

package claudeia

import "testing"

// TestCusto_ConfereComOExemploDaDocumentacao usa os tres exemplos de custo que a
// pagina de visao publica: mil imagens 1000x1000 custam "cerca de US$ 1,30" no
// Haiku 4.5 (tier padrao) e "cerca de US$ 6,48" no Opus 5 (tier alta), e mil
// imagens 4K saem a "cerca de US$ 23,92" no Opus 5.
//
// Os dois do Opus batem no centavo. O do Haiku da US$ 1,296 e a documentacao
// arredondou para 1,30, entao a conferencia e em decimos de centavo.
func TestCusto_ConfereComOExemploDaDocumentacao(t *testing.T) {
	casos := []struct {
		nome            string
		p               Preco
		largura, altura int
		milDecimoCent   int64 // custo de MIL imagens, em decimos de centavo de dolar
		naDoc           string
	}{
		{"Haiku 4.5, 1000x1000", Haiku45, 1000, 1000, 1296, "cerca de US$ 1,30"},
		{"Opus 5, 1000x1000", Opus5, 1000, 1000, 6480, "cerca de US$ 6,48"},
		{"Opus 5, 3840x2160", Opus5, 3840, 2160, 23920, "cerca de US$ 23,92"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			um := c.p.Custo(Uso{TokensEntrada: TokensDeImagem(c.largura, c.altura, c.p.Tier)})
			mil := int64(um) * 1000 / 1000 // micro-dolares de mil imagens / 1000 = decimos de centavo
			if mil != c.milDecimoCent {
				t.Errorf("mil imagens = %d decimos de centavo de dolar, esperava %d (%s na documentacao)",
					mil, c.milDecimoCent, c.naDoc)
			}
		})
	}
}

func TestPrecoDe_ModeloDesconhecidoFalha(t *testing.T) {
	// Preferimos falhar a cobrar um numero inventado do cliente.
	if _, err := PrecoDe("claude-inexistente"); err == nil {
		t.Error("modelo fora da tabela devia dar erro")
	}
	p, err := PrecoDe("claude-sonnet-5")
	if err != nil || p != Sonnet5 {
		t.Errorf("PrecoDe(sonnet-5) = %+v, %v", p, err)
	}
}

func TestCusto_SomaEntradaESaida(t *testing.T) {
	// Sonnet 5: US$ 2 entrada / US$ 10 saida por milhao.
	// 2000 de entrada + 300 de saida = 2000*2 + 300*10 = 7000 micro-dolares.
	got := Sonnet5.Custo(Uso{TokensEntrada: 2000, TokensSaida: 300})
	if got != 7000 {
		t.Errorf("custo = %d micro-dolares, queria 7000", got)
	}
	if got.String() != "US$ 0,007000" {
		t.Errorf("String() = %q", got.String())
	}
}

func TestEmCentavosBRL_ArredondaParaCima(t *testing.T) {
	// Errar para menos num custo repassado e prejuizo silencioso.
	// 7000 micro-dolares a R$ 5,40 = 7000*540/1e6 = 3,78 centavos -> 4.
	if got := MicroDolar(7000).EmCentavosBRL(540); got != 4 {
		t.Errorf("= %d centavos, queria 4", got)
	}
	if got := MicroDolar(0).EmCentavosBRL(540); got != 0 {
		t.Errorf("zero = %d", got)
	}
	if got := MicroDolar(7000).EmCentavosBRL(0); got != 0 {
		t.Errorf("taxa invalida devia dar 0, deu %d", got)
	}
}
