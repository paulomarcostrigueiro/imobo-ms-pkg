// Copyright 2026 imobo. Licenca: privada.

package iauso

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// Proxy e o ia-service: recebe o pedido do servico no MESMO formato da
// Messages API, com os cabecalhos X-Imobo-*, e repassa a Anthropic pelo
// Transporte (em modo biblioteca). Teto, custo e registro sao os mesmos do modo
// biblioteca porque o codigo e o mesmo.
//
// A chave (x-api-key) vem do proprio servico e passa como veio: o ia-service nao
// guarda chave nenhuma, e por isso o plano B continua possivel.
type Proxy struct {
	rp  *httputil.ReverseProxy
	log *slog.Logger
}

// NovoProxy monta o proxy. upstream vazio = https://api.anthropic.com.
// t deve estar em ModoBiblioteca.
func NovoProxy(upstream string, t *Transporte, log *slog.Logger) (*Proxy, error) {
	if upstream == "" {
		upstream = "https://api.anthropic.com"
	}
	u, err := url.Parse(strings.TrimRight(upstream, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("iauso: upstream invalido: %q", upstream)
	}
	if t == nil || t.Modo() != ModoBiblioteca {
		return nil, fmt.Errorf("iauso: o proxy exige um Transporte em modo biblioteca")
	}
	if log == nil {
		log = slog.Default()
	}
	p := &Proxy{log: log}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(u)
			pr.Out.Host = u.Host
			removerCabecalhosImobo(pr.Out.Header)
		},
		Transport: t,
		ModifyResponse: func(resp *http.Response) error {
			if resp.Request != nil && ehMessages(resp.Request) {
				resp.Header.Set(HeaderRegistrado, "1")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Warn("iauso: proxy sem resposta da Anthropic", "erro", err)
			if ehMessages(r) {
				w.Header().Set(HeaderRegistrado, "1") // o Transporte ja gravou falha_rede
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "error",
				"error": map[string]string{"type": "api_error", "message": "ia-service: Anthropic nao respondeu"}})
		},
	}
	return p, nil
}

// ServeHTTP atende /v1/*.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		http.NotFound(w, r)
		return
	}
	o := origem{
		Servico:        cabecalho(r, HeaderServico, "nao_informado"),
		Funcionalidade: cabecalho(r, HeaderFuncionalidade, FuncionalidadeNaoInformada),
		Organizacao:    cabecalho(r, HeaderOrganizacao, "nao_informada"),
		ConversaID:     strings.TrimSpace(r.Header.Get(HeaderConversa)),
		Modo:           ModoMicrosservico,
	}
	if id, err := uuid.Parse(strings.TrimSpace(r.Header.Get(HeaderTenant))); err == nil && id != uuid.Nil {
		o.TenantID = &id
	}
	p.rp.ServeHTTP(w, r.WithContext(comOrigem(r.Context(), o)))
}

func cabecalho(r *http.Request, nome, padrao string) string {
	v := strings.TrimSpace(r.Header.Get(nome))
	if v == "" || len(v) > 60 {
		return padrao
	}
	return v
}
