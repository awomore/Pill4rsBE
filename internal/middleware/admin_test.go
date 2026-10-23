package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type fakeAdminChecker struct{ admin bool }

func (f fakeAdminChecker) IsAdmin(_ context.Context, _ uuid.UUID) (bool, error) {
	return f.admin, nil
}

func adminServer(admin bool) *echo.Echo {
	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if uid := c.Request().Header.Get("X-User"); uid != "" {
				c.Set("user_id", uid)
			}
			return next(c)
		}
	})
	e.GET("/api/admin/overview", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"ok": "true"})
	}, RequirePlatformAdmin(fakeAdminChecker{admin: admin}))
	return e
}

func TestRequirePlatformAdmin(t *testing.T) {
	e := adminServer(true)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil)
	req.Header.Set("X-User", uuid.NewString())
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRequirePlatformAdminRejectsNonAdmin(t *testing.T) {
	e := adminServer(false)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil)
	req.Header.Set("X-User", uuid.NewString())
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRequirePlatformAdminRejectsAPIKeySession(t *testing.T) {
	e := adminServer(true)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}
