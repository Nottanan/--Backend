package mw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"foodorder/auth"
)

func testIssuer() *auth.Issuer {
	return &auth.Issuer{
		Secret: []byte("test-secret-at-least-32-characters"),
		TTL:    time.Hour,
	}
}

func TestRequireLoginWithoutToken(t *testing.T) {
	issuer := testIssuer()

	handler := RequireLogin(issuer, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want status 401, got %d", rec.Code)
	}
}

func TestRequireLoginWithValidToken(t *testing.T) {
	issuer := testIssuer()

	token, err := issuer.Issue(123, "admin")
	if err != nil {
		t.Fatal(err)
	}

	handler := RequireLogin(issuer, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := MemberID(r.Context())
		if !ok || id != 123 {
			t.Errorf("want member ID 123, got %d", id)
		}

		if Role(r.Context()) != "admin" {
			t.Errorf("want role admin, got %s", Role(r.Context()))
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d", rec.Code)
	}
}

func TestRequireRole(t *testing.T) {
	handler := RequireRole("admin", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(
		contextWithRole(req, "customer"),
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want status 403, got %d", rec.Code)
	}
}

func contextWithRole(r *http.Request, role string) context.Context {
	return context.WithValue(r.Context(), roleKey, role)
}
