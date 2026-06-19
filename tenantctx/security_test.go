// Copyright 2026 imobo. Licenca: privada.

package tenantctx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// masterRoot e um UUID REAL (nao-nil) usado como master-root nos testes R1.
const masterRoot = "9f1c0b2a-1111-4b2c-8d3e-aaaaaaaaaaaa"

// runMiddlewareWithClaims roda o HTTPMiddleware com as claims dadas e devolve o
// TenantContext capturado pelo handler (e se foi capturado).
func runMiddlewareWithClaims(t *testing.T, claims Claims) (TenantContext, int) {
	t.Helper()
	validator := &fakeJWTValidator{claims: claims}
	mw := HTTPMiddleware(HTTPMiddlewareConfig{JWTValidator: validator})

	var capturedTC TenantContext
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTC, _ = From(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	req.Header.Set("Authorization", "Bearer faketoken")
	rr := httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)
	return capturedTC, rr.Code
}

// ---------------------------------------------------------------------------
// R1 — endurecimento do sentinela de MASTER
// ---------------------------------------------------------------------------

// R1: token forjado com home=nil-uuid NAO promove master (mesmo com env setada
// e cargo correto). O zero-value nunca e aceito como master-root.
func TestR1_HomeNilUUID_NaoPromoveMaster(t *testing.T) {
	t.Setenv(EnvMasterRootTenantID, masterRoot)
	claims := validClaims()
	claims.HomeTenantID = uuid.Nil.String() // 00000000-...
	claims.Cargo = CargoMasterImobo

	tc, code := runMiddlewareWithClaims(t, claims)
	require.Equal(t, http.StatusOK, code)
	assert.False(t, tc.IsMasterImobo, "home=nil-uuid jamais promove master")
}

// R1: home=MASTER_ROOT mas cargo != MASTER_IMOBO NAO promove (defesa dupla).
func TestR1_HomeMasterRoot_CargoErrado_NaoPromove(t *testing.T) {
	t.Setenv(EnvMasterRootTenantID, masterRoot)
	claims := validClaims()
	claims.HomeTenantID = masterRoot
	claims.Cargo = "ADMIN_IMOBILIARIA" // cargo errado

	tc, code := runMiddlewareWithClaims(t, claims)
	require.Equal(t, http.StatusOK, code)
	assert.False(t, tc.IsMasterImobo, "sem cargo MASTER_IMOBO nao vira master")
}

// R1: home=MASTER_ROOT + cargo=MASTER_IMOBO promove.
func TestR1_HomeMasterRoot_CargoMaster_Promove(t *testing.T) {
	t.Setenv(EnvMasterRootTenantID, masterRoot)
	claims := validClaims()
	claims.HomeTenantID = masterRoot
	claims.Cargo = CargoMasterImobo

	tc, code := runMiddlewareWithClaims(t, claims)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, tc.IsMasterImobo, "home=master-root + cargo=MASTER_IMOBO promove")
}

// R1: env ausente => NUNCA promove (fail-closed), mesmo com cargo correto e
// home == ao que seria o master-root.
func TestR1_EnvAusente_NuncaPromove(t *testing.T) {
	// Garante que a env NAO esta setada neste teste.
	t.Setenv(EnvMasterRootTenantID, "")
	claims := validClaims()
	claims.HomeTenantID = masterRoot
	claims.Cargo = CargoMasterImobo

	tc, code := runMiddlewareWithClaims(t, claims)
	require.Equal(t, http.StatusOK, code)
	assert.False(t, tc.IsMasterImobo, "sem env MASTER_ROOT_TENANT_ID, ninguem vira master")
}

// R1: env invalida (nao-UUID) => fail-closed.
func TestR1_EnvInvalida_NuncaPromove(t *testing.T) {
	t.Setenv(EnvMasterRootTenantID, "nao-e-uuid")
	claims := validClaims()
	claims.HomeTenantID = masterRoot
	claims.Cargo = CargoMasterImobo

	tc, code := runMiddlewareWithClaims(t, claims)
	require.Equal(t, http.StatusOK, code)
	assert.False(t, tc.IsMasterImobo)
}

// R1: env = nil-uuid e EXPLICITAMENTE rejeitada como sentinela.
func TestR1_EnvNilUUID_NuncaPromove(t *testing.T) {
	t.Setenv(EnvMasterRootTenantID, uuid.Nil.String())
	claims := validClaims()
	claims.HomeTenantID = uuid.Nil.String()
	claims.Cargo = CargoMasterImobo

	tc, code := runMiddlewareWithClaims(t, claims)
	require.Equal(t, http.StatusOK, code)
	assert.False(t, tc.IsMasterImobo, "nil-uuid nunca e master-root, mesmo via env")
}

