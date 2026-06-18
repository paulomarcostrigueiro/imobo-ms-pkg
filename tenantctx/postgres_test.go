// Copyright 2026 imobo. Licenca: privada.

package tenantctx

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBeginner mocka pgxBeginner.
type fakeBeginner struct {
	tx     pgx.Tx
	begErr error
}

func (f *fakeBeginner) Begin(_ context.Context) (pgx.Tx, error) {
	if f.begErr != nil {
		return nil, f.begErr
	}
	return f.tx, nil
}

func TestWithTenantContext_NilPool(t *testing.T) {
	err := WithTenantContext(context.Background(), nil, func(tx pgx.Tx) error { return nil })
	require.Error(t, err)
}

func TestWithTenantContext_SemTenantContext_Erro(t *testing.T) {
	beginner := &fakeBeginner{}
	called := false
	err := withTenantContextOn(context.Background(), beginner, func(tx pgx.Tx) error {
		called = true
		return nil
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSemTenantContext))
	assert.False(t, called)
}

func TestWithTenantContext_TenantContextInvalido(t *testing.T) {
	cases := []struct {
		name string
		tc   TenantContext
	}{
		{
			name: "ActedAsTenantID zero",
			tc: TenantContext{
				ActedAsUserID:    uuid.New(),
				ActedByUserID:    uuid.New(),
				VisibleTenantIDs: []uuid.UUID{uuid.New()},
			},
		},
		{
			name: "ActedByUserID zero",
			tc: TenantContext{
				ActedAsTenantID:  uuid.New(),
				ActedAsUserID:    uuid.New(),
				VisibleTenantIDs: []uuid.UUID{uuid.New()},
			},
		},
		{
			name: "ActedAsUserID zero",
			tc: TenantContext{
				ActedAsTenantID:  uuid.New(),
				ActedByUserID:    uuid.New(),
				VisibleTenantIDs: []uuid.UUID{uuid.New()},
			},
		},
		{
			name: "VisibleTenantIDs vazio",
			tc: TenantContext{
				ActedAsTenantID: uuid.New(),
				ActedAsUserID:   uuid.New(),
				ActedByUserID:   uuid.New(),
			},
		},
		{
			name: "VisibleTenantIDs com zero",
			tc: TenantContext{
				ActedAsTenantID:  uuid.New(),
				ActedAsUserID:    uuid.New(),
				ActedByUserID:    uuid.New(),
				VisibleTenantIDs: []uuid.UUID{uuid.New(), uuid.Nil},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			beginner := &fakeBeginner{}
			ctx := Inject(context.Background(), c.tc)
			called := false
			err := withTenantContextOn(ctx, beginner, func(tx pgx.Tx) error {
				called = true
				return nil
			})
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrTenantContextInvalido))
			assert.False(t, called)
		})
	}
}

func TestWithTenantContext_BeginErro(t *testing.T) {
	tc := TenantContext{
		ActedAsTenantID:  uuid.New(),
		ActedAsUserID:    uuid.New(),
		ActedByUserID:    uuid.New(),
		VisibleTenantIDs: []uuid.UUID{uuid.New()},
	}
	ctx := Inject(context.Background(), tc)

	beginner := &fakeBeginner{begErr: errors.New("conexao recusada")}
	err := withTenantContextOn(ctx, beginner, func(tx pgx.Tx) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "begin tx")
}

func TestValidateTenantContext_Ok(t *testing.T) {
	tc := TenantContext{
		ActedAsTenantID:  uuid.New(),
		ActedAsUserID:    uuid.New(),
		ActedByUserID:    uuid.New(),
		VisibleTenantIDs: []uuid.UUID{uuid.New()},
	}
	require.NoError(t, validateTenantContext(tc))
}

// =============================================================================
// fakeTx + fakeBeginnerTx — mocks completos para testar branches de erro de
// applySetLocals, Commit e Rollback sem precisar de Postgres real.
// =============================================================================

// fakeTx implementa pgx.Tx em memoria. Apenas Exec, Commit e Rollback sao
// usados pelo codigo sob teste; os outros metodos panicam se invocados (red
// flag pra mudanca futura no codigo de producao).
type fakeTx struct {
	// execErrs retorna o erro associado ao N-esimo Exec (0 = primeiro
	// SET LOCAL = tenant_ids_visiveis, 1 = acted_as_user_id, 2 = acted_by_user_id).
	// Se index out-of-bounds, retorna nil.
	execErrs []error
	execN    int

	// execStatements registra os SQLs executados — util para asserts.
	execStatements []string
	// execArgs registra os args de cada Exec (set_config($1,$2,true) → [nome,valor]).
	execArgs [][]any

	commitErr error
	commitN   int

	rollbackErr error
	rollbackN   int
}

