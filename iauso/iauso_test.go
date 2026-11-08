// Copyright 2026 imobo. Licenca: privada.

package iauso

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/paulomarcostrigueiro/imobo-ms-pkg/claudeia"
	"github.com/paulomarcostrigueiro/imobo-ms-pkg/tenantctx"
)

const chaveA = "sk-ant-chave-do-servico"

// ---- dubles ----

type memGravador struct {
	mu        sync.Mutex
	rs        []Registro
	gasto     claudeia.MicroDolar
	falhas    int // quantas vezes Gravar ainda falha
	erroGasto error
}

func (m *memGravador) Gravar(_ context.Context, rs []Registro) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.falhas > 0 {
		m.falhas--
		return errors.New("banco fora do ar")
	}
	m.rs = append(m.rs, rs...)
	return nil
}

func (m *memGravador) GastoDesde(context.Context, string, time.Time) (claudeia.MicroDolar, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gasto, m.erroGasto
}

func (m *memGravador) registros() []Registro {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Registro(nil), m.rs...)
}

type memAvisador struct {
	mu  sync.Mutex
	avs []Aviso
}

func (a *memAvisador) Avisar(_ context.Context, av Aviso) error {
	a.mu.Lock()
	a.avs = append(a.avs, av)
	a.mu.Unlock()
	return nil
}

func (a *memAvisador) lista() []Aviso {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Aviso(nil), a.avs...)
}

// anthropicFalsa responde como a Messages API e conta as chamadas. Guarda os
// cabecalhos recebidos para conferir que X-Imobo-* nunca chega la.
type anthropicFalsa struct {
	srv      *httptest.Server
	chamadas atomic.Int32
	mu       sync.Mutex
	headers  []http.Header
}

func novaAnthropic(t *testing.T, status int, corpo string) *anthropicFalsa {
	a := &anthropicFalsa{}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.chamadas.Add(1)
		a.mu.Lock()
		a.headers = append(a.headers, r.Header.Clone())
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("request-id", "req_123")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, corpo)
	}))
	t.Cleanup(a.srv.Close)
	return a
}

const respostaOK = `{"id":"msg_1","model":"claude-sonnet-4-6","content":[{"type":"text","text":"oi"}],
"usage":{"input_tokens":1000,"output_tokens":200,"cache_creation_input_tokens":400,"cache_read_input_tokens":3000,
"cache_creation":{"ephemeral_5m_input_tokens":300,"ephemeral_1h_input_tokens":100}}}`

func pedido(t *testing.T, ctx context.Context, base, chave string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-4-6","max_tokens":10,"messages":[{"role":"user","content":"segredo do cliente"}]}`))
	require.NoError(t, err)
	req.Header.Set("x-api-key", chave)
	req.Header.Set("anthropic-version", "2023-06-01")
	return req
}

func fechar(t *testing.T, f *Fila) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.Fechar(ctx)
}

func cfgBiblioteca() Config {
	return Config{Servico: "fiscal", Modo: ModoBiblioteca, Organizacao: "B", ChaveServico: chaveA}
}

// ---- config ----

func TestParseUSD(t *testing.T) {
	casos := map[string]claudeia.MicroDolar{"200": 200_000_000, "2.5": 2_500_000, "0.000001": 1, "50.00": 50_000_000}
	for s, want := range casos {
		got, err := ParseUSD(s)
		require.NoError(t, err, s)
		assert.Equal(t, want, got, s)
	}
	for _, s := range []string{"", "-1", "1.0000001", "abc", "1.x"} {
		_, err := ParseUSD(s)
		assert.Error(t, err, s)
	}
}

