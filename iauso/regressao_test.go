// Copyright 2026 imobo. Licenca: privada.

// Regressoes dos achados da revisao independente de 08/11/2026. Cada teste
// reproduz o cenario que quebrava.
package iauso

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/paulomarcostrigueiro/imobo-ms-pkg/claudeia"
)

// anthropicGzip comprime a resposta sempre que o pedido aceita gzip, como
// faria uma CDN no caminho.
func anthropicGzip(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			_, _ = io.WriteString(zw, respostaOK)
			_ = zw.Close()
			return
		}
		_, _ = io.WriteString(w, respostaOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Achado 1: no modo microsservico com resposta gzip, o custo saia 0.
func TestGzipMicrosservicoMedeOCusto(t *testing.T) {
	api := anthropicGzip(t)
	ms, gMS, fMS := iaService(t, api.URL, nil)
	cfg := cfgBiblioteca()
	cfg.Modo, cfg.URLMicrosservico = ModoMicrosservico, ms.URL
	tr, _ := NovoTransporte(cfg, nil, nil, nil, nil)
	resp, err := tr.Cliente(5 * time.Second).Do(novoPedido(t, context.Background(), "https://api.anthropic.com", chaveA))
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, respostaOK, string(b), "o servico recebe o JSON legivel")
	fechar(t, fMS)
	require.Len(t, gMS.registros(), 1)
	assert.Equal(t, claudeia.MicroDolar(8625), gMS.registros()[0].Custo)
}

// Achado 1 (outra ponta): base que nao descomprime sozinha.
func TestGzipComBaseSemDescompressaoMedeOCusto(t *testing.T) {
	api := anthropicGzip(t)
	g := &memGravador{}
	f := NovaFila(g, nil)
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DisableCompression = true
	tr, _ := NovoTransporte(cfgBiblioteca(), &forcaGzip{base}, f, nil, nil)
	_, err := tr.Cliente(5 * time.Second).Do(novoPedido(t, context.Background(), api.URL, chaveA))
	require.NoError(t, err)
	fechar(t, f)
	require.Len(t, g.registros(), 1)
	assert.Equal(t, claudeia.MicroDolar(8625), g.registros()[0].Custo)
}

type forcaGzip struct{ rt http.RoundTripper }

func (f *forcaGzip) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Accept-Encoding", "gzip")
	return f.rt.RoundTrip(r)
}

// Achado 1 (salvaguarda): 200 sem usage legivel nunca vira custo 0.
func TestRespostaSemUsageEstimaPorCima(t *testing.T) {
	api := novaAnthropic(t, 200, `{"id":"msg_x","content":[]}`)
	g := &memGravador{}
	f := NovaFila(g, nil)
	tr, _ := NovoTransporte(cfgBiblioteca(), nil, f, nil, nil)
	_, err := tr.Cliente(5 * time.Second).Do(novoPedido(t, context.Background(), api.srv.URL, chaveA))
	require.NoError(t, err)
	fechar(t, f)
	require.Len(t, g.registros(), 1)
	r := g.registros()[0]
	assert.Positive(t, int64(r.Custo))
	assert.False(t, r.PrecoConhecido, "marcado como estimado")
	assert.Equal(t, 10, r.TokensSaida, "saida = max_tokens do pedido")
}

// Achado 2: um registro ruim travava todos. Agora so ele sai (para o log).
type gravadorSeletivo struct{ memGravador }

func (g *gravadorSeletivo) Gravar(ctx context.Context, rs []Registro) error {
	var bons []Registro
	inv := &ErrRegistrosInvalidos{Causa: errors.New("value too long")}
	for i, r := range rs {
		if r.Modelo == "ruim" {
			inv.Indices = append(inv.Indices, i)
			continue
		}
		bons = append(bons, r)
	}
	_ = g.memGravador.Gravar(ctx, bons)
	if len(inv.Indices) > 0 {
		return inv
	}
	return nil
}

func TestRegistroInvalidoNaoTravaAFila(t *testing.T) {
	g := &gravadorSeletivo{}
	f := NovaFila(g, nil)
	f.Enviar(Registro{ID: uuid.New(), Modelo: "ruim"})
	for range 5 {
		f.Enviar(Registro{ID: uuid.New(), Modelo: "bom"})
	}
	time.Sleep(1500 * time.Millisecond) // um tique do timer, sem Fechar
	assert.Len(t, g.registros(), 5)
	fechar(t, f)
	assert.Len(t, g.registros(), 5)
}

func TestSanearCortaOsCampos(t *testing.T) {
	r := sanear(Registro{
		Servico: strings.Repeat("s", 100), Modelo: "a\x00b" + strings.Repeat("m", 200),
		ConversaID: strings.Repeat("ç", 90), Custo: -5, TokensEntrada: -1,
	})
	assert.Len(t, r.Servico, 40)
	assert.Equal(t, 80, len([]rune(r.Modelo)))
	assert.NotContains(t, r.Modelo, "\x00")
	assert.Equal(t, 80, len([]rune(r.ConversaID)))
	assert.Zero(t, r.Custo)
	assert.Zero(t, r.TokensEntrada)
}