func (f *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.execStatements = append(f.execStatements, sql)
	f.execArgs = append(f.execArgs, args)
	idx := f.execN
	f.execN++
	if idx < len(f.execErrs) {
		if err := f.execErrs[idx]; err != nil {
			return pgconn.CommandTag{}, err
		}
	}
	return pgconn.NewCommandTag("SET"), nil
}

func (f *fakeTx) Commit(_ context.Context) error {
	f.commitN++
	return f.commitErr
}

func (f *fakeTx) Rollback(_ context.Context) error {
	f.rollbackN++
	return f.rollbackErr
}

// Metodos restantes da interface pgx.Tx — nao usados pelo codigo sob teste.
// Panicam se invocados, expondo regressoes que adicionem dependencia neles.

func (f *fakeTx) Begin(_ context.Context) (pgx.Tx, error) {
	panic("fakeTx.Begin nao deveria ser chamado")
}

func (f *fakeTx) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	panic("fakeTx.CopyFrom nao deveria ser chamado")
}

func (f *fakeTx) SendBatch(_ context.Context, _ *pgx.Batch) pgx.BatchResults {
	panic("fakeTx.SendBatch nao deveria ser chamado")
}

func (f *fakeTx) LargeObjects() pgx.LargeObjects {
	panic("fakeTx.LargeObjects nao deveria ser chamado")
}

func (f *fakeTx) Prepare(_ context.Context, _, _ string) (*pgconn.StatementDescription, error) {
	panic("fakeTx.Prepare nao deveria ser chamado")
}

func (f *fakeTx) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	panic("fakeTx.Query nao deveria ser chamado")
}

func (f *fakeTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	panic("fakeTx.QueryRow nao deveria ser chamado")
}

func (f *fakeTx) Conn() *pgx.Conn {
	panic("fakeTx.Conn nao deveria ser chamado")
}

// validTC retorna um TenantContext valido (todos os UUIDs nao-zero) para uso
// nos testes happy/error abaixo.
func validTC() TenantContext {
	return TenantContext{
		ActedAsTenantID:  uuid.New(),
		ActedAsUserID:    uuid.New(),
		ActedByUserID:    uuid.New(),
		VisibleTenantIDs: []uuid.UUID{uuid.New()},
	}
}

// TestWithTenantContext_HappyPath_FakeTx — cobre fluxo OK ate o Commit.
// Garante que applySetLocals roda os 3 SETs e que Commit/Rollback sao
// chamados na ordem esperada (commit=1, rollback=0 quando fn nao falha).
func TestWithTenantContext_HappyPath_FakeTx(t *testing.T) {
	t.Parallel()

	tc := validTC()
	ctx := Inject(context.Background(), tc)
	tx := &fakeTx{}
	beginner := &fakeBeginner{tx: tx}

	called := false
	err := withTenantContextOn(ctx, beginner, func(_ pgx.Tx) error {
		called = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, called)

	// Sprint 16 B (ADR-017 + 0154 fix): 5 SET LOCAL — adicionados
	// app.tenant_id (singular, p/ trigger audit) + app.is_master_imobo (p/ RLS).
	require.Len(t, tx.execStatements, 5, "esperava 5 set_config")
	// set_config($1,$2,true) — o nome do setting vai no 1º arg (anti-injection).
	assert.Contains(t, tx.execStatements[0], "set_config")
	assert.Equal(t, "app.tenant_ids_visiveis", tx.execArgs[0][0])
	assert.Equal(t, "app.acted_as_user_id", tx.execArgs[1][0])
	assert.Equal(t, "app.acted_by_user_id", tx.execArgs[2][0])
	assert.Equal(t, "app.tenant_id", tx.execArgs[3][0])
	assert.Equal(t, "app.is_master_imobo", tx.execArgs[4][0])
	assert.Equal(t, 1, tx.commitN)
	assert.Equal(t, 0, tx.rollbackN, "rollback NAO deve ocorrer apos commit OK")
}

// TestApplySetLocals_ErrorBranches — cada SET LOCAL falhando individualmente
// retorna erro com mensagem distinta indicando qual variavel falhou.
func TestApplySetLocals_ErrorBranches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		execErrs  []error
		wantInMsg string
	}{
		{
			name:      "tenant_ids_visiveis falha",
			execErrs:  []error{errors.New("boom-visiveis")},
			wantInMsg: "set_config app.tenant_ids_visiveis",
		},
		{
			name:      "acted_as_user_id falha",
			execErrs:  []error{nil, errors.New("boom-acted-as")},
			wantInMsg: "set_config app.acted_as_user_id",
		},
		{
			name:      "acted_by_user_id falha",
			execErrs:  []error{nil, nil, errors.New("boom-acted-by")},
			wantInMsg: "set_config app.acted_by_user_id",
		},
		{
			name:      "tenant_id falha (Sprint 16 B)",
			execErrs:  []error{nil, nil, nil, errors.New("boom-tenant-id")},
			wantInMsg: "set_config app.tenant_id",
		},
		{
			name:      "is_master_imobo falha (Sprint 16 B)",
			execErrs:  []error{nil, nil, nil, nil, errors.New("boom-master")},
			wantInMsg: "set_config app.is_master_imobo",
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			tc := validTC()
			ctx := Inject(context.Background(), tc)
			tx := &fakeTx{execErrs: c.execErrs}
			beginner := &fakeBeginner{tx: tx}

			fnCalled := false
			err := withTenantContextOn(ctx, beginner, func(_ pgx.Tx) error {
				fnCalled = true
				return nil
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantInMsg)
			assert.Contains(t, err.Error(), "aplicar SET LOCAL")
			assert.False(t, fnCalled, "fn nao deve ser chamada se SET LOCAL falhou")
			assert.Equal(t, 0, tx.commitN, "commit NUNCA em falha de SET LOCAL")
			assert.Equal(t, 1, tx.rollbackN, "rollback DEVE ocorrer em falha de SET LOCAL")
		})
	}
}