func TestParseLimites(t *testing.T) {
	l, err := ParseLimites("A:200:50:15, B:200:2.5:3")
	require.NoError(t, err)
	assert.Equal(t, Limite{Credito: 200_000_000, Teto: 50_000_000, DiaRenovacao: 15}, l["A"])
	assert.Equal(t, claudeia.MicroDolar(202_500_000), l["B"].Total())

	vazio, err := ParseLimites("")
	require.NoError(t, err)
	assert.Empty(t, vazio)

	for _, s := range []string{"A:200:50", "A:200:50:0", "A:200:50:32", "A:1:1:1,A:1:1:1", "chave_tenant:1:1:1", ":1:1:1"} {
		_, err := ParseLimites(s)
		assert.Error(t, err, s)
	}
}

func TestInicioDoCiclo(t *testing.T) {
	brt := fusoBrasilia
	casos := []struct {
		agora time.Time
		dia   int
		want  time.Time
	}{
		{time.Date(2026, 11, 20, 10, 0, 0, 0, brt), 15, time.Date(2026, 11, 15, 0, 0, 0, 0, brt)},
		{time.Date(2026, 11, 10, 10, 0, 0, 0, brt), 15, time.Date(2026, 10, 15, 0, 0, 0, 0, brt)},
		{time.Date(2026, 1, 3, 10, 0, 0, 0, brt), 15, time.Date(2025, 12, 15, 0, 0, 0, 0, brt)},
		{time.Date(2026, 3, 1, 10, 0, 0, 0, brt), 31, time.Date(2026, 2, 28, 0, 0, 0, 0, brt)},
		{time.Date(2026, 11, 15, 0, 0, 0, 0, brt), 15, time.Date(2026, 11, 15, 0, 0, 0, 0, brt)},
		// 02:00 UTC do dia 15 ainda e dia 14 em Brasilia.
		{time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC), 15, time.Date(2026, 10, 15, 0, 0, 0, 0, brt)},
	}
	for _, c := range casos {
		assert.True(t, c.want.Equal(InicioDoCiclo(c.agora, c.dia)), "%v dia %d", c.agora, c.dia)
	}
}

func TestConfigValidar(t *testing.T) {
	_, err := NovoTransporte(Config{Servico: "x", Modo: "outro"}, nil, nil, nil, nil)
	assert.Error(t, err)
	_, err = NovoTransporte(Config{Servico: "x", Modo: ModoMicrosservico}, nil, nil, nil, nil)
	assert.Error(t, err, "microsservico sem URL")
	_, err = NovoTransporte(Config{Modo: ModoBiblioteca}, nil, nil, nil, nil)
	assert.Error(t, err, "sem servico")
	tr, err := NovoTransporte(Config{Servico: "x"}, nil, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, ModoBiblioteca, tr.Modo(), "padrao e biblioteca")
}

// ---- custo ----

func TestCustoComCache(t *testing.T) {
	p := claudeia.Preco{Entrada: 3, Saida: 15}
	u := usoMedido{Entrada: 1000, Saida: 200, CacheLeitura: 3000, CacheEscrita5m: 300, CacheEscrita1h: 100}
	// 1000*3 + 200*15 + 300*3*1,25 + 100*3*2 + 3000*3*0,1 = 3000+3000+1125+600+900
	assert.Equal(t, claudeia.MicroDolar(8625), custoDe(p, u))
	// arredonda para cima: 1 token de leitura a 3 micro = 0,3 -> 1
	assert.Equal(t, claudeia.MicroDolar(1), custoDe(p, usoMedido{CacheLeitura: 1}))
}

func TestPrecoDoModelo(t *testing.T) {
	p, ok := precoDoModelo("claude-haiku-4-5-20251001")
	assert.True(t, ok)
	assert.Equal(t, claudeia.Haiku45.Entrada, p.Entrada)
	p, ok = precoDoModelo("claude-inventado-9")
	assert.False(t, ok)
	assert.Equal(t, precoMaisCaro, p, "desconhecido usa o mais caro")
}

// ---- transporte, modo biblioteca ----