// R1: IsMasterRootTenant respeita a env e o fail-closed.
func TestR1_IsMasterRootTenant(t *testing.T) {
	root := uuid.MustParse(masterRoot)

	t.Run("env setada => true para o root", func(t *testing.T) {
		t.Setenv(EnvMasterRootTenantID, masterRoot)
		assert.True(t, IsMasterRootTenant(root))
		assert.False(t, IsMasterRootTenant(uuid.New()))
		assert.False(t, IsMasterRootTenant(uuid.Nil))
	})

	t.Run("env ausente => sempre false", func(t *testing.T) {
		t.Setenv(EnvMasterRootTenantID, "")
		assert.False(t, IsMasterRootTenant(root))
		assert.False(t, IsMasterRootTenant(uuid.Nil))
	})
}

// ---------------------------------------------------------------------------
// #40 — entitlement (claim `srv` / Servicos / HasService / RequireService)
// ---------------------------------------------------------------------------

// #40: claim `srv` faz round-trip emitir(srv)->validar->Servicos no contexto.
// (Nao ha issuer concreto na lib; o seam de emissao e o struct Claims, que o
// emissor — iam — preenche. Aqui validamos que srv emitido sobrevive ate o ctx.)
func TestEntitlement_Srv_RoundTrip(t *testing.T) {
	claims := validClaims()
	claims.Srv = []string{"fiscal", "cobranca", "comunicacao"}

	tc, code := runMiddlewareWithClaims(t, claims)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, []string{"fiscal", "cobranca", "comunicacao"}, tc.Servicos)
}

// #40: srv ausente => Servicos vazio.
func TestEntitlement_Srv_Ausente_Vazio(t *testing.T) {
	claims := validClaims() // sem Srv
	tc, code := runMiddlewareWithClaims(t, claims)
	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, tc.Servicos)
}

func TestHasService(t *testing.T) {
	tc := TenantContext{Servicos: []string{"fiscal", "cobranca"}}
	ctx := Inject(context.Background(), tc)

	assert.True(t, HasService(ctx, "fiscal"))
	assert.True(t, HasService(ctx, "cobranca"))
	assert.False(t, HasService(ctx, "comunicacao"), "servico ausente => false")
}

func TestHasService_SemContexto_False(t *testing.T) {
	// Sem TenantContext no ctx => fail-closed.
	assert.False(t, HasService(context.Background(), "fiscal"))
}

func TestHasService_SemSrv_False(t *testing.T) {
	tc := TenantContext{} // Servicos vazio
	ctx := Inject(context.Background(), tc)
	assert.False(t, HasService(ctx, "fiscal"))
}

// requireServiceWith roda RequireService(nome) sobre um TenantContext fixo e
// devolve o status code e se o handler downstream foi chamado.
func requireServiceWith(t *testing.T, nome string, tc *TenantContext) (int, bool) {
	t.Helper()
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fiscal/notas", nil)
	if tc != nil {
		req = req.WithContext(Inject(req.Context(), *tc))
	}
	rr := httptest.NewRecorder()
	RequireService(nome)(handler).ServeHTTP(rr, req)
	return rr.Code, called
}

// #40: RequireService("x") => 403 (servico_nao_contratado) quando srv nao contem "x".
func TestRequireService_Negado_403(t *testing.T) {
	tc := TenantContext{Servicos: []string{"cobranca"}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler nao deveria ser chamado")
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fiscal/notas", nil)
	req = req.WithContext(Inject(req.Context(), tc))
	rr := httptest.NewRecorder()
	RequireService("fiscal")(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	body, _ := io.ReadAll(rr.Body)
	assert.Contains(t, string(body), "servico_nao_contratado")
}

// #40: RequireService passa quando srv contem o servico.
func TestRequireService_Permitido(t *testing.T) {
	tc := TenantContext{Servicos: []string{"fiscal", "cobranca"}}
	code, called := requireServiceWith(t, "fiscal", &tc)
	assert.Equal(t, http.StatusOK, code)
	assert.True(t, called)
}

// #40: sem `srv` (lista vazia) => 403.
func TestRequireService_SemSrv_403(t *testing.T) {
	tc := TenantContext{} // Servicos vazio
	code, called := requireServiceWith(t, "fiscal", &tc)
	assert.Equal(t, http.StatusForbidden, code)
	assert.False(t, called)
}

// #40: sem TenantContext no ctx => 403 (fail-closed).
func TestRequireService_SemContexto_403(t *testing.T) {
	code, called := requireServiceWith(t, "fiscal", nil)
	assert.Equal(t, http.StatusForbidden, code)
	assert.False(t, called)
}

// #40: sem bypass pra master — RequireService nao olha IsMasterImobo, so `srv`.
// Master sem o servico no claim e bloqueado.
func TestRequireService_MasterSemBypass(t *testing.T) {
	tc := TenantContext{IsMasterImobo: true, Servicos: []string{"cobranca"}}
	code, called := requireServiceWith(t, "fiscal", &tc)
	assert.Equal(t, http.StatusForbidden, code, "master nao tem bypass; vale o claim srv")
	assert.False(t, called)
}
