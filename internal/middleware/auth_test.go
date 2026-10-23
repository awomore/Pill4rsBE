package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

type fakeVerifier struct {
	key       string
	workspace string
	agency    string
	scopes    []string
}

func (f fakeVerifier) VerifyAPIKey(_ context.Context, raw string) (string, string, []string, error) {
	if raw == f.key {
		return f.workspace, f.agency, f.scopes, nil
	}
	return "", "", nil, errors.New("unknown key")
}

func (f fakeVerifier) ResolveAgencyClient(_ context.Context, agencyID, clientID string) (string, error) {
	if agencyID == f.agency && clientID == "client1" {
		return "ws-client", nil
	}
	return "", errors.New("unknown client")
}

func newAuthServer(v APIKeyVerifier) *echo.Echo {
	e := echo.New()
	e.Use(Auth([]byte("jwt-secret"), v))
	h := func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"workspace": c.Get("workspace_id").(string)})
	}
	e.GET("/api/v1/campaigns", h, RequireScope("campaigns:read"))
	e.GET("/api/v1/accounts", h, RequireScope("accounts:read"))
	return e
}

func TestAuthAcceptsAPIKeyWithScope(t *testing.T) {
	e := newAuthServer(fakeVerifier{key: "p4_live_ok", workspace: "ws1", scopes: []string{"campaigns:read"}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/campaigns", nil)
	req.Header.Set("Authorization", "Bearer p4_live_ok")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthAcceptsXAPIKeyHeader(t *testing.T) {
	e := newAuthServer(fakeVerifier{key: "p4_live_ok", workspace: "ws1", scopes: []string{"campaigns:read"}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/campaigns", nil)
	req.Header.Set("X-API-Key", "p4_live_ok")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthRejectsMissingScope(t *testing.T) {
	e := newAuthServer(fakeVerifier{key: "p4_live_ok", workspace: "ws1", scopes: []string{"accounts:read"}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/campaigns", nil)
	req.Header.Set("Authorization", "Bearer p4_live_ok")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthRejectsInvalidKey(t *testing.T) {
	e := newAuthServer(fakeVerifier{key: "p4_live_ok", workspace: "ws1", scopes: []string{"*"}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/campaigns", nil)
	req.Header.Set("Authorization", "Bearer p4_live_bad")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthRequiresCredentials(t *testing.T) {
	e := newAuthServer(fakeVerifier{key: "p4_live_ok", workspace: "ws1", scopes: []string{"*"}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/campaigns", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHasScope(t *testing.T) {
	if !HasScope([]string{"*"}, "campaigns:write") {
		t.Error("wildcard should match")
	}
	if !HasScope([]string{"campaigns:write"}, "campaigns:write") {
		t.Error("exact scope should match")
	}
	if HasScope([]string{"campaigns:read"}, "campaigns:write") {
		t.Error("missing scope matched")
	}
}

func TestAPIKeyClientScoping(t *testing.T) {
	e := newAuthServer(fakeVerifier{key: "p4_live_ok", workspace: "ws-default", agency: "ag1", scopes: []string{"campaigns:read"}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/campaigns", nil)
	req.Header.Set("Authorization", "Bearer p4_live_ok")
	req.Header.Set("X-Client-Id", "client1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ws-client") {
		t.Errorf("workspace not switched: %s", rec.Body.String())
	}
}

func TestAPIKeyClientScopingRequiresAgencyKey(t *testing.T) {
	e := newAuthServer(fakeVerifier{key: "p4_live_ok", workspace: "ws-default", scopes: []string{"campaigns:read"}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/campaigns", nil)
	req.Header.Set("Authorization", "Bearer p4_live_ok")
	req.Header.Set("X-Client-Id", "client1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}
