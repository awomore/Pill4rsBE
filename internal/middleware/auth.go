package middleware

import (
	"net/http"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/labstack/echo/v4"
)

func Auth(jwtSecret []byte) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Request().Method == http.MethodOptions ||
				c.Path() == "/health" ||
				strings.HasPrefix(c.Path(), "/api/auth/") {
				return next(c)
			}

			cookie, err := c.Cookie("access_token")
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing access token"})
			}

			claims, err := crypto.VerifyAccessToken(cookie.Value, jwtSecret)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid access token"})
			}

			c.Set("user_id", claims.UserID)
			c.Set("workspace_id", claims.WorkspaceID)
			return next(c)
		}
	}
}