// Achado 3: banco pendurado fazia cada chamada esperar o timeout do cliente.
type gravadorPendurado struct{ memGravador }

func (g *gravadorPendurado) GastoDesde(ctx context.Context, _ string, _ time.Time) (claudeia.MicroDolar, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}

func TestBancoPenduradoNaoSeguraAIA(t *testing.T) {
	api := novaAnthropic(t, 200, respostaOK)
	g := &gravadorPendurado{}
	teto := NovoTeto(LimitesFixos{"B": {Credito: 1, Teto: 1, DiaRenovacao: 1}}, g, &memAvisador{}, nil)
	tr, _ := NovoTransporte(cfgBiblioteca(), nil, nil, teto, nil)
	inicio := time.Now()
	for range 5 {
		resp, err := tr.Cliente(10 * time.Second).Do(novoPedido(t, context.Background(), api.srv.URL, chaveA))
		require.NoError(t, err)
		_ = resp.Body.Close()
	}
	// 1 consulta de 500 ms; as outras 4 usam a tentativa guardada (backoff).
	assert.Less(t, time.Since(inicio), 2*time.Second)
}

// Achado 6: a releitura do banco apagava o que estava somado em memoria (ainda
// na fila), e o teto deixava de bloquear.
func TestReleituraNaoApagaGastoEmMemoria(t *testing.T) {
	g := &memGravador{} // banco ve 0: nada foi gravado ainda
	teto := NovoTeto(LimitesFixos{"A": {Credito: 100, Teto: 100, DiaRenovacao: 1}}, g, &memAvisador{}, nil)
	teto.validade = 0 // relê o banco toda vez
	ctx := context.Background()
	teto.Verificar(ctx, "A")
	teto.Somar(ctx, "A", 500)
	assert.True(t, teto.Verificar(ctx, "A").Bloqueado, "releu 0 do banco, mas o somado continua valendo")
}

// Suspeita da revisao: com cabecalho, qualquer um na rede interna furava o
// teto mandando X-Imobo-Organizacao: chave_tenant. Com impressoes, vale a chave.
func TestProxyIdentificaOrganizacaoPelaChave(t *testing.T) {
	api := novaAnthropic(t, 200, respostaOK)
	g := &memGravador{}
	f := NovaFila(g, nil)
	tr, _ := NovoTransporte(Config{Servico: "ia-service", Modo: ModoBiblioteca}, nil, f, nil, nil)
	p, _ := NovoProxy(api.srv.URL, tr, nil)
	imp, err := ParseImpressoes("A:" + impressao(chaveA))
	require.NoError(t, err)
	srv := httptest.NewServer(p.ComImpressoes(imp))
	defer srv.Close()

	for _, chave := range []string{chaveA, "sk-ant-de-um-tenant"} {
		req := novoPedido(t, context.Background(), srv.URL, chave)
		req.Header.Set(HeaderOrganizacao, OrganizacaoChaveTenant) // tentativa de furar
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}
	fechar(t, f)
	require.Len(t, g.registros(), 2)
	assert.Equal(t, "A", g.registros()[0].Organizacao, "a chave da IMOBO vale mais que o cabecalho")
	assert.Equal(t, OrganizacaoChaveTenant, g.registros()[1].Organizacao)

	_, err = ParseImpressoes("A:curta")
	assert.Error(t, err)
}

// Suspeita da revisao: stream cortado saia como ok.
func TestStreamCortadoEFalhaDeRede(t *testing.T) {
	g := &memGravador{}
	f := NovaFila(g, nil)
	tr, _ := NovoTransporte(cfgBiblioteca(), nil, f, nil, nil)
	l := &leitorSSE{orig: io.NopCloser(&leitorQuebra{bytes.NewBufferString(
		"data: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-haiku-4-5\",\"usage\":{\"input_tokens\":5}}}\n")}),
		fim: func(b []byte, e error) {
			r := lerSSE(b)
			if e != nil {
				r.Status = StatusFalhaRede
			}
			tr.registrar(context.Background(), origem{Servico: "fiscal", Organizacao: "B", Modo: ModoBiblioteca}, r, time.Now())
		}}
	_, _ = io.ReadAll(l)
	fechar(t, f)
	require.Len(t, g.registros(), 1)
	assert.Equal(t, StatusFalhaRede, g.registros()[0].Status)
	assert.Equal(t, 5, g.registros()[0].TokensEntrada, "o uso parcial fica")
}

type leitorQuebra struct{ b *bytes.Buffer }

func (l *leitorQuebra) Read(p []byte) (int, error) {
	n, err := l.b.Read(p)
	if err == io.EOF {
		return n, errors.New("conexao caiu")
	}
	return n, err
}

// Achado 7: Enviar depois de Fechar sumia calado (e podia travar).
func TestEnviarDepoisDeFecharNaoTrava(t *testing.T) {
	g := &memGravador{}
	f := NovaFila(g, nil)
	fechar(t, f)
	f.Enviar(Registro{ID: uuid.New()})
	assert.Empty(t, g.registros())
}
