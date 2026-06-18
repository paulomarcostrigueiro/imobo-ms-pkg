// Copyright 2026 imobo. Licenca: privada.

package tenantctx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// Erros canonicos retornados pelo middleware HTTP.
var (
	// ErrAuthorizationAusente indica que o header Authorization nao foi
	// enviado. O handler nem chega a ser chamado — middleware retorna 401.
	ErrAuthorizationAusente = errors.New("tenantctx: authorization header ausente")

	// ErrAuthorizationInvalido indica que o header Authorization existe mas
	// nao segue o formato `Bearer <token>`.
	ErrAuthorizationInvalido = errors.New("tenantctx: authorization header invalido (esperado: Bearer <token>)")

	// ErrJWTInvalido indica que o JWT validator rejeitou o token (assinatura
	// invalida, expirado, claims malformados).
	ErrJWTInvalido = errors.New("tenantctx: JWT invalido")

	// ErrClaimsInvalidos indica que os claims do JWT nao puderam ser parseados
	// para os campos esperados (ex.: `acting_as_tenant` nao e UUID valido).
	ErrClaimsInvalidos = errors.New("tenantctx: claims do JWT invalidos")
)

// Claims representa os claims relevantes extraidos do JWT.
//
// Campos sao strings (e nao uuid.UUID) porque o JWT serializa UUIDs como string
// e e responsabilidade do middleware fazer o parse + validacao.
type Claims struct {
	// Sub e o `sub` do JWT — identificador do usuario autenticado. Vai virar
	// ActedByUserID no TenantContext.
	Sub string

	// HomeTenantID e o tenant root/home do usuario (claim `tenant_root_id` no
	// imobo-platform). Para operadores IMOBO e o tenant root; para admins
	// SaaS e o proprio tenant.
	HomeTenantID string

	// ActingAsTenantID e o tenant que o usuario esta operando AGORA (claim
	// `acting_as_tenant`). No SaaS puro e igual ao HomeTenantID; no BPaaS e o
	// tenant do pequeno corretor que o operador esta operando.
	ActingAsTenantID string

	// Cargo e o papel canonico do usuario (claim `cargo`). Vazio quando o token
	// for de servico (cargo opcional). Consumido por handlers que precisam
	// autorizar baseado no cargo (e.g. /usuarios admin-only).
	Cargo string

	// Permissions sao as permissoes do usuario no tenant operado.
	Permissions []string
}

// JWTValidator valida tokens JWT e retorna os claims canonicos.
//
// E uma interface explicita (em vez de funcao) para facilitar mocking em testes
// e permitir multiplas implementacoes (HMAC simples para dev, RSA com JWKS para
// producao via identity-service).
type JWTValidator interface {
	// Validate aceita o token bruto (sem o prefixo `Bearer`) e retorna os
	// claims ou erro. O ctx serve para propagar timeout/cancelamento ao
	// chamar identity-service ou cache de chaves publicas.
	Validate(ctx context.Context, token string) (Claims, error)
}

// VisibleTenantsResolver resolve a lista de tenants visiveis para um dado
// usuario+papel+tenant operado, aplicando a hierarquia BPaaS quando aplicavel.
//
// Para SaaS puro, retorna [actingAs]. Para operador IMOBO atuando em tenant
// pai, retorna [actingAs + descendentes]. Implementacao concreta consulta a
// tabela `usuario_tenant_acesso` (ADR-002 secao 5).
//
// E uma interface separada (e nao parte de JWTValidator) porque a logica de
// hierarquia depende do banco de dados, e nao do JWT.
type VisibleTenantsResolver interface {
	// Resolve retorna a lista de tenants visiveis e indica se o usuario e
	// operador IMOBO (master cross-tenant).
	Resolve(ctx context.Context, userID, actingAs uuid.UUID) (visible []uuid.UUID, isMaster bool, err error)
}

// staticResolver e a implementacao default do VisibleTenantsResolver para
// quando nao houver banco de dados disponivel (ex.: testes unitarios do
// middleware sem testcontainers, ou apps simples SaaS-only). Sempre retorna
// [actingAs] e isMaster=false.
//
// Em producao, sempre injetar um resolver que consulte `usuario_tenant_acesso`.
type staticResolver struct{}

func (staticResolver) Resolve(_ context.Context, _, actingAs uuid.UUID) ([]uuid.UUID, bool, error) {
	return []uuid.UUID{actingAs}, false, nil
}

// HTTPMiddlewareConfig agrupa as dependencias e opcoes do middleware HTTP.
type HTTPMiddlewareConfig struct {
	// JWTValidator e obrigatorio.
	JWTValidator JWTValidator

	// VisibleTenantsResolver e opcional. Se nil, usa staticResolver (lista
	// unitaria com actingAs, isMaster=false).
	VisibleTenantsResolver VisibleTenantsResolver

	// SkipPaths sao prefixos de path que NAO requerem autenticacao
	// (ex.: "/health", "/metrics"). Se nil, nenhum path e pulado.
	SkipPaths []string
}

