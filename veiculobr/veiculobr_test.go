// Copyright 2026 imobo. Licenca: privada.

package veiculobr

import (
	"encoding/csv"
	"os"
	"testing"
)

// TestContraEstoqueRealDaHomologacao e o teste que da direito de confiar neste
// pacote. Os 524 registros de testdata/estoque-homologacao.csv vieram de
// GET /api/estoques da homologacao do RENAVE em 2026-09-07, nao de fixture
// inventada. Se o algoritmo do DV do RENAVAM estivesse errado, ele reprovaria
// documento legitimo em producao e o lojista ficaria travado.
func TestContraEstoqueRealDaHomologacao(t *testing.T) {
	f, err := os.Open("testdata/estoque-homologacao.csv")
	if err != nil {
		t.Fatalf("abrir fixture: %v", err)
	}
	defer func() { _ = f.Close() }()
	linhas, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("ler fixture: %v", err)
	}
	if len(linhas) < 2 {
		t.Fatal("fixture vazia")
	}
	var n int
	for _, l := range linhas[1:] {
		renavam, placa, chassi, numCRV, codSeg := l[0], l[1], l[2], l[3], l[4]
		n++
		if r := Renavam(renavam); !r.Valido || r.Valor != renavam {
			t.Errorf("Renavam(%q) = %+v; queria valido e inalterado", renavam, r)
		}
		if r := Placa(placa); !r.Valido || r.Valor != placa {
			t.Errorf("Placa(%q) = %+v; queria valido e inalterado", placa, r)
		}
		if r := Chassi(chassi); !r.Valido || r.Valor != chassi {
			t.Errorf("Chassi(%q) = %+v; queria valido e inalterado", chassi, r)
		}
		if r := NumeroCRV(numCRV); !r.Valido {
			t.Errorf("NumeroCRV(%q) = %+v; queria valido", numCRV, r)
		}
		if r := CodigoSegurancaCRV(codSeg); !r.Valido {
			t.Errorf("CodigoSegurancaCRV(%q) = %+v; queria valido", codSeg, r)
		}
	}
	if n != 524 {
		t.Fatalf("fixture com %d registros; esperava 524 (a amostra mudou?)", n)
	}
	t.Logf("524 registros reais da homologacao aprovados sem nenhuma correcao")
}

func TestChassi_CorrigeAsLetrasQueOVINNaoAdmite(t *testing.T) {
	// A ISO 3779 proibe I, O e Q justamente para nao confundir com 1 e 0.
	// Entao o OCR que devolve "O" leu um zero, e a correcao e certa.
	r := Chassi("9BWZZZ377VT0O4251")
	if !r.Valido || r.Status != StatusCorrigido {
		t.Fatalf("status = %s, queria corrigido: %+v", r.Status, r)
	}
	if r.Valor != "9BWZZZ377VT004251" {
		t.Errorf("valor = %q, queria 9BWZZZ377VT004251", r.Valor)
	}
	if len(r.Correcoes) != 1 || r.Correcoes[0].Posicao != 13 {
		t.Errorf("correcoes = %+v; queria uma na posicao 13", r.Correcoes)
	}
	if !r.PrecisaConferencia() {
		t.Error("campo corrigido pelo sistema tem de pedir conferencia humana")
	}
}

func TestChassi_NaoExigeDigitoVerificadorDaPosicao9(t *testing.T) {
	// O check digit da posicao 9 e do padrao norte-americano e NAO e obrigatorio
	// em veiculo fabricado no Brasil. Exigi-lo reprovaria chassi legitimo.
	for _, c := range []string{"9BD17106G48123456", "93YLSR7UHEJ123456"} {
		if r := Chassi(c); !r.Valido {
			t.Errorf("Chassi(%q) reprovou: %+v", c, r)
		}
	}
}

func TestPlaca_CorrigePelaPosicao(t *testing.T) {
	casos := []struct {
		nome, entrada, quer string
	}{
		{"zero lido no lugar de O nas tres primeiras", "0BC1234", "OBC1234"},
		{"O lido no lugar de zero na quarta", "ABCO234", "ABC0234"},
		{"minuscula e hifen", "abc-1d23", "ABC1D23"},
		{"S lido no lugar de 5 no fim", "ABC1D2S", "ABC1D25"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			r := Placa(c.entrada)
			if !r.Valido || r.Valor != c.quer {
				t.Errorf("Placa(%q) = %q (%s), queria %q", c.entrada, r.Valor, r.Status, c.quer)
			}
		})
	}
}

