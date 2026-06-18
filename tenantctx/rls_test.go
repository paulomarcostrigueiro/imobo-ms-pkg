// Copyright 2026 imobo. Licenca: privada.

//go:build integration

// Testes de integracao do RLS com Postgres real via testcontainers-go.
// Execucao: go test -tags integration -race -cover ./pkg/tenantctx/...
package tenantctx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupPostgres sobe um container Postgres 16 e retorna o pool conectado +
// funcao de teardown. Aplica o schema canonico do ADR-002 (tenant + RLS).
//
// IMPORTANTE: cria um role nao-superuser `imobo_app` para a aplicacao usar.
// Superusers BYPASS RLS por default, entao se o pool conectasse como
// superuser, FORCE ROW LEVEL SECURITY nao seria respeitado.
func setupPostgres(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("imobo_test"),
		postgres.WithUsername("imobo_admin"),
		postgres.WithPassword("imobo"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)

	adminDSN, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Pool admin (superuser) — usado APENAS para aplicar DDL e criar role app.
	adminPool, err := pgxpool.New(ctx, adminDSN)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return adminPool.Ping(ctx) == nil
	}, 30*time.Second, 200*time.Millisecond)

	applySchema(t, adminPool)
	createAppRole(t, adminPool)
	adminPool.Close()

	// Pool app — conecta como `imobo_app` (NOT superuser, NOT BYPASSRLS).
	appDSN := strings.Replace(adminDSN, "imobo_admin:imobo", "imobo_app:imobo", 1)
	pool, err := pgxpool.New(ctx, appDSN)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return pool.Ping(ctx) == nil
	}, 30*time.Second, 200*time.Millisecond)

	teardown := func() {
		pool.Close()
		_ = container.Terminate(context.Background())
	}
	return pool, teardown
}

// createAppRole cria um role nao-superuser para a aplicacao com permissoes
// CRUD nas tabelas de teste.
func createAppRole(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`CREATE ROLE imobo_app LOGIN PASSWORD 'imobo' NOSUPERUSER NOBYPASSRLS`,
		`GRANT CONNECT ON DATABASE imobo_test TO imobo_app`,
		`GRANT USAGE ON SCHEMA public TO imobo_app`,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO imobo_app`,
	}
	for _, s := range stmts {
		_, err := pool.Exec(ctx, s)
		require.NoError(t, err, "criar role app: %s", s)
	}
}

// applySchema aplica DDL ADR-002: tabela tenant + tabela lancamento com RLS.
func applySchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	stmts := []string{
		`CREATE EXTENSION IF NOT EXISTS pgcrypto`,

		`CREATE TABLE tenant (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            parent_tenant_id UUID REFERENCES tenant(id),
            nome TEXT NOT NULL,
            modelo_servico TEXT NOT NULL CHECK (modelo_servico IN ('SAAS','BPAAS','ROOT'))
        )`,

		`CREATE TABLE lancamento (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            tenant_id UUID NOT NULL REFERENCES tenant(id),
            valor_centavos BIGINT NOT NULL,
            descricao TEXT,
            criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )`,

		`CREATE TABLE acao_log (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            tenant_id UUID NOT NULL,
            acted_as_user_id UUID NOT NULL,
            acted_by_user_id UUID NOT NULL,
            acao TEXT NOT NULL,
            criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )`,

		`ALTER TABLE lancamento ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE lancamento FORCE ROW LEVEL SECURITY`,
		`CREATE POLICY tenant_isolation ON lancamento
            USING (tenant_id = ANY(current_setting('app.tenant_ids_visiveis', true)::uuid[]))`,

		`ALTER TABLE acao_log ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE acao_log FORCE ROW LEVEL SECURITY`,
		`CREATE POLICY tenant_isolation ON acao_log
            USING (tenant_id = ANY(current_setting('app.tenant_ids_visiveis', true)::uuid[]))`,
	}

	for _, s := range stmts {
		_, err := pool.Exec(ctx, s)
		require.NoError(t, err, "aplicar DDL: %s", s)
	}
}

// criarTenant insere um tenant e retorna o ID.
func criarTenant(t *testing.T, pool *pgxpool.Pool, nome, modelo string, parent *uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO tenant (id, nome, modelo_servico, parent_tenant_id) VALUES ($1,$2,$3,$4)`,
		id, nome, modelo, parent)
	require.NoError(t, err)
	return id
}