func TestBibliotecaRegistraEDevolveCorpo(t *testing.T) {
	api := novaAnthropic(t, 200, respostaOK)
	g := &memGravador{}
	f := NovaFila(g, nil)
	tr, err := NovoTransporte(cfgBiblioteca(), nil, f, nil, nil)
	require.NoError(t, err)

	tenant := uuid.New()
	ctx := tenantctx.Inject(context.Background(), tenantctx.TenantContext{ActedAsTenantID: tenant})
	ctx = ComFuncionalidade(ctx, "importacao_colunas")
	resp, err := tr.Cliente(5*time.Second).Do(pedido(t, ctx, api.srv.URL, chaveA))
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, respostaOK, string(b), "quem chama le o corpo intacto")
	fechar(t, f)

	rs := g.registros()
	require.Len(t, rs, 1)
	r := rs[0]
	assert.Equal(t, "fiscal", r.Servico)
	assert.Equal(t, "importacao_colunas", r.Funcionalidade)
	assert.Equal(t, "B", r.Organizacao)
	assert.Equal(t, ModoBiblioteca, r.Modo)
	assert.Equal(t, tenant, *r.TenantID)
	assert.Equal(t, "claude-sonnet-4-6", r.Modelo)
	assert.Equal(t, StatusOK, r.Status)
	assert.Equal(t, 200, r.HTTPStatus)
	assert.Equal(t, "req_123", r.RequestID)
	assert.Equal(t, [4]int{1000, 200, 400, 3000}, [4]int{r.TokensEntrada, r.TokensSaida, r.TokensCacheEscrita, r.TokensCacheLeitura})
	assert.Equal(t, claudeia.MicroDolar(8625), r.Custo)
	assert.True(t, r.PrecoConhecido)
	assert.NotEqual(t, uuid.Nil, r.ID)
}

func TestChaveDoTenantNaoContaNoTeto(t *testing.T) {
	api := novaAnthropic(t, 200, respostaOK)
	g := &memGravador{gasto: 999_000_000} // muito acima do limite
	f := NovaFila(g, nil)
	teto := NovoTeto(LimitesFixos{"B": {Credito: 1, Teto: 1, DiaRenovacao: 1}}, g, &memAvisador{}, nil)
	tr, _ := NovoTransporte(cfgBiblioteca(), nil, f, teto, nil)

	resp, err := tr.Cliente(5*time.Second).Do(pedido(t, context.Background(), api.srv.URL, "sk-ant-chave-do-tenant"))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode, "chave do tenant nunca bloqueia")
	fechar(t, f)
	require.Len(t, g.registros(), 1)
	assert.Equal(t, OrganizacaoChaveTenant, g.registros()[0].Organizacao)
	assert.Equal(t, FuncionalidadeNaoInformada, g.registros()[0].Funcionalidade)
}

func TestTetoAtingidoNaoChamaAnthropic(t *testing.T) {
	api := novaAnthropic(t, 200, respostaOK)
	g := &memGravador{gasto: 250_000_000}
	f := NovaFila(g, nil)
	av := &memAvisador{}
	teto := NovoTeto(LimitesFixos{"B": {Credito: 200_000_000, Teto: 50_000_000, DiaRenovacao: 1}}, g, av, nil)
	tr, _ := NovoTransporte(cfgBiblioteca(), nil, f, teto, nil)

	resp, err := tr.Cliente(5*time.Second).Do(pedido(t, context.Background(), api.srv.URL, chaveA))
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	assert.Equal(t, StatusTeto, resp.StatusCode)
	assert.Equal(t, "1", resp.Header.Get(HeaderTeto))
	assert.True(t, EhTetoAtingido(resp.StatusCode, b))
	assert.Contains(t, string(b), "organizacao B")
	assert.Zero(t, api.chamadas.Load(), "nao chamou a Anthropic")
	fechar(t, f)
	require.Len(t, g.registros(), 1)
	assert.Equal(t, StatusTetoAtingido, g.registros()[0].Status)
	assert.Zero(t, g.registros()[0].Custo)
	assert.Eventually(t, func() bool { return len(av.lista()) == 1 }, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, Nivel100, av.lista()[0].Nivel)
}

