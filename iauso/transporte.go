// Copyright 2026 imobo. Licenca: privada.

package iauso

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Cabecalhos entre o servico e o ia-service. Nunca vao para a Anthropic.
const (
	HeaderServico        = "X-Imobo-Servico"
	HeaderFuncionalidade = "X-Imobo-Funcionalidade"
	HeaderTenant         = "X-Imobo-Tenant"
	HeaderConversa       = "X-Imobo-Conversa"
	HeaderOrganizacao    = "X-Imobo-Organizacao"
	// HeaderRegistrado = "1" na resposta do ia-service: o uso ja foi gravado
	// la, o servico nao grava de novo.
	HeaderRegistrado = "X-Imobo-Ia-Registrado"
	// HeaderTeto = "1" na resposta de teto atingido.
	HeaderTeto = "X-Imobo-Ia-Teto"
)

// TipoErroTeto e o error.type da resposta de teto atingido.
const TipoErroTeto = "imobo_teto_atingido"

// StatusTeto e o HTTP status do teto atingido. 402 de proposito: 429 faria
// clientes com retry automatico insistirem.
const StatusTeto = http.StatusPaymentRequired

// Transporte e o http.RoundTripper que registra e controla o uso. Ver doc.go.
type Transporte struct {
	cfg          Config
	direto       http.RoundTripper
	ms           http.RoundTripper
	fila         *Fila
	teto         *Teto
	log          *slog.Logger
	chaveServico string
	agora        func() time.Time
}

// NovoTransporte monta o transporte. base nil = transporte padrao do Go.
// fila nil = nao grava (so para testes). teto nil = nunca bloqueia.
func NovoTransporte(cfg Config, base http.RoundTripper, fila *Fila, teto *Teto, log *slog.Logger) (*Transporte, error) {
	if err := cfg.validar(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	if base == nil {
		base = http.DefaultTransport.(*http.Transport).Clone()
	}
	ms := http.DefaultTransport.(*http.Transport).Clone()
	ms.DialContext = (&net.Dialer{Timeout: cfg.TempoConexaoMicrosservico, KeepAlive: 30 * time.Second}).DialContext
	// Sem HTTP_PROXY ate o ia-service: com proxy no meio, ia-service fora vira
	// 502 do proxy (conectou) e o plano B nunca dispara.
	ms.Proxy = nil
	if strings.TrimSpace(cfg.ChaveServico) != "" && cfg.Organizacao == "" {
		log.Error("iauso: servico com chave e sem " + EnvOrganizacao + "; uso gravado como nao_configurada e SEM teto")
	}
	return &Transporte{
		cfg: cfg, direto: base, ms: ms, fila: fila, teto: teto, log: log,
		chaveServico: impressao(cfg.ChaveServico),
		agora:        time.Now,
	}, nil
}

// Cliente devolve um *http.Client com este transporte.
func (t *Transporte) Cliente(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: t}
}

// Modo e o modo configurado.
func (t *Transporte) Modo() Modo { return t.cfg.Modo }

func ehMessages(r *http.Request) bool {
	return r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v1/messages")
}

// RoundTrip implementa http.RoundTripper.
func (t *Transporte) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.cfg.Modo == ModoDesligado || !ehMessages(req) {
		return t.direto.RoundTrip(req)
	}
	inicio := t.agora()
	corpo, err := lerCorpo(req)
	if err != nil {
		return nil, err
	}
	o := t.origemDa(req)
	ped := lerPedido(corpo)
	modelo := ped.Modelo

	if t.cfg.Modo == ModoMicrosservico && o.Modo == "" {
		resp, err := t.viaMicrosservico(req, corpo, o)
		if err == nil {
			if resp.Header.Get(HeaderRegistrado) == "1" {
				return resp, nil
			}
			o.Modo = ModoMicrosservico
			return t.medir(resp, o, ped, inicio), nil
		}
		if !ehFalhaDeConexao(err) || req.Context().Err() != nil {
			// Conectou e falhou depois: o pedido pode ja ter ido a Anthropic.
			// Nao repete (cobraria duas vezes) e nao grava (o ia-service grava).
			return nil, err
		}
		t.log.Warn("iauso: ia-service nao conectou; plano B (Anthropic direto)", "servico", o.Servico, "erro", err)
		o.Modo = ModoPlanoB
	}
	if o.Modo == "" {
		o.Modo = ModoBiblioteca
	}

	if t.teto != nil && o.Organizacao != OrganizacaoChaveTenant {
		if s := t.teto.Verificar(req.Context(), o.Organizacao); s.Bloqueado {
			t.registrar(req.Context(), o, Registro{Modelo: modelo, Status: StatusTetoAtingido, HTTPStatus: StatusTeto}, inicio)
			return respostaTeto(req, o.Organizacao, s), nil
		}
	}

	r2 := req.Clone(req.Context())
	removerCabecalhosImobo(r2.Header)
	// Sem Accept-Encoding explicito, o http.Transport pede gzip e descomprime
	// sozinho; com ele (ex.: repassado pelo proxy), entregaria bytes gzip e o
	// usage nao seria lido (custo 0, teto cego).
	r2.Header.Del("Accept-Encoding")
	porCorpo(r2, corpo)
	resp, err := t.direto.RoundTrip(r2)
	if err != nil {
		t.registrar(req.Context(), o, Registro{Modelo: modelo, Status: StatusFalhaRede}, inicio)
		return nil, err
	}
	return t.medir(resp, o, ped, inicio), nil
}

