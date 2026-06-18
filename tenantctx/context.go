// Copyright 2026 imobo. Licenca: privada.

// Package tenantctx implementa o contexto multi-tenant hierarquico do imobo-platform-go.
//
// Esta e a contrapartida Go da biblioteca `imobo-tenant-context-spring` (Java),
// canonicamente especificada em:
//
//   - ADR-002 (RLS multi-tenant hierarquico): regra fundadora.
//   - ADR-007 (microsservicos agressivos desde MVP): cada servico aplica RLS no
//     seu proprio schema.
//   - ADR-014 (Strangler Fig Java to Go): contexto da migracao gradual.
//
// REGRAS INVIOLAVEIS:
//   - acted_as_user_id e acted_by_user_id sao SEMPRE preenchidos. No SaaS puro,
//     sao iguais. No BPaaS (operador IMOBO atuando em tenant cliente), sao diferentes.
//   - VisibleTenantIDs e a lista que vai pro `SET LOCAL app.tenant_ids_visiveis`
//     em cada transacao. Para SaaS puro: lista unitaria com ActedAsTenantID.
//     Para operador IMOBO em BPaaS: ActedAsTenantID + descendentes na arvore.
//   - HomeTenantID e o tenant "casa" do usuario; mantido para distinguir de
//     ActedAsTenantID em logs/auditoria.
//   - IsMasterImobo identifica usuario com papel OPERADOR_IMOBO (cross-tenant).
//
// Uso tipico:
//
//	ctx := tenantctx.Inject(req.Context(), tc)
//	err := tenantctx.WithTenantContext(ctx, db, func(tx pgx.Tx) error {
//	    // queries aqui ja respeitam RLS automaticamente
//	    return nil
//	})
package tenantctx

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// MasterRootTenantUUID e o tenant_id reservado para a IMOBO (operadora da
// plataforma). Usuarios cujo HomeTenantID coincide com esta constante sao
// SUPER-MASTER IMOBO e ganham IsMasterImobo=true automaticamente, mesmo
// se o cargo no DB esteja como ADMIN_IMOBILIARIA (Paulo 2026-06-03).
//
// Padrao Impersonate (Stripe/Slack/GitHub): super-master pode "atuar como"
// outro tenant — UI renderiza como o admin real veria (cargo virtual
// ADMIN_IMOBILIARIA), mas as gates de permissao master continuam ativas
// via IsMasterImobo flag.
var MasterRootTenantUUID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

// IsMasterRootTenant retorna true se o tenant id passado for o master root
// (operadora IMOBO). Usado pelo HTTPMiddleware pra promover IsMasterImobo
// quando home_tenant == master root.
func IsMasterRootTenant(tenantID uuid.UUID) bool {
	return tenantID == MasterRootTenantUUID
}

// ctxKey e o tipo nao-exportado usado como chave do TenantContext no
// context.Context. Usar tipo proprio (e nao string) evita colisoes com outros
// pacotes que poderiam usar a mesma string como chave.
type ctxKey struct{}

// tenantCtxKey e a chave singleton usada por Inject/From para armazenar e
// recuperar o TenantContext em context.Context.
var tenantCtxKey = ctxKey{}

