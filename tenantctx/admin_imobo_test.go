// Copyright 2026 imobo. Licenca: privada.

package tenantctx

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// LEI-MS #30, exceção 2: ADMIN_IMOBO só vale com cargo assinado, home no
// master-root e sem acting-as. Env ausente, inválida ou nil-uuid nega.
func TestEhAdminImobo_Tabela(t *testing.T) {
	root := uuid.MustParse(masterRoot)
	loja := uuid.New()

	casos := []struct {
		nome     string
		env      string
		cargo    string
		home     uuid.UUID
		actingAs uuid.UUID
		master   bool
		admin    bool
		carteira bool
	}{
		{"admin imobo na raiz", masterRoot, CargoAdminImobo, root, root, false, true, true},
		{"cargo certo com home errado", masterRoot, CargoAdminImobo, loja, loja, false, false, false},
		{"home certo com cargo ADMIN_IMOBILIARIA", masterRoot, "ADMIN_IMOBILIARIA", root, root, false, false, false},
		{"home certo com cargo OPERADOR", masterRoot, "OPERADOR", root, root, false, false, false},
		{"home certo com cargo vazio", masterRoot, "", root, root, false, false, false},
		{"cargo em minúsculas não vale", masterRoot, "admin_imobo", root, root, false, false, false},
		{"admin imobo em acting-as", masterRoot, CargoAdminImobo, root, loja, false, false, false},
		{"env ausente", "", CargoAdminImobo, root, root, false, false, false},
		{"env inválida", "nao-e-uuid", CargoAdminImobo, root, root, false, false, false},
		{"env nil-uuid com home nil-uuid", uuid.Nil.String(), CargoAdminImobo, uuid.Nil, uuid.Nil, false, false, false},
		{"master na raiz", masterRoot, CargoMasterImobo, root, root, true, false, true},
		{"master em acting-as", masterRoot, CargoMasterImobo, root, loja, true, false, true},
		{"cargo MASTER_IMOBO sem a flag não opera carteira", masterRoot, CargoMasterImobo, root, root, false, false, false},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Setenv(EnvMasterRootTenantID, c.env)
			tc := TenantContext{
				Cargo:           c.cargo,
				HomeTenantID:    c.home,
				ActedAsTenantID: c.actingAs,
				IsMasterImobo:   c.master,
			}
			assert.Equal(t, c.admin, tc.EhAdminImobo(), "EhAdminImobo")
			assert.Equal(t, c.carteira, tc.PodeOperarCarteiraImobo(), "PodeOperarCarteiraImobo")
		})
	}
}

// ADMIN_IMOBO não é master: o middleware não promove IsMasterImobo e a sessão
// não ganha visão global no RLS, mas pode operar a carteira.
func TestAdminImobo_PeloMiddleware_NaoViraMaster(t *testing.T) {
	t.Setenv(EnvMasterRootTenantID, masterRoot)
	claims := validClaims()
	claims.ActingAsTenantID = masterRoot
	claims.HomeTenantID = masterRoot
	claims.Cargo = CargoAdminImobo

	tc, code := runMiddlewareWithClaims(t, claims)
	require.Equal(t, http.StatusOK, code)
	assert.False(t, tc.IsMasterImobo)
	assert.False(t, tc.MasterVisaoGlobal())
	assert.True(t, tc.EhAdminImobo())
	assert.True(t, tc.PodeOperarCarteiraImobo())
}