// origemDa monta a origem: do ctx quando vem do Proxy, senao da Config.
func (t *Transporte) origemDa(req *http.Request) origem {
	ctx := req.Context()
	if o, ok := origemDe(ctx); ok {
		return o
	}
	o := origem{
		Servico:        t.cfg.Servico,
		Funcionalidade: funcionalidadeDe(ctx),
		TenantID:       tenantDe(ctx),
		ConversaID:     conversaDe(ctx),
		Organizacao:    OrganizacaoChaveTenant,
	}
	if t.chaveServico != "" && impressao(req.Header.Get("x-api-key")) == t.chaveServico {
		o.Organizacao = t.cfg.Organizacao
		if o.Organizacao == "" {
			o.Organizacao = "nao_configurada"
		}
	}
	return o
}

func (t *Transporte) viaMicrosservico(req *http.Request, corpo []byte, o origem) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	u := *req.URL
	alvo, err := http.NewRequest(http.MethodPost, t.cfg.URLMicrosservico, nil)
	if err != nil {
		return nil, fmt.Errorf("iauso: %s invalido: %w", EnvURLMicrosservico, err)
	}
	u.Scheme, u.Host = alvo.URL.Scheme, alvo.URL.Host
	u.Path = strings.TrimRight(alvo.URL.Path, "/") + req.URL.Path
	r2.URL, r2.Host = &u, ""
	removerCabecalhosImobo(r2.Header)
	r2.Header.Del("Accept-Encoding")
	r2.Header.Set(HeaderServico, o.Servico)
	r2.Header.Set(HeaderFuncionalidade, o.Funcionalidade)
	r2.Header.Set(HeaderOrganizacao, o.Organizacao)
	if o.TenantID != nil {
		r2.Header.Set(HeaderTenant, o.TenantID.String())
	}
	if o.ConversaID != "" {
		r2.Header.Set(HeaderConversa, o.ConversaID)
	}
	porCorpo(r2, corpo)
	return t.ms.RoundTrip(r2)
}

// pedido e o que se le do corpo do pedido para registrar e estimar.
type pedido struct {
	Modelo    string
	MaxTokens int
	Bytes     int
}

func lerPedido(corpo []byte) pedido {
	var v struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
	}
	_ = json.Unmarshal(corpo, &v)
	return pedido{Modelo: v.Model, MaxTokens: v.MaxTokens, Bytes: len(corpo)}
}

// estimar preenche tokens por cima quando uma resposta 200 nao trouxe usage
// legivel: entrada = bytes do pedido / 3, saida = max_tokens, preco do mais caro.
// Melhor cobrar a mais do que deixar o teto cego.
func (t *Transporte) estimar(r *Registro, ped pedido) {
	if r.Status != StatusOK || r.TokensEntrada+r.TokensSaida+r.TokensCacheEscrita+r.TokensCacheLeitura > 0 {
		return
	}
	r.TokensEntrada = ped.Bytes/3 + 1
	r.TokensSaida = ped.MaxTokens
	r.estimado = true
	t.log.Error("iauso: resposta 200 sem usage legivel; custo ESTIMADO por cima", "modelo", r.Modelo)
}