func TestAvisos80e100UmaVezPorCiclo(t *testing.T) {
	g := &memGravador{gasto: 200_000_000}
	av := &memAvisador{}
	teto := NovoTeto(LimitesFixos{"A": {Credito: 200_000_000, Teto: 10_000_000, DiaRenovacao: 1}}, g, av, nil)
	ctx := context.Background()

	assert.False(t, teto.Verificar(ctx, "A").Bloqueado)
	teto.Somar(ctx, "A", 8_000_000) // 80% do teto
	teto.Somar(ctx, "A", 1_000_000) // 90%: nao repete
	teto.Somar(ctx, "A", 1_000_000) // 100%
	assert.True(t, teto.Verificar(ctx, "A").Bloqueado, "Somar bloqueia sem esperar a proxima leitura")
	teto.Somar(ctx, "A", 1_000_000)

	assert.Eventually(t, func() bool { return len(av.lista()) == 2 }, 2*time.Second, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	niveis := []Nivel{}
	for _, a := range av.lista() {
		niveis = append(niveis, a.Nivel)
	}
	assert.ElementsMatch(t, []Nivel{Nivel80, Nivel100}, niveis)
}

func TestBancoForaDoArNaoParaAIA(t *testing.T) {
	api := novaAnthropic(t, 200, respostaOK)
	g := &memGravador{erroGasto: errors.New("fora"), falhas: 1}
	f := NovaFila(g, nil)
	teto := NovoTeto(LimitesFixos{"B": {Credito: 1, Teto: 1, DiaRenovacao: 1}}, g, nil, nil)
	tr, _ := NovoTransporte(cfgBiblioteca(), nil, f, teto, nil)

	resp, err := tr.Cliente(5*time.Second).Do(pedido(t, context.Background(), api.srv.URL, chaveA))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode, "sem gasto lido, nao bloqueia")
	fechar(t, f)
	assert.Len(t, g.registros(), 1, "o registro pendente entra quando o banco volta")
}

func TestSemCredito(t *testing.T) {
	api := novaAnthropic(t, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API."}}`)
	g := &memGravador{}
	f := NovaFila(g, nil)
	tr, _ := NovoTransporte(cfgBiblioteca(), nil, f, nil, nil)
	resp, err := tr.Cliente(5*time.Second).Do(pedido(t, context.Background(), api.srv.URL, chaveA))
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode, "o servico ve o erro original")
	fechar(t, f)
	require.Len(t, g.registros(), 1)
	assert.Equal(t, StatusSemCredito, g.registros()[0].Status)
	assert.Equal(t, "claude-sonnet-4-6", g.registros()[0].Modelo, "modelo do pedido quando a resposta nao traz")
}

func TestStreamSSE(t *testing.T) {
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_s\",\"model\":\"claude-haiku-4-5\",\"usage\":{\"input_tokens\":50,\"output_tokens\":1,\"cache_read_input_tokens\":10}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"oi\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":30}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer srv.Close()
	g := &memGravador{}
	f := NovaFila(g, nil)
	tr, _ := NovoTransporte(cfgBiblioteca(), nil, f, nil, nil)
	resp, err := tr.Cliente(5*time.Second).Do(pedido(t, context.Background(), srv.URL, chaveA))
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, sse, string(b), "stream repassado intacto")
	fechar(t, f)
	require.Len(t, g.registros(), 1)
	r := g.registros()[0]
	assert.Equal(t, [3]int{50, 30, 10}, [3]int{r.TokensEntrada, r.TokensSaida, r.TokensCacheLeitura})
	assert.Equal(t, "msg_s", r.RequestID)
	// 50*1 + 30*5 + 10*1*0,1 = 50 + 150 + 1
	assert.Equal(t, claudeia.MicroDolar(201), r.Custo)
}