func TestPlaca_NaoInventaOFormatoNaQuintaPosicao(t *testing.T) {
	// A quinta posicao e a fronteira entre antiga e Mercosul. Corrigir ali seria
	// escolher o formato no lugar do documento, entao o pacote nao mexe.
	if r := Placa("ABC1D23"); FormatoDaPlaca(r.Valor) != FormatoMercosul {
		t.Error("ABC1D23 e Mercosul")
	}
	if r := Placa("ABC1234"); FormatoDaPlaca(r.Valor) != FormatoAntigo {
		t.Error("ABC1234 e formato antigo")
	}
	for _, p := range []string{"ABC1D23", "ABC1234"} {
		if r := Placa(p); r.Status != StatusValido {
			t.Errorf("Placa(%q) mexeu no campo: %+v", p, r)
		}
	}
}

func TestRenavam_PegaOErroDeLeituraPeloDV(t *testing.T) {
	// 07654972351 e real (veio da homologacao). Trocar um digito tem de reprovar:
	// e este teste que impede uma entrada de R$ 5,48 no veiculo errado.
	if r := Renavam("07654972351"); !r.Valido {
		t.Fatalf("RENAVAM real reprovou: %+v", r)
	}
	if r := Renavam("07654972352"); r.Valido || r.Status != StatusDVInvalido {
		t.Errorf("digito final trocado passou: %+v", r)
	}
	if r := Renavam("07654972331"); r.Valido {
		t.Errorf("digito do meio trocado passou: %+v", r)
	}
}

func TestRenavam_CompletaZerosAEsquerda(t *testing.T) {
	// RENAVAM antigo tinha 9 digitos e muito documento imprime sem os zeros.
	// O zero a esquerda faz parte do numero, completar nao e chute.
	r := Renavam("7654972351")
	if !r.Valido || r.Valor != "07654972351" {
		t.Fatalf("Renavam(10 digitos) = %+v; queria 07654972351", r)
	}
}

func TestRenavam_CorrigeLetraLidaPorEngano(t *testing.T) {
	r := Renavam("O7654972351") // letra O no lugar do zero inicial
	if !r.Valido || r.Valor != "07654972351" || r.Status != StatusCorrigido {
		t.Fatalf("Renavam com O inicial = %+v", r)
	}
}

func TestCamposSemDV_SoConferemTamanho(t *testing.T) {
	// Limite honesto do pacote: numeroCrv e codigoSegurancaCrv nao tem digito
	// verificador conhecido. Um digito trocado pelo OCR passa direto por aqui.
	// Este teste existe para documentar isso, nao para celebrar.
	bom := CodigoSegurancaCRV("77722711756")
	ruim := CodigoSegurancaCRV("77722711757") // um digito diferente
	if !bom.Valido || !ruim.Valido {
		t.Fatal("ambos passam: nao existe DV nesse campo")
	}
	if r := CodigoSegurancaCRV("7772271175"); r.Valido {
		t.Error("10 digitos tinha de reprovar por tamanho")
	}
}

func TestCPFeCNPJ(t *testing.T) {
	if r := CPF("529.982.247-25"); !r.Valido || r.Valor != "52998224725" {
		t.Errorf("CPF valido reprovou: %+v", r)
	}
	if r := CPF("529.982.247-26"); r.Valido {
		t.Error("CPF com DV errado passou")
	}
	if r := CPF("111.111.111-11"); r.Valido {
		t.Error("CPF de digitos repetidos passou")
	}
	if r := CNPJ("11.222.333/0001-81"); !r.Valido || r.Valor != "11222333000181" {
		t.Errorf("CNPJ valido reprovou: %+v", r)
	}
	if r := CNPJ("11.222.333/0001-82"); r.Valido {
		t.Error("CNPJ com DV errado passou")
	}
}

func TestEntradasImprestaveis(t *testing.T) {
	casos := []struct {
		nome string
		res  Resultado
		quer Status
	}{
		{"chassi vazio", Chassi("  "), StatusVazio},
		{"chassi cortado", Chassi("9BWZZZ377VT00425"), StatusTamanhoInvalido},
		{"placa vazia", Placa(""), StatusVazio},
		{"renavam vazio", Renavam("---"), StatusVazio},
		{"renavam longo demais", Renavam("076549723511"), StatusTamanhoInvalido},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if c.res.Valido || c.res.Status != c.quer {
				t.Errorf("status = %s, queria %s: %+v", c.res.Status, c.quer, c.res)
			}
			if c.res.Detalhe == "" {
				t.Error("campo invalido sem mensagem para mostrar ao lojista")
			}
		})
	}
}

