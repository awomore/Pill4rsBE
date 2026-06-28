package meta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetStatus(t *testing.T) {
	var gotPath, gotMethod, gotStatus, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = r.ParseForm()
		gotStatus = r.Form.Get("status")
		gotToken = r.Form.Get("access_token")
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	if err := newTestClient(srv.URL).SetStatus(context.Background(), "tok", "act_1", "23847", "PAUSED"); err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q want POST", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/23847") {
		t.Errorf("path = %q", gotPath)
	}
	if gotStatus != "PAUSED" || gotToken != "tok" {
		t.Errorf("form status=%q token=%q", gotStatus, gotToken)
	}
}

func TestSetStatusGraphError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"Invalid status","code":100}}`))
	}))
	defer srv.Close()

	err := newTestClient(srv.URL).SetStatus(context.Background(), "tok", "act_1", "1", "PAUSED")
	if err == nil || !strings.Contains(err.Error(), "Invalid status") {
		t.Fatalf("expected graph error, got %v", err)
	}
}

func TestUpdateAdSetBudget(t *testing.T) {
	var gotPath, gotBudget string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = r.ParseForm()
		gotBudget = r.Form.Get("daily_budget")
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	if err := newTestClient(srv.URL).UpdateAdSetBudget(context.Background(), "tok", "act_1", "555", 50.50); err != nil {
		t.Fatalf("UpdateAdSetBudget failed: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/555") {
		t.Errorf("path = %q", gotPath)
	}
	if gotBudget != "5050" {
		t.Errorf("daily_budget = %q want 5050 (minor units)", gotBudget)
	}
}