// HTTPMiddleware extrai o JWT do header Authorization, valida via JWTValidator
// injetado, computa a hierarquia de tenants visiveis e popula o TenantContext
// no request context.Context.
//
// Antes de qualquer handler de /api/*, este middleware DEVE rodar. Caso o
// handler tente extrair o TenantContext via `MustFrom` sem ter passado por
// aqui, panica.
//
// Comportamento de erro:
//   - Authorization ausente => 401 + JSON {error: "..."}
//   - Authorization formato invalido => 401
//   - JWT invalido (assinatura/exp) => 401
//   - Claims malformados (UUID invalido) => 401
//   - VisibleTenantsResolver retorna erro => 500
//
// Em todos os casos de erro, o handler downstream NAO e chamado.
func HTTPMiddleware(cfg HTTPMiddlewareConfig) func(http.Handler) http.Handler {
	if cfg.JWTValidator == nil {
		panic("tenantctx: HTTPMiddleware exige JWTValidator nao-nulo")
	}
	resolver := cfg.VisibleTenantsResolver
	if resolver == nil {
		resolver = staticResolver{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip de paths publicos (healthcheck, metrics).
			for _, prefix := range cfg.SkipPaths {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}

			ctx := r.Context()
			tc, err := buildTenantContext(ctx, r, cfg.JWTValidator, resolver)
			if err != nil {
				writeAuthError(w, r, err)
				return
			}

			next.ServeHTTP(w, r.WithContext(Inject(ctx, tc)))
		})
	}
}

// buildTenantContext encapsula a logica de extrair JWT, validar e montar o
// TenantContext. Separado da funcao acima para facilitar teste unitario sem
// servidor HTTP real.
func buildTenantContext(
	ctx context.Context,
	r *http.Request,
	validator JWTValidator,
	resolver VisibleTenantsResolver,
) (TenantContext, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return TenantContext{}, ErrAuthorizationAusente
	}

	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(header, bearerPrefix) {
		return TenantContext{}, ErrAuthorizationInvalido
	}
	token := strings.TrimSpace(header[len(bearerPrefix):])
	if token == "" {
		return TenantContext{}, ErrAuthorizationInvalido
	}

	claims, err := validator.Validate(ctx, token)
	if err != nil {
		return TenantContext{}, fmt.Errorf("%w: %v", ErrJWTInvalido, err)
	}

	userID, err := uuid.Parse(claims.Sub)
	if err != nil {
		return TenantContext{}, fmt.Errorf("%w: sub nao e UUID valido: %v", ErrClaimsInvalidos, err)
	}

	homeTenantID, err := uuid.Parse(claims.HomeTenantID)
	if err != nil {
		return TenantContext{}, fmt.Errorf("%w: home_tenant_id nao e UUID valido: %v", ErrClaimsInvalidos, err)
	}

	actingAs, err := uuid.Parse(claims.ActingAsTenantID)
	if err != nil {
		return TenantContext{}, fmt.Errorf("%w: acting_as_tenant nao e UUID valido: %v", ErrClaimsInvalidos, err)
	}

	visible, isMaster, err := resolver.Resolve(ctx, userID, actingAs)
	if err != nil {
		return TenantContext{}, fmt.Errorf("tenantctx: resolver falhou: %w", err)
	}
	if len(visible) == 0 {
		// Fallback seguro: ao menos o tenant operado.
		visible = []uuid.UUID{actingAs}
	}

	// Promoção SUPER-MASTER IMOBO: se o home tenant do usuario eh o master
	// root UUID (00000000-...), eleva IsMasterImobo=true independente do que
	// o resolver disser. Padrao Impersonate (Stripe/Slack/GitHub) — Paulo
	// 2026-06-03: super-master conserva poderes ao atuar em tenant inferior,
	// mas o cargo virtual no JWT vira ADMIN_IMOBILIARIA pra UI renderizar
	// como o admin real veria. Flag IsMasterImobo decide gates de permissao.
	if IsMasterRootTenant(homeTenantID) {
		isMaster = true
	}

	return TenantContext{
		ActedAsTenantID:  actingAs,
		ActedAsUserID:    userID, // por padrao, igual ao acted_by; em BPaaS o resolver/handler pode sobrescrever
		ActedByUserID:    userID,
		HomeTenantID:     homeTenantID,
		Cargo:            claims.Cargo,
		Permissions:      append([]string(nil), claims.Permissions...),
		VisibleTenantIDs: visible,
		IsMasterImobo:    isMaster,
	}, nil
}

// writeAuthError escreve uma resposta HTTP de erro padronizada (401 ou 500),
// e loga estruturadamente o motivo (sem expor PII no body publico).
func writeAuthError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusUnauthorized
	var body string

	switch {
	case errors.Is(err, ErrAuthorizationAusente):
		body = `{"error":"authorization_required"}`
	case errors.Is(err, ErrAuthorizationInvalido):
		body = `{"error":"authorization_invalid"}`
	case errors.Is(err, ErrJWTInvalido):
		body = `{"error":"token_invalid"}`
	case errors.Is(err, ErrClaimsInvalidos):
		body = `{"error":"claims_invalid"}`
	default:
		// Erro de resolver (banco de dados, cache) — 500 sem detalhes.
		status = http.StatusInternalServerError
		body = `{"error":"internal_error"}`
	}

	slog.WarnContext(r.Context(), "tenantctx: middleware bloqueou request",
		"path", r.URL.Path,
		"method", r.Method,
		"err", err.Error(),
		"status", status,
	)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