// TenantContext carrega informacoes de tenant + usuario para cada request HTTP,
// mensagem RabbitMQ ou job assincrono.
//
// E o veiculo canonico que substituiu o `TenantAcessoService` (eliminado pela
// ADR-004). Cada request popula este struct via `HTTPMiddleware`, e cada query
// Postgres extrai daqui os valores de `SET LOCAL app.tenant_ids_visiveis`,
// `acted_as_user_id` e `acted_by_user_id`.
type TenantContext struct {
	// ActedAsTenantID e o tenant onde a acao acontece (RLS apply).
	// Sempre presente. No SaaS puro = HomeTenantID. No BPaaS, e o tenant do
	// pequeno corretor que o operador IMOBO esta operando.
	ActedAsTenantID uuid.UUID

	// ActedAsUserID e o usuario "ativo" do tenant operado (admin do tenant
	// cliente em BPaaS; o proprio usuario logado em SaaS).
	ActedAsUserID uuid.UUID

	// ActedByUserID e quem REALMENTE clicou (operador IMOBO em BPaaS; o proprio
	// usuario em SaaS). Preenchido a partir do `sub` do JWT autenticado.
	ActedByUserID uuid.UUID

	// HomeTenantID e o tenant "home" do usuario (tenant root para operador
	// IMOBO; tenant proprio para admin SaaS).
	HomeTenantID uuid.UUID

	// Cargo e o papel canonico do usuario (claim `cargo` do JWT). Valores
	// possiveis: MASTER_IMOBO / ADMIN_IMOBILIARIA / GESTOR / OPERADOR /
	// VISUALIZADOR. Vazio quando o token for de servico.
	//
	// Usado por handlers que precisam autorizar baseado no cargo (e.g.
	// /api/v1/usuarios admin-only). Para RLS, continuar usando VisibleTenantIDs.
	Cargo string

	// Permissions sao as permissoes/papeis efetivos do usuario no tenant
	// operado. Usado para autorizacao de UI/endpoint, nao para RLS (RLS usa
	// VisibleTenantIDs).
	Permissions []string

	// DataScope define quais DADOS o usuario pode visualizar no tenant operado.
	// Valores: "all" (diretor/admin), "assigned" (gerente de obra), "own" (membro).
	// Vazio = nenhum role ativo → queries de scope devem tratar como "own".
	DataScope string

	// VisibleTenantIDs e a lista completa de tenants que o usuario pode
	// visualizar nesta sessao (hierarquia BPaaS — master IMOBO ve filhos).
	// Usado para popular `app.tenant_ids_visiveis` no Postgres.
	// Para SaaS puro: lista unitaria com [ActedAsTenantID].
	// Para operador IMOBO atuando como tenant pai: [ActedAsTenantID + descendentes].
	VisibleTenantIDs []uuid.UUID

	// IsMasterImobo indica se o usuario tem papel OPERADOR_IMOBO. Operadores
	// IMOBO sao os unicos com acesso cross-tenant (hierarquia BPaaS).
	IsMasterImobo bool
}

// Inject coloca o TenantContext no context.Context, retornando um novo ctx
// derivado. O caller deve passar adiante o contexto retornado para que
// handlers downstream possam recuperar via From/MustFrom.
//
// Convencionalmente chamado pelo `HTTPMiddleware` apos validar o JWT e
// computar a hierarquia de tenants visiveis.
func Inject(ctx context.Context, tc TenantContext) context.Context {
	return context.WithValue(ctx, tenantCtxKey, tc)
}

// From extrai o TenantContext do context.Context. Retorna o TenantContext e
// `true` se presente; zero-value e `false` se ausente.
//
// Use em handlers que querem checar a presenca antes de usar (ex.: middlewares
// opcionais, healthchecks). Para handlers que JA passaram pelo middleware
// `HTTPMiddleware`, prefira `MustFrom` — o panic e mais explicito do que um
// zero-value silencioso.
func From(ctx context.Context) (TenantContext, bool) {
	if ctx == nil {
		return TenantContext{}, false
	}
	tc, ok := ctx.Value(tenantCtxKey).(TenantContext)
	return tc, ok
}

// MustFrom extrai o TenantContext do context.Context ou panica.
//
// Use APENAS em handlers que JA passaram pelo middleware `HTTPMiddleware` —
// o panic indica bug de programacao (rota nao protegida pelo middleware), nao
// erro de runtime esperado.
func MustFrom(ctx context.Context) TenantContext {
	tc, ok := From(ctx)
	if !ok {
		panic("tenantctx: TenantContext ausente em context.Context (esqueceu HTTPMiddleware?)")
	}
	return tc
}

// VisibleTenantIDsSQL formata o slice de UUIDs no formato esperado pelo
// Postgres `SET LOCAL app.tenant_ids_visiveis = '<aqui>'`.
//
// Formato: '{uuid1,uuid2,uuid3}' (array literal Postgres).
//
// Se o slice estiver vazio, retorna '{}' (array vazio) — RLS bloqueia tudo,
// que e o comportamento seguro por padrao.
//
// Os UUIDs sao formatados como strings canonicas (sem aspas individuais — o
// driver pgx ja escapa o conteudo do array literal).
func (tc TenantContext) VisibleTenantIDsSQL() string {
	if len(tc.VisibleTenantIDs) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(tc.VisibleTenantIDs))
	for _, id := range tc.VisibleTenantIDs {
		parts = append(parts, id.String())
	}
	return "{" + strings.Join(parts, ",") + "}"
}
