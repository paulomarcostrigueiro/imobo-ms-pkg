// Copyright 2026 imobo. Licenca: privada.

package tenantctx

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectAndFrom_RoundTrip(t *testing.T) {
	tc := TenantContext{
		ActedAsTenantID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ActedAsUserID:    uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		ActedByUserID:    uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		HomeTenantID:     uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		Permissions:      []string{"ledger.read", "ledger.write"},
		VisibleTenantIDs: []uuid.UUID{uuid.MustParse("11111111-1111-1111-1111-111111111111")},
		IsMasterImobo:    false,
	}

	ctx := Inject(context.Background(), tc)

	got, ok := From(ctx)
	require.True(t, ok)
	assert.Equal(t, tc, got)
}

func TestFrom_AusenteRetornaFalse(t *testing.T) {
	got, ok := From(context.Background())
	assert.False(t, ok)
	assert.Equal(t, TenantContext{}, got)
}

func TestFrom_NilCtxRetornaFalse(t *testing.T) {
	//nolint:staticcheck // testando defensividade contra nil
	got, ok := From(nil)
	assert.False(t, ok)
	assert.Equal(t, TenantContext{}, got)
}

func TestMustFrom_PanicaSemContexto(t *testing.T) {
	assert.Panics(t, func() {
		_ = MustFrom(context.Background())
	})
}

func TestMustFrom_RetornaQuandoPresente(t *testing.T) {
	tc := TenantContext{ActedAsTenantID: uuid.New()}
	ctx := Inject(context.Background(), tc)
	got := MustFrom(ctx)
	assert.Equal(t, tc, got)
}

func TestVisibleTenantIDsSQL_Vazio(t *testing.T) {
	tc := TenantContext{}
	assert.Equal(t, "{}", tc.VisibleTenantIDsSQL())
}

func TestVisibleTenantIDsSQL_Unitario(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tc := TenantContext{VisibleTenantIDs: []uuid.UUID{id}}
	assert.Equal(t, "{11111111-1111-1111-1111-111111111111}", tc.VisibleTenantIDsSQL())
}

func TestVisibleTenantIDsSQL_Multiplos(t *testing.T) {
	id1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	id2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	id3 := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	tc := TenantContext{VisibleTenantIDs: []uuid.UUID{id1, id2, id3}}
	expected := "{11111111-1111-1111-1111-111111111111,22222222-2222-2222-2222-222222222222,33333333-3333-3333-3333-333333333333}"
	assert.Equal(t, expected, tc.VisibleTenantIDsSQL())
}

func TestVisibleTenantIDsSQL_FormatoSemAspas(t *testing.T) {
	// Garante que o formato gerado nao tem aspas internas — Postgres escapa
	// o array literal com as aspas externas, e UUIDs sao seguros (apenas hex+hifens).
	id := uuid.New()
	tc := TenantContext{VisibleTenantIDs: []uuid.UUID{id}}
	out := tc.VisibleTenantIDsSQL()
	assert.NotContains(t, out, "'")
	assert.NotContains(t, out, "\"")
	assert.Contains(t, out, id.String())
}

func TestVisibleTenantIDsSQL_FormatoBrackets(t *testing.T) {
	tc := TenantContext{VisibleTenantIDs: []uuid.UUID{uuid.New()}}
	out := tc.VisibleTenantIDsSQL()
	assert.True(t, len(out) >= 2)
	assert.Equal(t, byte('{'), out[0])
	assert.Equal(t, byte('}'), out[len(out)-1])
}