func TestOutrasRotasEModoDesligadoPassamDireto(t *testing.T) {
	api := novaAnthropic(t, 200, `{"data":[]}`)
	g := &memGravador{}
	f := NovaFila(g, nil)
	tr, _ := NovoTransporte(cfgBiblioteca(), nil, f, nil, nil)
	req, _ := http.NewRequest(http.MethodGet, api.srv.URL+"/v1/models", nil)
	_, err := tr.Cliente(5*time.Second).Do(req)
	require.NoError(t, err)

	cfg := cfgBiblioteca()
	cfg.Modo = ModoDesligado
	des, _ := NovoTransporte(cfg, nil, f, nil, nil)
	_, err = des.Cliente(5*time.Second).Do(pedido(t, context.Background(), api.srv.URL, chaveA))
	require.NoError(t, err)
	fechar(t, f)
	assert.Empty(t, g.registros())
	assert.EqualValues(t, 2, api.chamadas.Load())
}

// ---- modo microsservico ----

// iaService sobe um ia-service de verdade (Proxy + Transporte) na frente da
// Anthropic falsa.
func iaService(t *testing.T, upstream string, teto *Teto) (*httptest.Server, *memGravador, *Fila) {
	g := &memGravador{}
	f := NovaFila(g, nil)
	tr, err := NovoTransporte(Config{Servico: "ia-service", Modo: ModoBiblioteca}, nil, f, teto, nil)
	require.NoError(t, err)
	p, err := NovoProxy(upstream, tr, nil)
	require.NoError(t, err)
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	return srv, g, f
}

func TestMicrosservicoRegistraNoIaService(t *testing.T) {
	api := novaAnthropic(t, 200, respostaOK)
	ms, gMS, fMS := iaService(t, api.srv.URL, nil)

	gLocal := &memGravador{}
	fLocal := NovaFila(gLocal, nil)
	cfg := cfgBiblioteca()
	cfg.Modo, cfg.URLMicrosservico = ModoMicrosservico, ms.URL
	tr, err := NovoTransporte(cfg, nil, fLocal, nil, nil)
	require.NoError(t, err)

	tenant := uuid.New()
	ctx := ComConversa(ComTenant(ComFuncionalidade(context.Background(), "assistente_coletor"), tenant), "conv-9")
	// o servico chama o endereco da Anthropic; o transporte desvia para o ia-service
	resp, err := tr.Cliente(5*time.Second).Do(pedido(t, ctx, "https://api.anthropic.com", chaveA))
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, respostaOK, string(b))
	fechar(t, fLocal)
	fechar(t, fMS)

	assert.Empty(t, gLocal.registros(), "nao grava duas vezes")
	require.Len(t, gMS.registros(), 1)
	r := gMS.registros()[0]
	assert.Equal(t, "fiscal", r.Servico)
	assert.Equal(t, "assistente_coletor", r.Funcionalidade)
	assert.Equal(t, "B", r.Organizacao)
	assert.Equal(t, ModoMicrosservico, r.Modo)
	assert.Equal(t, tenant, *r.TenantID)
	assert.Equal(t, "conv-9", r.ConversaID)
	assert.Equal(t, claudeia.MicroDolar(8625), r.Custo, "mesma conta do modo biblioteca")

	require.Len(t, api.headers, 1)
	for k := range api.headers[0] {
		assert.False(t, strings.HasPrefix(k, "X-Imobo-"), "cabecalho %s vazou para a Anthropic", k)
	}
	assert.Equal(t, chaveA, api.headers[0].Get("x-api-key"), "a chave passa como veio")
}

func TestMicrosservicoAplicaOTeto(t *testing.T) {
	api := novaAnthropic(t, 200, respostaOK)
	gTeto := &memGravador{gasto: 300_000_000}
	teto := NovoTeto(LimitesFixos{"B": {Credito: 200_000_000, Teto: 50_000_000, DiaRenovacao: 1}}, gTeto, &memAvisador{}, nil)
	ms, _, fMS := iaService(t, api.srv.URL, teto)
	defer fechar(t, fMS)

	cfg := cfgBiblioteca()
	cfg.Modo, cfg.URLMicrosservico = ModoMicrosservico, ms.URL
	tr, _ := NovoTransporte(cfg, nil, nil, nil, nil)
	resp, err := tr.Cliente(5*time.Second).Do(pedido(t, context.Background(), "https://api.anthropic.com", chaveA))
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	assert.True(t, EhTetoAtingido(resp.StatusCode, b), "mesma resposta de teto nos dois modos")
	assert.Zero(t, api.chamadas.Load())
}