// inserirLancamento insere lancamento via tenantctx.WithTenantContext (caminho oficial).
func inserirLancamento(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, userID uuid.UUID, valor int64) uuid.UUID {
	t.Helper()

	tc := TenantContext{
		ActedAsTenantID:  tenantID,
		ActedAsUserID:    userID,
		ActedByUserID:    userID,
		HomeTenantID:     tenantID,
		VisibleTenantIDs: []uuid.UUID{tenantID},
	}
	ctx := Inject(context.Background(), tc)

	var id uuid.UUID
	err := WithTenantContext(ctx, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO lancamento (tenant_id, valor_centavos, descricao) VALUES ($1,$2,$3) RETURNING id`,
			tenantID, valor, "teste",
		).Scan(&id)
	})
	require.NoError(t, err)
	return id
}

// TestRLS_TenantANaoVeTenantB verifica isolamento basico.
func TestRLS_TenantANaoVeTenantB(t *testing.T) {
	pool, teardown := setupPostgres(t)
	defer teardown()

	tenantA := criarTenant(t, pool, "Imobiliaria A", "SAAS", nil)
	tenantB := criarTenant(t, pool, "Imobiliaria B", "SAAS", nil)
	userA := uuid.New()
	userB := uuid.New()

	idA := inserirLancamento(t, pool, tenantA, userA, 1000)
	idB := inserirLancamento(t, pool, tenantB, userB, 2000)

	// Conectado como tenant A, tenta listar TUDO (sem WHERE).
	tcA := TenantContext{
		ActedAsTenantID:  tenantA,
		ActedAsUserID:    userA,
		ActedByUserID:    userA,
		HomeTenantID:     tenantA,
		VisibleTenantIDs: []uuid.UUID{tenantA},
	}
	ctxA := Inject(context.Background(), tcA)

	var ids []uuid.UUID
	err := WithTenantContext(ctxA, pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctxA, `SELECT id FROM lancamento`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	require.NoError(t, err)

	assert.Contains(t, ids, idA)
	assert.NotContains(t, ids, idB)
	assert.Len(t, ids, 1)
}

// TestRLS_MasterIMOBO_VeApenasActingAs verifica que master IMOBO atuando em
// tenant filho ve so o filho (lista visible = [filho]).
func TestRLS_MasterIMOBO_VeApenasActingAs(t *testing.T) {
	pool, teardown := setupPostgres(t)
	defer teardown()

	root := criarTenant(t, pool, "imobo (root)", "ROOT", nil)
	pinheiros := criarTenant(t, pool, "Pinheiros", "BPAAS", &root)
	maria := uuid.New()         // operador IMOBO
	pinheirosUser := uuid.New() // user admin de Pinheiros

	idPinheiros := inserirLancamento(t, pool, pinheiros, pinheirosUser, 5000)

	// Maria atuando em nome de Pinheiros — visible = [pinheiros] apenas.
	tc := TenantContext{
		ActedAsTenantID:  pinheiros,
		ActedAsUserID:    pinheirosUser,
		ActedByUserID:    maria,
		HomeTenantID:     root,
		VisibleTenantIDs: []uuid.UUID{pinheiros},
		IsMasterImobo:    true,
	}
	ctx := Inject(context.Background(), tc)

	// Insere acao_log via WithTenantContext: deve registrar acted_by != acted_as.
	var logActedAs, logActedBy uuid.UUID
	err := WithTenantContext(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO acao_log (tenant_id, acted_as_user_id, acted_by_user_id, acao)
             VALUES ($1, current_setting('app.acted_as_user_id')::uuid, current_setting('app.acted_by_user_id')::uuid, 'PAGAR_BOLETO')`,
			pinheiros)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			`SELECT acted_as_user_id, acted_by_user_id FROM acao_log WHERE tenant_id = $1`,
			pinheiros,
		).Scan(&logActedAs, &logActedBy)
	})
	require.NoError(t, err)

	assert.Equal(t, pinheirosUser, logActedAs)
	assert.Equal(t, maria, logActedBy)
	assert.NotEqual(t, logActedAs, logActedBy)

	// Confirma que ve o lancamento.
	var visible []uuid.UUID
	err = WithTenantContext(ctx, pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM lancamento`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			_ = rows.Scan(&id)
			visible = append(visible, id)
		}
		return rows.Err()
	})
	require.NoError(t, err)
	assert.Contains(t, visible, idPinheiros)
}

// TestRLS_BypassDirectoSemSetLocal_RetornaZeroRows verifica que queries fora
// de WithTenantContext (no pool, sem SET LOCAL) sao bloqueadas pelo RLS.
//
// O comportamento esperado e UM dos dois (ambos significam "bloqueado"):
//  1. Query falha com erro (current_setting vazio nao casta pra uuid[]).
//  2. Query retorna 0 rows (NULL contra ANY e false).
//
// Ambos sao seguros — vazamento exigiria "linhas de outros tenants aparecerem".
func TestRLS_BypassDirectoSemSetLocal_RetornaZeroRows(t *testing.T) {
	pool, teardown := setupPostgres(t)
	defer teardown()

	tenantA := criarTenant(t, pool, "A", "SAAS", nil)
	tenantB := criarTenant(t, pool, "B", "SAAS", nil)
	_ = inserirLancamento(t, pool, tenantA, uuid.New(), 100)
	_ = inserirLancamento(t, pool, tenantB, uuid.New(), 200)

	// Pool direto, sem WithTenantContext: RLS bloqueia tudo.
	rows, err := pool.Query(context.Background(), `SELECT id FROM lancamento`)
	if err != nil {
		// Caso 1: erro de cast — bloqueio implicito. OK.
		assert.Contains(t, err.Error(), "malformed array literal", "erro deveria vir do cast falho")
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}
	// Caso 2: zero rows.
	assert.Equal(t, 0, count, "RLS deveria bloquear tudo sem SET LOCAL")
}

// TestRLS_SemTenantContext_RetornaErro verifica que WithTenantContext retorna
// ErrSemTenantContext quando o ctx nao tem TenantContext injetado.
func TestRLS_SemTenantContext_RetornaErro(t *testing.T) {
	pool, teardown := setupPostgres(t)
	defer teardown()

	// ctx sem Inject.
	called := false
	err := WithTenantContext(context.Background(), pool, func(tx pgx.Tx) error {
		called = true
		return nil
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSemTenantContext))
	assert.False(t, called, "fn nao deveria ser chamada")
}

// TestRLS_HierarquiaBPaaS verifica que master IMOBO com lista [pai+filho] ve
// dados de ambos; e que filho com lista [filho] NAO ve dados do pai.
func TestRLS_HierarquiaBPaaS(t *testing.T) {
	pool, teardown := setupPostgres(t)
	defer teardown()

	root := criarTenant(t, pool, "imobo", "ROOT", nil)
	filho1 := criarTenant(t, pool, "Filho1", "BPAAS", &root)
	filho2 := criarTenant(t, pool, "Filho2", "BPAAS", &root)

	idRoot := inserirLancamento(t, pool, root, uuid.New(), 1)
	idFilho1 := inserirLancamento(t, pool, filho1, uuid.New(), 2)
	idFilho2 := inserirLancamento(t, pool, filho2, uuid.New(), 3)

	// Master IMOBO com visibilidade ampla: ve os tres.
	tcMaster := TenantContext{
		ActedAsTenantID:  root,
		ActedAsUserID:    uuid.New(),
		ActedByUserID:    uuid.New(),
		HomeTenantID:     root,
		VisibleTenantIDs: []uuid.UUID{root, filho1, filho2},
		IsMasterImobo:    true,
	}
	ctxMaster := Inject(context.Background(), tcMaster)

	var seenMaster []uuid.UUID
	err := WithTenantContext(ctxMaster, pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctxMaster, `SELECT id FROM lancamento ORDER BY valor_centavos`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			_ = rows.Scan(&id)
			seenMaster = append(seenMaster, id)
		}
		return rows.Err()
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{idRoot, idFilho1, idFilho2}, seenMaster)

	// Filho1 com visibilidade so dele: nao ve root nem filho2.
	tcFilho := TenantContext{
		ActedAsTenantID:  filho1,
		ActedAsUserID:    uuid.New(),
		ActedByUserID:    uuid.New(),
		HomeTenantID:     filho1,
		VisibleTenantIDs: []uuid.UUID{filho1},
	}
	ctxFilho := Inject(context.Background(), tcFilho)

	var seenFilho []uuid.UUID
	err = WithTenantContext(ctxFilho, pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctxFilho, `SELECT id FROM lancamento`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			_ = rows.Scan(&id)
			seenFilho = append(seenFilho, id)
		}
		return rows.Err()
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{idFilho1}, seenFilho)
}

// TestRLS_TenantContextInvalido_NaoExecutaFn verifica que TenantContext com
// VisibleTenantIDs vazio falha sem executar fn.
func TestRLS_TenantContextInvalido_NaoExecutaFn(t *testing.T) {
	pool, teardown := setupPostgres(t)
	defer teardown()

	tc := TenantContext{
		ActedAsTenantID: uuid.New(),
		ActedAsUserID:   uuid.New(),
		ActedByUserID:   uuid.New(),
		HomeTenantID:    uuid.New(),
		// VisibleTenantIDs intencionalmente vazio
	}
	ctx := Inject(context.Background(), tc)

	called := false
	err := WithTenantContext(ctx, pool, func(tx pgx.Tx) error {
		called = true
		return nil
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTenantContextInvalido))
	assert.False(t, called)
}

// TestRLS_FnRetornaErro_Rollback verifica que erro retornado por fn dispara
// rollback e propaga o erro original.
func TestRLS_FnRetornaErro_Rollback(t *testing.T) {
	pool, teardown := setupPostgres(t)
	defer teardown()

	tenant := criarTenant(t, pool, "T", "SAAS", nil)
	tc := TenantContext{
		ActedAsTenantID:  tenant,
		ActedAsUserID:    uuid.New(),
		ActedByUserID:    uuid.New(),
		HomeTenantID:     tenant,
		VisibleTenantIDs: []uuid.UUID{tenant},
	}
	ctx := Inject(context.Background(), tc)

	bizErr := errors.New("erro de negocio")
	err := WithTenantContext(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO lancamento (tenant_id, valor_centavos) VALUES ($1, $2)`, tenant, 999)
		require.NoError(t, err)
		return bizErr
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, bizErr))

	// Verifica que o lancamento NAO foi commitado abrindo NOVA transacao via
	// WithTenantContext (que aplica os SET LOCAL corretamente).
	var count int
	err = WithTenantContext(ctx, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM lancamento WHERE valor_centavos = 999`).Scan(&count)
	})
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}