// medir le o usage da resposta (sem atrapalhar quem le o corpo depois) e grava.
func (t *Transporte) medir(resp *http.Response, o origem, ped pedido, inicio time.Time) *http.Response {
	ctx := context.Background()
	if resp.Request != nil {
		ctx = resp.Request.Context()
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body = &leitorSSE{
			orig: resp.Body,
			fim: func(b []byte, errLeitura error) {
				r := lerSSE(descomprimir(resp.Header, b))
				r.HTTPStatus = resp.StatusCode
				r.RequestID = primeiro(resp.Header.Get("request-id"), r.RequestID)
				if r.Modelo == "" {
					r.Modelo = ped.Modelo
				}
				if errLeitura != nil && r.Status == StatusOK {
					r.Status = StatusFalhaRede // stream cortado: tokens parciais
				}
				t.estimar(&r, ped)
				t.registrar(ctx, o, r, inicio)
			},
		}
		return resp
	}
	b, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(b))
	if err != nil {
		t.registrar(ctx, o, Registro{Modelo: ped.Modelo, Status: StatusFalhaRede, HTTPStatus: resp.StatusCode}, inicio)
		return resp
	}
	r := lerResposta(resp.StatusCode, descomprimir(resp.Header, b))
	r.RequestID = primeiro(resp.Header.Get("request-id"), r.RequestID)
	if r.Modelo == "" {
		r.Modelo = ped.Modelo
	}
	t.estimar(&r, ped)
	t.registrar(ctx, o, r, inicio)
	return resp
}

// descomprimir devolve o corpo legivel para a MEDICAO (o corpo entregue a quem
// chamou nao muda). So gzip; outro encoding volta como veio.
func descomprimir(h http.Header, b []byte) []byte {
	if !strings.EqualFold(strings.TrimSpace(h.Get("Content-Encoding")), "gzip") {
		return b
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return b
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, 64<<20))
	if err != nil {
		return b
	}
	return out
}

// registrar completa e envia o registro. r traz modelo, tokens e status.
func (t *Transporte) registrar(ctx context.Context, o origem, r Registro, inicio time.Time) {
	agora := t.agora()
	r.ID = uuid.New()
	r.CriadoEm = agora
	r.TenantID = o.TenantID
	r.Servico = o.Servico
	r.Funcionalidade = o.Funcionalidade
	r.Organizacao = o.Organizacao
	r.Modo = o.Modo
	r.ConversaID = o.ConversaID
	r.DuracaoMS = agora.Sub(inicio).Milliseconds()
	if r.Status == StatusOK || r.TokensEntrada+r.TokensSaida+r.TokensCacheEscrita+r.TokensCacheLeitura > 0 {
		p, conhecido := precoDoModelo(r.Modelo)
		if r.estimado {
			p, conhecido = precoMaisCaro, false
		}
		r.PrecoConhecido = conhecido
		r.Custo = custoDe(p, usoMedido{
			Entrada: r.TokensEntrada, Saida: r.TokensSaida, CacheLeitura: r.TokensCacheLeitura,
			CacheEscrita5m: r.TokensCacheEscrita - r.cache1h, CacheEscrita1h: r.cache1h,
		})
		if !conhecido && !r.estimado {
			t.log.Warn("iauso: modelo fora da tabela de precos; custo pelo mais caro", "modelo", r.Modelo)
		}
	} else {
		r.PrecoConhecido = true
	}
	if t.fila != nil {
		t.fila.Enviar(r)
	}
	if t.teto != nil && r.Organizacao != OrganizacaoChaveTenant {
		t.teto.Somar(ctx, r.Organizacao, r.Custo)
	}
}

// ---- leitura da resposta da Messages API ----

type wireUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreation            *struct {
		Ephemeral5m int `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

type wireErro struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type wireResposta struct {
	ID    string     `json:"id"`
	Model string     `json:"model"`
	Usage *wireUsage `json:"usage"`
	Error *wireErro  `json:"error"`
}

func (u *wireUsage) aplicar(r *Registro) {
	if u == nil {
		return
	}
	if u.InputTokens > 0 {
		r.TokensEntrada = u.InputTokens
	}
	if u.OutputTokens > 0 {
		r.TokensSaida = u.OutputTokens
	}
	if u.CacheReadInputTokens > 0 {
		r.TokensCacheLeitura = u.CacheReadInputTokens
	}
	if u.CacheCreationInputTokens > 0 {
		r.TokensCacheEscrita = u.CacheCreationInputTokens
		r.cache1h = 0
		if u.CacheCreation != nil {
			r.cache1h = min(u.CacheCreation.Ephemeral1h, u.CacheCreationInputTokens)
		}
	}
}

func statusDoErro(e *wireErro) Status {
	if e != nil && strings.Contains(strings.ToLower(e.Message), "credit balance is too low") {
		return StatusSemCredito
	}
	return StatusErro
}

func lerResposta(httpStatus int, b []byte) Registro {
	r := Registro{HTTPStatus: httpStatus, Status: StatusOK}
	var w wireResposta
	_ = json.Unmarshal(b, &w)
	if httpStatus != http.StatusOK {
		r.Status = statusDoErro(w.Error)
		if r.Status == StatusErro && w.Error == nil && bytes.Contains(bytes.ToLower(b), []byte("credit balance is too low")) {
			r.Status = StatusSemCredito
		}
	}
	r.Modelo = w.Model
	r.RequestID = w.ID
	w.Usage.aplicar(&r)
	return r
}

// lerSSE soma o usage de um stream: message_start traz entrada e cache,
// message_delta traz a saida acumulada.
func lerSSE(b []byte) Registro {
	r := Registro{Status: StatusOK}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		linha := sc.Text()
		if !strings.HasPrefix(linha, "data:") {
			continue
		}
		var ev struct {
			Type    string        `json:"type"`
			Message *wireResposta `json:"message"`
			Usage   *wireUsage    `json:"usage"`
			Error   *wireErro     `json:"error"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(linha[5:])), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				r.Modelo, r.RequestID = ev.Message.Model, ev.Message.ID
				ev.Message.Usage.aplicar(&r)
			}
		case "message_delta":
			ev.Usage.aplicar(&r)
		case "error":
			r.Status = statusDoErro(ev.Error)
		}
	}
	return r
}

// leitorSSE repassa o stream e, no fim, entrega tudo o que passou para fim().
// errLeitura e nil quando o stream terminou normalmente (EOF).
type leitorSSE struct {
	orig io.ReadCloser
	mu   sync.Mutex
	buf  bytes.Buffer
	fim  func(b []byte, errLeitura error)
	once sync.Once
}

func (l *leitorSSE) terminar(err error) {
	l.once.Do(func() {
		l.mu.Lock()
		b := append([]byte(nil), l.buf.Bytes()...)
		l.mu.Unlock()
		l.fim(b, err)
	})
}

func (l *leitorSSE) Read(p []byte) (int, error) {
	n, err := l.orig.Read(p)
	l.mu.Lock()
	l.buf.Write(p[:n])
	l.mu.Unlock()
	if err == io.EOF {
		l.terminar(nil)
	} else if err != nil {
		l.terminar(err)
	}
	return n, err
}

// Close antes do fim (quem chamou desistiu) conta como stream cortado.
func (l *leitorSSE) Close() error {
	l.terminar(errors.New("stream fechado antes do fim"))
	return l.orig.Close()
}

// ---- utilitarios ----

func lerCorpo(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("iauso: ler pedido: %w", err)
	}
	return b, nil
}

func porCorpo(r *http.Request, corpo []byte) {
	r.Body = io.NopCloser(bytes.NewReader(corpo))
	r.ContentLength = int64(len(corpo))
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(corpo)), nil }
}

func removerCabecalhosImobo(h http.Header) {
	for k := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), "X-Imobo-") {
			h.Del(k)
		}
	}
}

// ehFalhaDeConexao: so erro ANTES de conectar (discagem/DNS) libera o plano B.
func ehFalhaDeConexao(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	var dns *net.DNSError
	return errors.As(err, &dns)
}

func primeiro(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// respostaTeto e a resposta sintetica de teto atingido, no formato de erro da
// Messages API, para o cliente de cada servico tratar como qualquer erro.
func respostaTeto(req *http.Request, org string, s Situacao) *http.Response {
	msg := fmt.Sprintf("Teto mensal de IA da organizacao %s atingido (gasto no ciclo %s; credito %s + teto %s). "+
		"Aumente pelo link enviado ao WhatsApp do responsavel.", org, s.Gasto, s.Limite.Credito, s.Limite.Teto)
	b, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": TipoErroTeto, "message": msg}})
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set(HeaderTeto, "1")
	return &http.Response{
		Status: fmt.Sprintf("%d %s", StatusTeto, http.StatusText(StatusTeto)), StatusCode: StatusTeto,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: h, Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b)), Request: req,
	}
}

// EhTetoAtingido diz se uma resposta e o bloqueio do teto (para o servico
// mostrar uma mensagem clara em vez de "erro da IA").
func EhTetoAtingido(status int, corpo []byte) bool {
	if status != StatusTeto {
		return false
	}
	var w wireResposta
	return json.Unmarshal(corpo, &w) == nil && w.Error != nil && w.Error.Type == TipoErroTeto
}

var _ http.RoundTripper = (*Transporte)(nil)
