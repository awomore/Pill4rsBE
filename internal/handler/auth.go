package handler

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/config"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/labstack/echo/v4"
)

type AuthHandler struct {
	svc *service.AuthService
	cfg *config.Config
}

func NewAuthHandler(svc *service.AuthService, cfg *config.Config) *AuthHandler {
	return &AuthHandler{svc: svc, cfg: cfg}
}

type signupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) Signup(c echo.Context) error {
	var req signupRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	result, err := h.svc.Signup(c.Request().Context(), req.Email, req.Password)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "invalid email"):
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		case strings.Contains(err.Error(), "password"):
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		case err == service.ErrEmailAlreadyExists:
			return c.JSON(http.StatusConflict, map[string]string{"error": "email already registered"})
		default:
			slog.Error("signup failed", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "signup failed"})
		}
	}

	setTokenCookies(c, result.Tokens)
	return c.JSON(http.StatusCreated, map[string]interface{}{
		"workspace": result.Workspace,
	})
}

func (h *AuthHandler) Login(c echo.Context) error {
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	result, err := h.svc.Login(c.Request().Context(), req.Email, req.Password)
	if err != nil {
		if err == service.ErrInvalidCredentials {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
		}
		slog.Error("login failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "login failed"})
	}

	setTokenCookies(c, result.Tokens)
	return c.JSON(http.StatusOK, map[string]interface{}{
		"workspace": result.Workspace,
	})
}

func (h *AuthHandler) Refresh(c echo.Context) error {
	cookie, err := c.Cookie("refresh_token")
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing refresh token"})
	}

	tokens, err := h.svc.Refresh(c.Request().Context(), cookie.Value)
	if err != nil {
		switch err {
		case service.ErrInvalidRefresh:
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid refresh token"})
		case service.ErrSessionExpired:
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "session expired"})
		default:
			slog.Error("refresh failed", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "refresh failed"})
		}
	}

	setTokenCookies(c, *tokens)
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AuthHandler) Logout(c echo.Context) error {
	cookie, err := c.Cookie("refresh_token")
	if err != nil {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}

	h.svc.Logout(c.Request().Context(), cookie.Value)

	clearCookies(c)
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AuthHandler) GoogleLogin(c echo.Context) error {
	if h.cfg.GoogleClientID == "" {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "google auth not configured"})
	}

	state := generateState()
	c.SetCookie(&http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/api/auth/google",
		MaxAge:   600,
	})

	redirectURI := h.cfg.GoogleRedirectURI
	if redirectURI == "" {
		redirectURI = fmt.Sprintf("http://localhost:%s/api/auth/google/callback", h.cfg.Port)
	}

	authURL := fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=openid+email+profile&state=%s&access_type=online",
		h.cfg.GoogleClientID,
		redirectURI,
		state,
	)
	return c.Redirect(http.StatusTemporaryRedirect, authURL)
}

func (h *AuthHandler) GoogleCallback(c echo.Context) error {
	code := c.QueryParam("code")
	state := c.QueryParam("state")

	if code == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing authorization code"})
	}

	cookie, err := c.Cookie("oauth_state")
	if err != nil || cookie.Value == "" || cookie.Value != state {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid oauth state"})
	}
	c.SetCookie(&http.Cookie{
		Name: "oauth_state", Value: "", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode, Path: "/api/auth/google", MaxAge: -1,
	})

	redirectURI := h.cfg.GoogleRedirectURI
	if redirectURI == "" {
		redirectURI = fmt.Sprintf("http://localhost:%s/api/auth/google/callback", h.cfg.Port)
	}

	result, err := h.svc.GoogleCallback(
		c.Request().Context(),
		code,
		h.cfg.GoogleClientID,
		h.cfg.GoogleClientSecret,
		redirectURI,
	)
	if err != nil {
		return c.Redirect(http.StatusTemporaryRedirect, h.cfg.FrontendOrigin+"/?error=google_auth_failed")
	}

	setTokenCookies(c, result.Tokens)
	return c.Redirect(http.StatusTemporaryRedirect, h.cfg.FrontendOrigin+"/?success=1&workspace_id="+result.Workspace.ID)
}

func generateState() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

func setTokenCookies(c echo.Context, tokens service.TokenPair) {
	accessCookie := &http.Cookie{
		Name:     "access_token",
		Value:    tokens.AccessToken,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		MaxAge:   900,
	}
	refreshCookie := &http.Cookie{
		Name:     "refresh_token",
		Value:    tokens.RefreshToken,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/api/auth/refresh",
		MaxAge:   604800,
	}
	c.SetCookie(accessCookie)
	c.SetCookie(refreshCookie)
}

func clearCookies(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     "access_token",
		Value:    "",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		MaxAge:   -1,
	})
	c.SetCookie(&http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/api/auth/refresh",
		MaxAge:   -1,
	})
}
