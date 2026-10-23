package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/labstack/echo/v4"
)

// APIKeyVerifier resolves a plaintext API key to its workspace and scopes.
type APIKeyVerifier interface {
	VerifyAPIKey(ctx context.Context, raw string) (workspaceID, agencyID string, scopes []string, err error)
	ResolveAgencyClient(ctx context.Context, agencyID, clientID string) (workspaceID string, err error)
}

// Auth accepts either an API key (Authorization: Bearer p4_... or X-API-Key) or
// the httpOnly access_token cookie used by the UI. API keys are intended for
// external marketing brands automating campaigns through /api/v1.
func Auth(jwtSecret []byte, apiKeys ...APIKeyVerifier) echo.MiddlewareFunc {
	var verifier APIKeyVerifier
	if len(apiKeys) > 0 {
		verifier = apiKeys[0]
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Request().Method == http.MethodOptions ||
				c.Path() == "/health" ||
				strings.HasPrefix(c.Path(), "/media/") ||
				strings.HasPrefix(c.Path(), "/api/webhooks/") ||
				strings.HasPrefix(c.Path(), "/api/auth/") {
				return next(c)
			}

			if key := apiKeyFromRequest(c); key != "" {
				if verifier == nil {
					return c.JSON(http.StatusUnauthorized, map[string]string{"error": "api key authentication is not configured"})
				}
				workspaceID, agencyID, scopes, err := verifier.VerifyAPIKey(c.Request().Context(), key)
				if err != nil || workspaceID == "" {
					return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid api key"})
				}

				// Agency keys can target any client in the agency. Without the
				// header they act on the key's default workspace.
				if clientID := strings.TrimSpace(c.Request().Header.Get("X-Client-Id")); clientID != "" {
					if agencyID == "" {
						return c.JSON(http.StatusForbidden, map[string]string{"error": "client scoping requires an agency-scoped api key"})
					}
					resolved, err := verifier.ResolveAgencyClient(c.Request().Context(), agencyID, clientID)
					if err != nil || resolved == "" {
						return c.JSON(http.StatusForbidden, map[string]string{"error": "api key cannot access this client"})
					}
					workspaceID = resolved
				}

				c.Set("workspace_id", workspaceID)
				if agencyID != "" {
					c.Set("agency_id", agencyID)
				}
				c.Set("api_key_scopes", scopes)
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

// RequireScope enforces an API key scope. Cookie-authenticated UI sessions are
// always allowed; API keys must have the scope or the wildcard.
func RequireScope(scope string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			raw, ok := c.Get("api_key_scopes").([]string)
			if !ok {
				return next(c)
			}
			if HasScope(raw, scope) {
				return next(c)
			}
			return c.JSON(http.StatusForbidden, map[string]string{"error": "api key is missing scope: " + scope})
		}
	}
}

// HasScope reports whether scopes contains the wanted scope or "*".
func HasScope(scopes []string, wanted string) bool {
	for _, s := range scopes {
		if s == "*" || s == wanted {
			return true
		}
	}
	return false
}

func apiKeyFromRequest(c echo.Context) string {
	if key := strings.TrimSpace(c.Request().Header.Get("X-API-Key")); key != "" {
		return key
	}
	auth := strings.TrimSpace(c.Request().Header.Get("Authorization"))
	const bearer = "Bearer "
	if len(auth) > len(bearer) && strings.EqualFold(auth[:len(bearer)], bearer) {
		token := strings.TrimSpace(auth[len(bearer):])
		if strings.HasPrefix(token, "p4_") {
			return token
		}
	}
	return ""
}
