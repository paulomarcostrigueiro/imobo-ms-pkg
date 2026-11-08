// Copyright 2026 imobo. Licenca: privada.

//go:build integration

// Postgres real (testcontainers). Execucao:
//
//	go test -tags integration -race ./iauso/
//
// Reproduz os papeis do deploy/init/00-init.sh: owner e app, os dois
// NOSUPERUSER NOBYPASSRLS, com o ALTER DEFAULT PRIVILEGES que os servicos usam.
package iauso

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/paulomarcostrigueiro/imobo-ms-pkg/claudeia"
)

func conectar(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	p, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return p.Ping(ctx) == nil }, 30*time.Second, 200*time.Millisecond)
	t.Cleanup(p.Close)
	return p
}

func TestPostgresTabelaRLSEGasto(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("imobo_test"), postgres.WithUsername("imobo_admin"), postgres.WithPassword("imobo"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	adminDSN, err := c.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	admin := conectar(t, ctx, adminDSN)
	for _, s := range []string{
		`CREATE ROLE svc_owner LOGIN PASSWORD 'p' NOSUPERUSER NOBYPASSRLS CREATEROLE`,
		`CREATE ROLE svc_app LOGIN PASSWORD 'p' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE`,
		`CREATE SCHEMA svc AUTHORIZATION svc_owner`,
		`GRANT USAGE ON SCHEMA svc TO svc_app`,
	} {
		_, err := admin.Exec(ctx, s)
		require.NoError(t, err, s)
	}
	owner := conectar(t, ctx, strings.Replace(adminDSN, "imobo_admin:imobo", "svc_owner:p", 1))
	_, err = owner.Exec(ctx, `ALTER DEFAULT PRIVILEGES IN SCHEMA svc GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO svc_app`)
	require.NoError(t, err)
	for range 2 { // a migration precisa ser idempotente
		_, err = owner.Exec(ctx, SQLTabela("svc", "svc_app"))
		require.NoError(t, err)
	}

	app := conectar(t, ctx, strings.Replace(adminDSN, "imobo_admin:imobo", "svc_app:p", 1))
	g, err := NovoPostgresGravador(app, "svc")
	require.NoError(t, err)

	t1, t2 := uuid.New(), uuid.New()
	agora := time.Now()
	rs := []Registro{
		{ID: uuid.New(), CriadoEm: agora, TenantID: &t1, Servico: "fiscal", Funcionalidade: "x", Organizacao: "A", Modo: ModoBiblioteca, Modelo: "m", Custo: 1_000, PrecoConhecido: true, Status: StatusOK, HTTPStatus: 200},
		{ID: uuid.New(), CriadoEm: agora, TenantID: &t2, Servico: "fiscal", Funcionalidade: "x", Organizacao: "A", Modo: ModoPlanoB, Custo: 2_000, PrecoConhecido: true, Status: StatusOK, RequestID: "req", ConversaID: "c"},
		{ID: uuid.New(), CriadoEm: agora, Servico: "fiscal", Funcionalidade: "x", Organizacao: "B", Modo: ModoMicrosservico, Custo: 5_000, PrecoConhecido: false, Status: StatusOK},
		{ID: uuid.New(), CriadoEm: agora.Add(-40 * 24 * time.Hour), TenantID: &t1, Servico: "fiscal", Funcionalidade: "x", Organizacao: "A", Modo: ModoBiblioteca, Custo: 9_999, Status: StatusOK},
	}
	require.NoError(t, g.Gravar(ctx, rs))
	require.NoError(t, g.Gravar(ctx, rs[:1]), "repetir nao duplica nem falha")

	gasto, err := g.GastoDesde(ctx, "A", agora.Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, claudeia.MicroDolar(3_000), gasto, "soma os dois tenants, fora do ciclo nao entra")

	var n int
	require.NoError(t, app.QueryRow(ctx, `SELECT count(*) FROM svc.ia_uso`).Scan(&n))
	assert.Zero(t, n, "sem tenant no contexto a RLS nao mostra nada")

	tx, err := app.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('app.tenant_ids_visiveis', $1, true)`, "{"+t1.String()+"}")
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM svc.ia_uso`).Scan(&n))
	assert.Equal(t, 2, n, "o tenant ve so as proprias linhas")
	var g2 int64
	require.NoError(t, tx.QueryRow(ctx, `SELECT svc.ia_gasto_desde('A', $1)`, agora.Add(-time.Hour)).Scan(&g2))
	assert.EqualValues(t, 3_000, g2)
	var master string
	require.NoError(t, tx.QueryRow(ctx, `SELECT COALESCE(current_setting('app.is_master_imobo', true), '')`).Scan(&master))
	assert.NotEqual(t, "true", master, "a visao master da funcao nao vaza para a transacao")
	require.NoError(t, tx.Rollback(ctx))

	_, err = app.Exec(ctx, `UPDATE svc.ia_uso SET custo_micro_usd = 0`)
	assert.ErrorContains(t, err, "permission denied", "historico nao se altera")
	_, err = app.Exec(ctx, `DELETE FROM svc.ia_uso`)
	assert.ErrorContains(t, err, "permission denied", "historico nao se apaga")
	_, err = app.Exec(ctx, `INSERT INTO svc.ia_uso (servico, funcionalidade, organizacao, modo, status) VALUES ('x','x','A','biblioteca','ok')`)
	assert.ErrorContains(t, err, "permission denied", "so grava pela funcao")

	var total int
	require.NoError(t, admin.QueryRow(ctx, `SELECT count(*) FROM svc.ia_uso`).Scan(&total))
	assert.Equal(t, 4, total, "o reenvio nao duplicou")

	// Um registro ruim no meio do lote (status fora do CHECK): so ele e
	// recusado; os outros dois entram.
	lote := []Registro{
		{ID: uuid.New(), CriadoEm: agora, Servico: "s", Funcionalidade: "f", Organizacao: "A", Modo: ModoBiblioteca, Status: StatusOK},
		{ID: uuid.New(), CriadoEm: agora, Servico: "s", Funcionalidade: "f", Organizacao: "A", Modo: ModoBiblioteca, Status: "invalido"},
		{ID: uuid.New(), CriadoEm: agora, Servico: strings.Repeat("x", 300), Funcionalidade: "f", Organizacao: "A", Modo: ModoBiblioteca, Status: StatusOK},
	}
	err = g.Gravar(ctx, lote)
	var inv *ErrRegistrosInvalidos
	require.ErrorAs(t, err, &inv)
	assert.Equal(t, []int{1}, inv.Indices, "o texto longo e cortado; so o status invalido e recusado")
	require.NoError(t, admin.QueryRow(ctx, `SELECT count(*) FROM svc.ia_uso`).Scan(&total))
	assert.Equal(t, 6, total)
}
