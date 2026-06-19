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
	"os"
	"strings"

	"github.com/google/uuid"
)

// EnvMasterRootTenantID e o nome da variavel de ambiente que define o tenant_id
// reservado para a IMOBO (operadora da plataforma). DEVE conter um UUID REAL,
// nao-nil. Quando ausente, vazio, invalido ou nil-uuid, o sistema NUNCA promove
// usuarios a SUPER-MASTER IMOBO (fail-closed).
//
// SECURITY (R1, 2026-06-19): substitui a antiga constante MasterRootTenantUUID =
// uuid.Nil (00000000-...). Usar o zero-value como sentinela era um risco: o valor
// e adivinhavel e qualquer token forjado com home_tenant = nil-uuid era promovido
// a master. Agora o sentinela e um UUID secreto/operacional configurado por env,
// e a promocao exige defesa dupla (home == master-root E cargo == MASTER_IMOBO).
const EnvMasterRootTenantID = "MASTER_ROOT_TENANT_ID"

// CargoMasterImobo e o valor canonico do claim `cargo` exigido (em conjunto com
// o home tenant == master-root) para promover IsMasterImobo. Defesa dupla: o
// cargo vem do JWT ASSINADO pela plataforma, logo nao e forjavel sem a chave.
const CargoMasterImobo = "MASTER_IMOBO"

// masterRootTenantID le e parseia EnvMasterRootTenantID. Retorna (id, true)
// SOMENTE quando a env contiver um UUID valido e NAO-nil. Em qualquer outra
// situacao (ausente, vazia, invalida, nil-uuid) retorna (uuid.Nil, false) —
// comportamento fail-closed: sem master-root configurado, ninguem vira master.
func masterRootTenantID() (uuid.UUID, bool) {
	raw := strings.TrimSpace(os.Getenv(EnvMasterRootTenantID))
	if raw == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	if id == uuid.Nil {
		// nil-uuid NUNCA e aceito como sentinela de master-root.
		return uuid.Nil, false
	}
	return id, true
}

// IsMasterRootTenant retorna true se o tenant id passado for EXATAMENTE o
// master-root configurado por env (EnvMasterRootTenantID) e este for um UUID
// real nao-nil. Se a env nao estiver configurada (ou for invalida/nil), retorna
// sempre false — nenhum tenant e considerado master-root (fail-closed).
//
// ATENCAO: ser master-root e CONDICAO NECESSARIA mas NAO suficiente para virar
// master. A promocao a IsMasterImobo exige tambem cargo == MASTER_IMOBO (ver
// buildTenantContext / shouldPromoteMaster). Use esta funcao apenas para checar
// o tenant; nao a use sozinha como gate de permissao.
func IsMasterRootTenant(tenantID uuid.UUID) bool {
	root, ok := masterRootTenantID()
	if !ok {
		return false
	}
	return tenantID == root
}

// shouldPromoteMaster aplica a regra de defesa dupla do R1: promove a
// IsMasterImobo SOMENTE quando o home tenant coincide com o master-root
// configurado E o cargo (claim assinado) for exatamente MASTER_IMOBO.
func shouldPromoteMaster(homeTenantID uuid.UUID, cargo string) bool {
	return IsMasterRootTenant(homeTenantID) && cargo == CargoMasterImobo
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

	// Servicos e a lista de servicos contratados/ativos do tenant OPERADO
	// (entitlement — LEI-MS #40). Vem do claim `srv` do JWT de sessao, assinado
	// pela plataforma. Reflete sempre o tenant ACTED-AS: quando o master opera
	// como tenant Y, `srv` = servicos de Y. Vazio quando o claim estiver ausente.
	//
	// NAO ha bypass especial pra master: o claim ja reflete o tenant operado.
	// Front so LE este campo; quem VALIDA e o servidor (RequireService).
	Servicos []string
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

// HasServico retorna true se o servico `nome` estiver na lista de servicos
// contratados/ativos do TenantContext (entitlement — LEI-MS #40). A comparacao
// e exata (case-sensitive). Lista vazia => sempre false (fail-closed).
func (tc TenantContext) HasServico(nome string) bool {
	for _, s := range tc.Servicos {
		if s == nome {
			return true
		}
	}
	return false
}

// HasService verifica se o tenant operado no ctx tem o servico `nome`
// contratado/ativo (entitlement — LEI-MS #40). Retorna false se nao houver
// TenantContext no ctx, ou se o servico nao estiver na lista (fail-closed).
//
// Use para gates programaticas dentro de handlers. Para proteger rotas inteiras,
// prefira o middleware RequireService.
func HasService(ctx context.Context, nome string) bool {
	tc, ok := From(ctx)
	if !ok {
		return false
	}
	return tc.HasServico(nome)
}