// TestContraDocumentosOficiaisDeVerdade roda os campos de dois CRLV-e reais,
// assinados digitalmente pelo DETRAN, de estados e anos diferentes. E a prova de
// que o pacote nao foi calibrado so contra a homologacao do SERPRO, que e um
// ambiente de mock.
func TestContraDocumentosOficiaisDeVerdade(t *testing.T) {
	docs := []struct {
		fonte                                        string
		renavam, placa, chassi, numeroCRV, documento string
		formato                                      FormatoPlaca
	}{
		{
			fonte:   "CRLV-e DETRAN-SP, exercicio 2021",
			renavam: "01183536108", placa: "EFK8I77", chassi: "99ADJ78V7K4000189",
			numeroCRV: "213034750730", documento: "17800176000120", formato: FormatoMercosul,
		},
		{
			fonte:   "CRLV-e DETRAN-MG, exercicio 2023",
			renavam: "00852838034", placa: "HBO7C69", chassi: "9C2JD20205R016451",
			numeroCRV: "213144245208", documento: "16725962000148", formato: FormatoMercosul,
		},
	}
	for _, d := range docs {
		t.Run(d.fonte, func(t *testing.T) {
			if r := Renavam(d.renavam); !r.Valido || r.Valor != d.renavam {
				t.Errorf("Renavam: %+v", r)
			}
			if r := Placa(d.placa); !r.Valido || FormatoDaPlaca(r.Valor) != d.formato {
				t.Errorf("Placa: %+v", r)
			}
			if r := Chassi(d.chassi); !r.Valido {
				t.Errorf("Chassi: %+v", r)
			}
			if r := NumeroCRV(d.numeroCRV); !r.Valido {
				t.Errorf("NumeroCRV: %+v", r)
			}
			if r := CNPJ(d.documento); !r.Valido {
				t.Errorf("CNPJ: %+v", r)
			}
		})
	}
}

// TestChassi_AceitaAteVinteEUm segue o contrato do RENAVE, que pede
// [A-HJ-NPR-Za-hj-npr-z0-9]{17,21}, e nao a amostra da homologacao, onde todos
// tinham 17.
func TestChassi_AceitaAteVinteEUm(t *testing.T) {
	if r := Chassi("9BWZZZ377VT004251ABCD"); !r.Valido { // 21
		t.Errorf("21 caracteres reprovou: %+v", r)
	}
	if r := Chassi("9BWZZZ377VT004251ABCDE"); r.Valido { // 22
		t.Error("22 caracteres passou")
	}
}

// TestCNPJ_Alfanumerico cobre o CNPJ com letra na raiz, que o contrato do RENAVE
// ja aceita (\d{11}|[A-Za-z0-9]{12}\d{2}). O DV usa o codigo ASCII menos 48, que
// para CNPJ so de digitos cai exatamente na conta de sempre.
func TestCNPJ_Alfanumerico(t *testing.T) {
	if r := CNPJ("12ABC34501DE35"); !r.Valido {
		t.Errorf("CNPJ alfanumerico valido reprovou: %+v", r)
	}
	if r := CNPJ("12ABC34501DE36"); r.Valido {
		t.Error("CNPJ alfanumerico com DV errado passou")
	}
	if r := CNPJ("12ABC34501DEF5"); r.Valido || r.Status != StatusCaractereInvalido {
		t.Errorf("DV com letra tinha de reprovar: %+v", r)
	}
}

// TestOCodigoDoCLAPassaBatido documenta a armadilha mais cara do projeto.
//
// O CRLV-e traz um campo "CODIGO DE SEGURANCA DO CLA" com 11 digitos, que NAO
// serve para dar entrada no RENAVE. O valor abaixo e o do CRLV-e do DETRAN-SP
// que usamos de amostra. Esta funcao o aprova, e esta certa em aprovar: nao ha
// nada de errado com ele como sequencia de digitos. O erro esta em usa-lo no
// campo do CRV, e nenhuma validacao sintatica pega isso.
//
// A defesa mora na modelagem, na porta ExtratorDocumento do renave-service: um
// extrator que leu um CRLV-e nao pode preencher codigoSegurancaCrv. Este teste
// existe para que ninguem apague aquela regra achando que a validacao aqui cobre.
func TestOCodigoDoCLAPassaBatido(t *testing.T) {
	const codigoDoCLA = "81626917837" // CRLV-e DETRAN-SP, campo "CODIGO DE SEGURANCA DO CLA"
	if r := CodigoSegurancaCRV(codigoDoCLA); !r.Valido {
		t.Fatal("o teste perdeu o sentido: era para passar, e e esse o problema")
	}
}
