package meta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newTestClient(serverURL string) *Client {
	c := NewClient("app-123", "secret-xyz", "https://api.example.com/api/integrations/meta/callback")
	c.graphBaseURL = serverURL
	c.dialogBaseURL = serverURL
	return c
}

func TestGetOAuthURL(t *testing.T) {
	c := NewClient("app-123", "secret-xyz", "https://api.example.com/api/integrations/meta/callback")

	raw := c.GetOAuthURL("state-token-abc")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("invalid url: %v", err)
	}

	if u.Host != "www.facebook.com" {
		t.Errorf("host: got %q want www.facebook.com", u.Host)
	}
	if !strings.HasSuffix(u.Path, "/dialog/oauth") {
		t.Errorf("path: got %q want suffix /dialog/oauth", u.Path)
	}

	q := u.Query()
	checks := map[string]string{
		"client_id":     "app-123",
		"redirect_uri":  "https://api.example.com/api/integrations/meta/callback",
		"state":         "state-token-abc",
		"scope":         "ads_read,ads_management",
		"response_type": "code",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("query %q: got %q want %q", k, got, want)
		}
	}
}

func TestExchangeCodeForToken(t *testing.T) {
	var gotMethod, gotContentType, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotBody = r.Form.Encode()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"the-access-token","token_type":"bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	token, err := c.ExchangeCodeForToken(context.Background(), "auth-code-1")
	if err != nil {
		t.Fatalf("ExchangeCodeForToken failed: %v", err)
	}

	if token.AccessToken != "the-access-token" {
		t.Errorf("access token: got %q", token.AccessToken)
	}
	if token.ExpiresAt.IsZero() {
		t.Error("expected non-zero expiry")
	}
	if d := time.Until(token.ExpiresAt); d < 50*time.Minute || d > 65*time.Minute {
		t.Errorf("expiry out of expected range: %v", d)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q want POST", gotMethod)
	}
	if !strings.HasPrefix(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("content-type: got %q", gotContentType)
	}
	for _, want := range []string{"code=auth-code-1", "client_id=app-123", "client_secret=secret-xyz", "redirect_uri="} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body missing %q, got %q", want, gotBody)
		}
	}
}

func TestExchangeCodeForTokenNoExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"long-lived","token_type":"bearer"}`))
	}))
	defer srv.Close()

	token, err := newTestClient(srv.URL).ExchangeCodeForToken(context.Background(), "code")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !token.ExpiresAt.IsZero() {
		t.Errorf("expected zero expiry when expires_in absent, got %v", token.ExpiresAt)
	}
}

func TestExchangeCodeForTokenGraphError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"Invalid verification code","type":"OAuthException","code":100}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).ExchangeCodeForToken(context.Background(), "bad-code")
	if err == nil {
		t.Fatal("expected error from graph error response")
	}
	if !strings.Contains(err.Error(), "Invalid verification code") {
		t.Errorf("error should surface graph message, got %v", err)
	}
}

func TestFetchAdAccountID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/me/adaccounts") {
			w.Write([]byte(`{"data":[{"id":"act_987654","account_id":"987654"}]}`))
			return
		}
		t.Errorf("unexpected path %q", r.URL.Path)
	}))
	defer srv.Close()

	id, err := newTestClient(srv.URL).FetchAdAccountID(context.Background(), "tok")
	if err != nil {
		t.Fatalf("FetchAdAccountID failed: %v", err)
	}
	if id != "act_987654" {
		t.Errorf("id: got %q want act_987654", id)
	}
}

func TestFetchAdAccountIDFallbackToMe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/me/adaccounts"):
			w.Write([]byte(`{"data":[]}`))
		case strings.HasSuffix(r.URL.Path, "/me"):
			w.Write([]byte(`{"id":"user-555"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	id, err := newTestClient(srv.URL).FetchAdAccountID(context.Background(), "tok")
	if err != nil {
		t.Fatalf("FetchAdAccountID failed: %v", err)
	}
	if id != "user-555" {
		t.Errorf("id: got %q want user-555", id)
	}
}

func TestFetchAdAccountIDGraphError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":{"message":"Invalid OAuth access token","type":"OAuthException","code":190}}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).FetchAdAccountID(context.Background(), "bad")
	if err == nil {
		t.Fatal("expected error from graph error response")
	}
}

// TestOAuthRoundTrip exercises the full sequence the callback handler performs:
// build the dialog URL, exchange the returned code, then resolve the ad account.
func TestOAuthRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth/access_token"):
			w.Write([]byte(`{"access_token":"round-trip-token","expires_in":5184000}`))
		case strings.HasSuffix(r.URL.Path, "/me/adaccounts"):
			w.Write([]byte(`{"data":[{"id":"act_111"}]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)

	authURL := c.GetOAuthURL("csrf-state")
	if !strings.Contains(authURL, "state=csrf-state") {
		t.Fatalf("auth url missing state: %s", authURL)
	}

	token, err := c.ExchangeCodeForToken(context.Background(), "returned-code")
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if token.AccessToken != "round-trip-token" {
		t.Fatalf("unexpected token: %q", token.AccessToken)
	}

	id, err := c.FetchAdAccountID(context.Background(), token.AccessToken)
	if err != nil {
		t.Fatalf("fetch account failed: %v", err)
	}
	if id != "act_111" {
		t.Fatalf("unexpected account id: %q", id)
	}
}
