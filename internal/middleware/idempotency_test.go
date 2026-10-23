package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/awomore/Pill4rsBE/internal/idempotency"
	"github.com/labstack/echo/v4"
)

type fakeIdempotencyStore struct {
	mu      sync.Mutex
	records map[string]idempotency.Record
}

func newFakeStore() *fakeIdempotencyStore {
	return &fakeIdempotencyStore{records: make(map[string]idempotency.Record)}
}

func (s *fakeIdempotencyStore) k(ws, endpoint, key string) string {
	return ws + "|" + endpoint + "|" + key
}

func (s *fakeIdempotencyStore) Get(_ context.Context, ws, endpoint, key string) (idempotency.Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[s.k(ws, endpoint, key)]
	return rec, ok, nil
}

func (s *fakeIdempotencyStore) Begin(_ context.Context, ws, endpoint, key, hash string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.k(ws, endpoint, key)
	if _, ok := s.records[k]; ok {
		return false, nil
	}
	s.records[k] = idempotency.Record{RequestHash: hash, Status: "in_progress"}
	return true, nil
}

func (s *fakeIdempotencyStore) Complete(_ context.Context, ws, endpoint, key string, status int, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[s.k(ws, endpoint, key)] = idempotency.Record{
		RequestHash:    s.records[s.k(ws, endpoint, key)].RequestHash,
		Status:         "completed",
		ResponseStatus: status,
		ResponseBody:   body,
	}
	return nil
}

func (s *fakeIdempotencyStore) Delete(_ context.Context, ws, endpoint, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, s.k(ws, endpoint, key))
	return nil
}

func newIdemServer(store idempotency.Store, calls *int, handlerErr error) *echo.Echo {
	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("workspace_id", "ws1")
			return next(c)
		}
	})
	e.POST("/api/v1/campaigns", func(c echo.Context) error {
		*calls++
		if handlerErr != nil {
			return handlerErr
		}
		return c.JSON(http.StatusCreated, map[string]any{"ok": true, "calls": *calls})
	}, Idempotency(store))
	return e
}

func doIdem(e *echo.Echo, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/campaigns", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(IdempotencyHeader, key)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestIdempotencyReplaysResponse(t *testing.T) {
	store := newFakeStore()
	calls := 0
	e := newIdemServer(store, &calls, nil)

	first := doIdem(e, "key-1", `{"name":"a"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d", first.Code)
	}
	second := doIdem(e, "key-1", `{"name":"a"}`)
	if second.Code != http.StatusCreated {
		t.Fatalf("second status = %d", second.Code)
	}
	if calls != 1 {
		t.Errorf("handler ran %d times, want 1", calls)
	}
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Error("missing replay header")
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("body mismatch: %q vs %q", first.Body.String(), second.Body.String())
	}
}

func TestIdempotencyRejectsDifferentBody(t *testing.T) {
	store := newFakeStore()
	calls := 0
	e := newIdemServer(store, &calls, nil)

	_ = doIdem(e, "key-1", `{"name":"a"}`)
	rec := doIdem(e, "key-1", `{"name":"b"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Errorf("handler ran %d times, want 1", calls)
	}
}

func TestIdempotencyInProgressConflict(t *testing.T) {
	store := newFakeStore()
	calls := 0
	e := newIdemServer(store, &calls, nil)

	// Seed an in-progress record with the same request hash.
	body := `{"name":"a"}`
	hash := requestHash("POST /api/v1/campaigns", []byte(body))
	_, _ = store.Begin(context.Background(), "ws1", "POST /api/v1/campaigns", "key-1", hash)

	rec := doIdem(e, "key-1", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Errorf("handler ran %d times, want 0", calls)
	}
}

func TestIdempotencyDeletesOnHandlerError(t *testing.T) {
	store := newFakeStore()
	calls := 0
	e := newIdemServer(store, &calls, echo.NewHTTPError(http.StatusInternalServerError, "boom"))

	rec := doIdem(e, "key-1", `{"name":"a"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if _, found, _ := store.Get(context.Background(), "ws1", "POST /api/v1/campaigns", "key-1"); found {
		t.Error("record should be deleted after a handler error")
	}
}

func TestIdempotencyWithoutKeyAlwaysRuns(t *testing.T) {
	store := newFakeStore()
	calls := 0
	e := newIdemServer(store, &calls, nil)

	_ = doIdem(e, "", `{"name":"a"}`)
	_ = doIdem(e, "", `{"name":"a"}`)
	if calls != 2 {
		t.Errorf("handler ran %d times, want 2", calls)
	}
}
