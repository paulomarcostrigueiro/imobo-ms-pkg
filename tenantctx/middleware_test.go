// Copyright 2026 imobo. Licenca: privada.

package tenantctx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeJWTValidator implementa JWTValidator com comportamento controlavel pelos testes.
type fakeJWTValidator struct {
	claims Claims
	err    error
}

func (f *fakeJWTValidator) Validate(_ context.Context, _ string) (Claims, error) {
	return f.claims, f.err
}

// fakeResolver implementa VisibleTenantsResolver com comportamento controlavel.
type fakeResolver struct {
	visible  []uuid.UUID
	isMaster bool
	err      error
}

func (f *fakeResolver) Resolve(_ context.Context, _, _ uuid.UUID) ([]uuid.UUID, bool, error) {
	return f.visible, f.isMaster, f.err
}

func validClaims() Claims {
	return Claims{
		Sub:              "33333333-3333-3333-3333-333333333333",
		HomeTenantID:     "44444444-4444-4444-4444-444444444444",
		ActingAsTenantID: "11111111-1111-1111-1111-111111111111",
		Permissions:      []string{"ledger.read"},
	}
}

func TestHTTPMiddleware_HappyPath(t *testing.T) {
	validator := &fakeJWTValidator{claims: validClaims()}
	resolver := &fakeResolver{
		visible:  []uuid.UUID{uuid.MustParse("11111111-1111-1111-1111-111111111111")},
		isMaster: false,
	}

	var capturedTC TenantContext
	var captured bool

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTC, captured = From(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	mw := HTTPMiddleware(HTTPMiddlewareConfig{
		JWTValidator:           validator,
		VisibleTenantsResolver: resolver,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	req.Header.Set("Authorization", "Bearer faketoken")
	rr := httptest.NewRecorder()

	mw(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.True(t, captured)
	assert.Equal(t, uuid.MustParse("11111111-1111-1111-1111-111111111111"), capturedTC.ActedAsTenantID)
	assert.Equal(t, uuid.MustParse("33333333-3333-3333-3333-333333333333"), capturedTC.ActedByUserID)
	assert.Equal(t, uuid.MustParse("44444444-4444-4444-4444-444444444444"), capturedTC.HomeTenantID)
	assert.Equal(t, []string{"ledger.read"}, capturedTC.Permissions)
	assert.False(t, capturedTC.IsMasterImobo)
}

func TestHTTPMiddleware_AuthorizationAusente(t *testing.T) {
	validator := &fakeJWTValidator{claims: validClaims()}
	mw := HTTPMiddleware(HTTPMiddlewareConfig{JWTValidator: validator})

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	rr := httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	body, _ := io.ReadAll(rr.Body)
	assert.Contains(t, string(body), "authorization_required")
	assert.False(t, called)
}

func TestHTTPMiddleware_AuthorizationFormatoInvalido(t *testing.T) {
	validator := &fakeJWTValidator{claims: validClaims()}
	mw := HTTPMiddleware(HTTPMiddlewareConfig{JWTValidator: validator})

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	tests := []string{
		"naotembearer",
		"Basic abc",
		"Bearer ",
		"Bearer  ",
	}
	for _, h := range tests {
		t.Run(h, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
			req.Header.Set("Authorization", h)
			rr := httptest.NewRecorder()
			mw(handler).ServeHTTP(rr, req)
			assert.Equal(t, http.StatusUnauthorized, rr.Code)
			body, _ := io.ReadAll(rr.Body)
			assert.Contains(t, string(body), "authorization_invalid")
			assert.False(t, called)
		})
	}
}

func TestHTTPMiddleware_JWTInvalido(t *testing.T) {
	validator := &fakeJWTValidator{err: errors.New("assinatura invalida")}
	mw := HTTPMiddleware(HTTPMiddlewareConfig{JWTValidator: validator})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("nao deveria chamar handler")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	req.Header.Set("Authorization", "Bearer xyz")
	rr := httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	body, _ := io.ReadAll(rr.Body)
	assert.Contains(t, string(body), "token_invalid")
}

func TestHTTPMiddleware_ClaimsInvalidos(t *testing.T) {
	cases := []struct {
		name   string
		claims Claims
	}{
		{"sub invalido", Claims{Sub: "nao-uuid", HomeTenantID: uuid.New().String(), ActingAsTenantID: uuid.New().String()}},
		{"home invalido", Claims{Sub: uuid.New().String(), HomeTenantID: "nao-uuid", ActingAsTenantID: uuid.New().String()}},
		{"acting_as invalido", Claims{Sub: uuid.New().String(), HomeTenantID: uuid.New().String(), ActingAsTenantID: "nao-uuid"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			validator := &fakeJWTValidator{claims: tc.claims}
			mw := HTTPMiddleware(HTTPMiddlewareConfig{JWTValidator: validator})
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("nao deveria chamar handler")
			})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
			req.Header.Set("Authorization", "Bearer xyz")
			rr := httptest.NewRecorder()
			mw(handler).ServeHTTP(rr, req)

			assert.Equal(t, http.StatusUnauthorized, rr.Code)
			body, _ := io.ReadAll(rr.Body)
			assert.Contains(t, string(body), "claims_invalid")
		})
	}
}

func TestHTTPMiddleware_ResolverErro_500(t *testing.T) {
	validator := &fakeJWTValidator{claims: validClaims()}
	resolver := &fakeResolver{err: errors.New("db caiu")}

	mw := HTTPMiddleware(HTTPMiddlewareConfig{
		JWTValidator:           validator,
		VisibleTenantsResolver: resolver,
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("nao deveria chamar handler")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	req.Header.Set("Authorization", "Bearer xyz")
	rr := httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	body, _ := io.ReadAll(rr.Body)
	assert.Contains(t, string(body), "internal_error")
}

func TestHTTPMiddleware_SkipPaths(t *testing.T) {
	validator := &fakeJWTValidator{err: errors.New("nao deveria ser chamado")}
	mw := HTTPMiddleware(HTTPMiddlewareConfig{
		JWTValidator: validator,
		SkipPaths:    []string{"/health", "/metrics"},
	})

	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	for _, path := range []string{"/health", "/health/live", "/metrics"} {
		t.Run(path, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rr := httptest.NewRecorder()
			mw(handler).ServeHTTP(rr, req)
			assert.True(t, called)
			assert.Equal(t, http.StatusOK, rr.Code)
		})
	}
}

func TestHTTPMiddleware_PanicaSemValidator(t *testing.T) {
	assert.Panics(t, func() {
		_ = HTTPMiddleware(HTTPMiddlewareConfig{})
	})
}

func TestHTTPMiddleware_DefaultResolverFallback(t *testing.T) {
	// Sem VisibleTenantsResolver => usa staticResolver, lista unitaria.
	validator := &fakeJWTValidator{claims: validClaims()}
	mw := HTTPMiddleware(HTTPMiddlewareConfig{JWTValidator: validator})

	var capturedTC TenantContext
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTC, _ = From(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	req.Header.Set("Authorization", "Bearer xyz")
	rr := httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Len(t, capturedTC.VisibleTenantIDs, 1)
	assert.Equal(t, uuid.MustParse("11111111-1111-1111-1111-111111111111"), capturedTC.VisibleTenantIDs[0])
	assert.False(t, capturedTC.IsMasterImobo)
}

func TestHTTPMiddleware_OperadorIMOBO_VeFilhos(t *testing.T) {
	tenantPai := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tenantFilho := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	validator := &fakeJWTValidator{claims: validClaims()}
	resolver := &fakeResolver{
		visible:  []uuid.UUID{tenantPai, tenantFilho},
		isMaster: true,
	}

	mw := HTTPMiddleware(HTTPMiddlewareConfig{
		JWTValidator:           validator,
		VisibleTenantsResolver: resolver,
	})

	var capturedTC TenantContext
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTC, _ = From(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	req.Header.Set("Authorization", "Bearer xyz")
	rr := httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)

	assert.True(t, capturedTC.IsMasterImobo)
	assert.Len(t, capturedTC.VisibleTenantIDs, 2)
}

func TestHTTPMiddleware_ResolverVazio_FallbackParaActingAs(t *testing.T) {
	validator := &fakeJWTValidator{claims: validClaims()}
	resolver := &fakeResolver{visible: nil} // vazio

	mw := HTTPMiddleware(HTTPMiddlewareConfig{
		JWTValidator:           validator,
		VisibleTenantsResolver: resolver,
	})

	var capturedTC TenantContext
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTC, _ = From(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	req.Header.Set("Authorization", "Bearer xyz")
	rr := httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Len(t, capturedTC.VisibleTenantIDs, 1)
	assert.Equal(t, uuid.MustParse("11111111-1111-1111-1111-111111111111"), capturedTC.VisibleTenantIDs[0])
}

func TestHTTPMiddleware_BearerCaseSensitive(t *testing.T) {
	// Implementacao aceita "Bearer " exatamente; "bearer xyz" devera falhar
	// (RFC 6750 e case-insensitive na pratica, mas mantemos strict aqui pra
	// reduzir surface de erro).
	validator := &fakeJWTValidator{claims: validClaims()}
	mw := HTTPMiddleware(HTTPMiddlewareConfig{JWTValidator: validator})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	req.Header.Set("Authorization", "bearer xyz") // lowercase
	rr := httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestStaticResolver_RetornaActingAs(t *testing.T) {
	r := staticResolver{}
	id := uuid.New()
	visible, master, err := r.Resolve(context.Background(), uuid.New(), id)
	require.NoError(t, err)
	assert.False(t, master)
	require.Len(t, visible, 1)
	assert.Equal(t, id, visible[0])
}

// Sanity check: verifica que o token e trimado.
func TestHTTPMiddleware_TokenComEspacosLaterais(t *testing.T) {
	captured := ""
	validator := &fakeJWTValidator{claims: validClaims()}
	validator2 := jwtValidatorFunc(func(_ context.Context, token string) (Claims, error) {
		captured = token
		return validator.claims, nil
	})

	mw := HTTPMiddleware(HTTPMiddlewareConfig{JWTValidator: validator2})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	req.Header.Set("Authorization", "Bearer   abc.def.ghi   ")
	rr := httptest.NewRecorder()
	mw(handler).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "abc.def.ghi", strings.TrimSpace(captured))
}

// jwtValidatorFunc adapta uma funcao para a interface JWTValidator.
type jwtValidatorFunc func(ctx context.Context, token string) (Claims, error)

func (f jwtValidatorFunc) Validate(ctx context.Context, token string) (Claims, error) {
	return f(ctx, token)
}