// TestApplySetLocals_DiretoOk — chama applySetLocals direto pra cobrir o
// caminho feliz da funcao (alvo de cobertura: postgres.go:114-128).
func TestApplySetLocals_DiretoOk(t *testing.T) {
	t.Parallel()

	tc := validTC()
	tx := &fakeTx{}
	err := applySetLocals(context.Background(), tx, tc)
	require.NoError(t, err)
	require.Len(t, tx.execStatements, 5,
		"5 set_config (anti-injection): tenant_ids_visiveis, acted_as, acted_by, tenant_id, is_master_imobo")
	assert.True(t, strings.HasPrefix(tx.execStatements[0], "SELECT set_config"))
	assert.Equal(t, "app.tenant_ids_visiveis", tx.execArgs[0][0])
	assert.Equal(t, "app.tenant_id", tx.execArgs[3][0])
	assert.Equal(t, "app.is_master_imobo", tx.execArgs[4][0])
}

// TestWithTenantContext_FnErro_RollbackChamado — fn retorna erro, tx.Rollback
// e chamado, erro da fn e propagado puro (nao wrapped).
func TestWithTenantContext_FnErro_RollbackChamado(t *testing.T) {
	t.Parallel()

	tc := validTC()
	ctx := Inject(context.Background(), tc)
	tx := &fakeTx{}
	beginner := &fakeBeginner{tx: tx}

	fnErr := errors.New("erro de negocio")
	err := withTenantContextOn(ctx, beginner, func(_ pgx.Tx) error {
		return fnErr
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, fnErr), "erro retornado deve ser o erro da fn")
	assert.Equal(t, 0, tx.commitN, "commit NUNCA em falha de fn")
	assert.Equal(t, 1, tx.rollbackN, "rollback DEVE ocorrer em falha de fn")
}

// TestWithTenantContext_FnErro_RollbackTambemFalha — caso patologico: fn falha
// E o rollback tambem falha. Ainda assim retornamos o erro da fn (rollback
// best-effort, erro silenciado de proposito por design).
func TestWithTenantContext_FnErro_RollbackTambemFalha(t *testing.T) {
	t.Parallel()

	tc := validTC()
	ctx := Inject(context.Background(), tc)
	tx := &fakeTx{rollbackErr: errors.New("rollback explodiu")}
	beginner := &fakeBeginner{tx: tx}

	fnErr := errors.New("fn explodiu primeiro")
	err := withTenantContextOn(ctx, beginner, func(_ pgx.Tx) error {
		return fnErr
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, fnErr))
	assert.Equal(t, 1, tx.rollbackN)
}

// TestWithTenantContext_CommitErro — fn OK mas Commit falha, erro vem
// wrapped com "commit:" e rollback ainda e tentado (defer best-effort).
func TestWithTenantContext_CommitErro(t *testing.T) {
	t.Parallel()

	tc := validTC()
	ctx := Inject(context.Background(), tc)
	commitErr := errors.New("connection broken")
	tx := &fakeTx{commitErr: commitErr}
	beginner := &fakeBeginner{tx: tx}

	err := withTenantContextOn(ctx, beginner, func(_ pgx.Tx) error {
		return nil
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, commitErr))
	assert.Contains(t, err.Error(), "commit")
	assert.Equal(t, 1, tx.commitN, "commit foi tentado")
	// commitDone NAO foi setado pra true (erro), entao defer roda Rollback.
	assert.Equal(t, 1, tx.rollbackN, "rollback best-effort tentado apos commit falhar")
}

// TestWithTenantContext_PoolNil_PublicAPI — cobre o ramo `pool == nil` da API
// publica WithTenantContext (postgres.go:57-59).
func TestWithTenantContext_PoolNil_PublicAPI(t *testing.T) {
	t.Parallel()

	err := WithTenantContext(context.Background(), nil, func(_ pgx.Tx) error {
		return nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pool e nil")
}
