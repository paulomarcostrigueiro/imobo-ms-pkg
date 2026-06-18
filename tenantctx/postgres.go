// Copyright 2026 imobo. Licenca: privada.

package tenantctx

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Erros canonicos retornados pelos helpers Postgres.
var (
	// ErrSemTenantContext indica que `WithTenantContext` foi chamado com um
	// ctx que NAO tem TenantContext injetado.
	//
	// Sintoma comum: job assincrono ou consumer RabbitMQ que esqueceu de
	// chamar `Inject` antes de abrir transacao. ADR-002 R1.
	ErrSemTenantContext = errors.New("tenantctx: ctx sem TenantContext (esqueceu Inject ou middleware?)")

	// ErrTenantContextInvalido indica que o TenantContext esta presente mas
	// invalido (ex.: VisibleTenantIDs vazio, ActedByUserID zero).
	ErrTenantContextInvalido = errors.New("tenantctx: TenantContext invalido")
)

// pgxBeginner e a interface minima necessaria para abrir transacoes. Tanto
// *pgxpool.Pool quanto *pgx.Conn satisfazem esta interface.
//
// Definida para permitir teste unitario mockando comportamento sem precisar de
// pool real, mas aceitar `*pgxpool.Pool` como argumento concreto na assinatura
// publica (ergonomia vs flexibilidade).
type pgxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// WithTenantContext executa `fn` dentro de uma transacao Postgres com os
// valores `SET LOCAL` aplicados conforme o TenantContext do ctx.
//
// Sequencia exata:
//  1. Extrai TenantContext do ctx — falha com ErrSemTenantContext se ausente.
//  2. Valida o TenantContext (VisibleTenantIDs nao-vazio, IDs nao-zero).
//  3. Abre transacao via pool.Begin().
//  4. Executa `SET LOCAL app.tenant_ids_visiveis = '{<UUIDs>}'`.
//  5. Executa `SET LOCAL app.acted_as_user_id = '<UUID>'`.
//  6. Executa `SET LOCAL app.acted_by_user_id = '<UUID>'`.
//  7. Roda fn(tx).
//  8. Commit em sucesso, rollback em erro.
//
// IMPORTANTE: NUNCA faca queries direto no `pool` sem passar por
// `WithTenantContext`. RLS Postgres bloqueia tudo por padrao quando
// `app.tenant_ids_visiveis` nao esta setado — sintoma e queries retornando
// zero rows silenciosamente. Ver ADR-002 R1.
func WithTenantContext(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	if pool == nil {
		return errors.New("tenantctx: pool e nil")
	}
	return withTenantContextOn(ctx, pool, fn)
}

// withTenantContextOn e a versao testavel (aceita interface) chamada por
// WithTenantContext. Permite teste unitario substituindo o pool por mock.
func withTenantContextOn(ctx context.Context, beginner pgxBeginner, fn func(tx pgx.Tx) error) error {
	tc, ok := From(ctx)
	if !ok {
		return ErrSemTenantContext
	}

	if err := validateTenantContext(tc); err != nil {
		return fmt.Errorf("%w: %v", ErrTenantContextInvalido, err)
	}

	tx, err := beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tenantctx: begin tx: %w", err)
	}

	// rollbackOnly garante que se a fn ou o commit falharem, fazemos rollback.
	// Idempotente: rollback apos commit e no-op no pgx.
	commitDone := false
	defer func() {
		if !commitDone {
			// Rollback usa contexto background para nao falhar se ctx ja foi
			// cancelado (timeout, deadline). E uma tentativa best-effort de
			// liberar a conexao.
			_ = tx.Rollback(context.Background())
		}
	}()

	if err := applySetLocals(ctx, tx, tc); err != nil {
		return fmt.Errorf("tenantctx: aplicar SET LOCAL: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("tenantctx: commit: %w", err)
	}
	commitDone = true
	return nil
}

// applySetLocals executa os SET LOCAL canonicos do ADR-002 + ADR-017 (Sprint 16 B).
//
// Sprint 16 B (ADR-017 audit_log + fix RLS catalogo_pendente):
//   - app.tenant_id = '<ActedAsTenantID>' (singular, p/ trigger audit.log_changes()).
//   - app.is_master_imobo = 'true'/'false' (p/ politica RLS catalogo_pendente
//     0154 — master ve orfaos imobiliaria_id NULL).
func applySetLocals(ctx context.Context, tx pgx.Tx, tc TenantContext) error {
	// SECURITY (revisao 2026-06-18): usar set_config(name, value, is_local=true)
	// com value PARAMETRIZADO ($1) em vez de fmt.Sprintf("SET LOCAL ... '%s'").
	// Postgres nao aceita placeholder em SET LOCAL, mas set_config aceita — isso
	// elimina qualquer interpolacao de string no contexto que define a RLS
	// (defesa em profundidade contra SQL injection no isolamento por tenant).
	masterFlag := "false"
	if tc.IsMasterImobo {
		masterFlag = "true"
	}
	settings := []struct{ name, value string }{
		{"app.tenant_ids_visiveis", tc.VisibleTenantIDsSQL()},
		{"app.acted_as_user_id", tc.ActedAsUserID.String()},
		{"app.acted_by_user_id", tc.ActedByUserID.String()},
		// app.tenant_id (singular) p/ trigger audit.log_changes() (ADR-017).
		{"app.tenant_id", tc.ActedAsTenantID.String()},
		// app.is_master_imobo p/ politicas RLS que diferenciam master (0154).
		{"app.is_master_imobo", masterFlag},
	}
	for _, s := range settings {
		if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", s.name, s.value); err != nil {
			return fmt.Errorf("set_config %s: %w", s.name, err)
		}
	}
	return nil
}

// validateTenantContext checa invariantes do TenantContext antes de aplicar
// SET LOCAL. Falhar aqui e melhor do que aplicar configuracao parcial.
func validateTenantContext(tc TenantContext) error {
	if tc.ActedAsTenantID == uuid.Nil {
		return errors.New("ActedAsTenantID e zero")
	}
	if tc.ActedByUserID == uuid.Nil {
		return errors.New("ActedByUserID e zero")
	}
	if tc.ActedAsUserID == uuid.Nil {
		return errors.New("ActedAsUserID e zero")
	}
	if len(tc.VisibleTenantIDs) == 0 {
		return errors.New("VisibleTenantIDs vazio (RLS bloquearia tudo)")
	}
	for i, id := range tc.VisibleTenantIDs {
		if id == uuid.Nil {
			return fmt.Errorf("VisibleTenantIDs[%d] e zero", i)
		}
	}
	return nil
}