func TestPlanoBQuandoIaServiceNaoConecta(t *testing.T) {
	api := novaAnthropic(t, 200, respostaOK)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	morto := "http://" + l.Addr().String()
	require.NoError(t, l.Close()) // porta fechada: conexao recusada

	g := &memGravador{}
	f := NovaFila(g, nil)
	cfg := cfgBiblioteca()
	cfg.Modo, cfg.URLMicrosservico = ModoMicrosservico, morto
	tr, _ := NovoTransporte(cfg, nil, f, nil, nil)
	resp, err := tr.Cliente(5*time.Second).Do(pedido(t, context.Background(), api.srv.URL, chaveA))
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	fechar(t, f)
	require.Len(t, g.registros(), 1)
	assert.Equal(t, ModoPlanoB, g.registros()[0].Modo)
	assert.EqualValues(t, 1, api.chamadas.Load())
	for k := range api.headers[0] {
		assert.False(t, strings.HasPrefix(k, "X-Imobo-"), "cabecalho %s vazou no plano B", k)
	}
}

func TestSemPlanoBQuandoConectouECaiu(t *testing.T) {
	api := novaAnthropic(t, 200, respostaOK)
	// aceita a conexao, le o pedido e derruba sem responder
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = http.ReadRequest(bufio.NewReader(c))
			_ = c.Close()
		}
	}()
	cfg := cfgBiblioteca()
	cfg.Modo, cfg.URLMicrosservico = ModoMicrosservico, fmt.Sprintf("http://%s", l.Addr())
	tr, _ := NovoTransporte(cfg, nil, nil, nil, nil)
	_, err = tr.Cliente(5*time.Second).Do(pedido(t, context.Background(), api.srv.URL, chaveA))
	assert.Error(t, err)
	assert.Zero(t, api.chamadas.Load(), "nao repete direto: poderia cobrar duas vezes")
}

// ---- fila e SQL ----

func TestFilaFecharGravaPendentes(t *testing.T) {
	g := &memGravador{}
	f := NovaFila(g, nil)
	for range 250 {
		f.Enviar(Registro{ID: uuid.New(), Status: StatusOK, Modo: ModoBiblioteca})
	}
	fechar(t, f)
	assert.Len(t, g.registros(), 250)
}

func TestSQLTabelaTemAsColunasDoInsert(t *testing.T) {
	sql := SQLTabela("fiscal", "fiscal_app")
	assert.Contains(t, sql, "CREATE TABLE IF NOT EXISTS fiscal.ia_uso")
	assert.Contains(t, sql, "UUID PRIMARY KEY DEFAULT gen_random_uuid()")
	assert.Contains(t, sql, "GRANT SELECT, INSERT ON fiscal.ia_uso TO fiscal_app")
	for _, c := range []string{"id", "criado_em", "tenant_id", "servico", "funcionalidade", "organizacao", "modo", "modelo",
		"tokens_entrada", "tokens_saida", "tokens_cache_escrita", "tokens_cache_leitura", "custo_micro_usd",
		"preco_conhecido", "request_id_anthropic", "status", "http_status", "duracao_ms", "conversa_id"} {
		assert.Contains(t, sql, "\n    "+c+" ", "coluna %s", c)
	}
	for _, s := range []Status{StatusOK, StatusErro, StatusSemCredito, StatusTetoAtingido, StatusFalhaRede} {
		assert.Contains(t, sql, "'"+string(s)+"'")
	}
	for _, m := range []Modo{ModoBiblioteca, ModoMicrosservico, ModoPlanoB} {
		assert.Contains(t, sql, "'"+string(m)+"'")
	}
	_, err := NovoPostgresGravador(nil, "fiscal; DROP")
	assert.Error(t, err)
}
