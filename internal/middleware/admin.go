package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// PlatformAdminChecker verifies a user's platform-admin status.
type PlatformAdminChecker interface {
	IsAdmin(ctx context.Context, userID uuid.UUID) (bool, error)
}

// RequirePlatformAdmin gates the Pill4rs operator API. It requires a user
// session (API keys cannot manage the platform).
func RequirePlatformAdmin(checker PlatformAdminChecker) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			raw, _ := c.Get("user_id").(string)
			userID, err := uuid.Parse(raw)
			if err != nil {
				return c.JSON(http.StatusForbidden, map[string]string{"error": "platform admin session required"})
			}
			ok, err := checker.IsAdmin(c.Request().Context(), userID)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to verify platform admin"})
			}
			if !ok {
				return c.JSON(http.StatusForbidden, map[string]string{"error": "platform admin access required"})
			}
			return next(c)
		}
	}
}
