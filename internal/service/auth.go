package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrEmailAlreadyExists = errors.New("email already exists")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidRefresh     = errors.New("invalid refresh token")
	ErrSessionExpired     = errors.New("session expired")
	ErrInvalidGoogleToken = errors.New("invalid google token")
)

type AuthService struct {
	adminEmails map[string]struct{}
	queries     *db.Queries
	jwtSecret   []byte
}

func (s *AuthService) SetPlatformAdminEmails(emails []string) {
	m := make(map[string]struct{}, len(emails))
	for _, e := range emails {
		if e = strings.TrimSpace(strings.ToLower(e)); e != "" {
			m[e] = struct{}{}
		}
	}
	s.adminEmails = m
}

func NewAuthService(queries *db.Queries, jwtSecret []byte) *AuthService {
	return &AuthService{queries: queries, jwtSecret: jwtSecret}
}

type WorkspaceInfo struct {
	ID                 string `json:"id"`
	OnboardingComplete bool   `json:"onboarding_complete"`
}

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type AuthResult struct {
	Tokens    TokenPair     `json:"tokens"`
	Workspace WorkspaceInfo `json:"workspace"`
}

func (s *AuthService) Signup(ctx context.Context, email, password string) (*AuthResult, error) {
	email = strings.TrimSpace(email)
	if email == "" || !strings.Contains(email, "@") {
		return nil, fmt.Errorf("invalid email")
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("password must be at least 8 characters")
	}

	hash, err := crypto.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	_, err = s.queries.GetUserByEmail(ctx, email)
	if err == nil {
		return nil, ErrEmailAlreadyExists
	}

	user, err := s.queries.CreateUser(ctx, db.CreateUserParams{Email: email, PasswordHash: hash})
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			return nil, ErrEmailAlreadyExists
		}
		return nil, fmt.Errorf("create user: %w", err)
	}

	s.grantAdminIfConfigured(ctx, user.ID, email)
	workspace, err := s.queries.CreateWorkspace(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	s.ensureAgencyForWorkspace(ctx, user.ID, workspace.ID)

	tokens, err := s.issueTokens(ctx, user.ID, workspace.ID)
	if err != nil {
		return nil, err
	}

	return &AuthResult{
		Tokens: *tokens,
		Workspace: WorkspaceInfo{
			ID:                 formatUUID(workspace.ID),
			OnboardingComplete: workspace.OnboardingComplete,
		},
	}, nil
}

func (s *AuthService) Login(ctx context.Context, email, password string) (*AuthResult, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, ErrInvalidCredentials
	}

	user, err := s.queries.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	if err := crypto.CheckPassword(user.PasswordHash, password); err != nil {
		return nil, ErrInvalidCredentials
	}

	workspace, err := s.queries.GetWorkspaceByUserID(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("get workspace: %w", err)
	}

	tokens, err := s.issueTokens(ctx, user.ID, workspace.ID)
	if err != nil {
		return nil, err
	}

	return &AuthResult{
		Tokens: *tokens,
		Workspace: WorkspaceInfo{
			ID:                 formatUUID(workspace.ID),
			OnboardingComplete: workspace.OnboardingComplete,
		},
	}, nil
}

func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	hash := hashToken(refreshToken)
	session, err := s.queries.GetSessionByRefreshHash(ctx, hash)
	if err != nil {
		return nil, ErrInvalidRefresh
	}

	if session.ExpiresAt.Valid && session.ExpiresAt.Time.Before(time.Now()) {
		s.queries.DeleteSession(ctx, session.ID)
		return nil, ErrSessionExpired
	}

	workspaceID := session.WorkspaceID
	if !workspaceID.Valid {
		workspace, err := s.queries.GetWorkspaceByUserID(ctx, session.UserID)
		if err != nil {
			return nil, fmt.Errorf("get workspace: %w", err)
		}
		workspaceID = workspace.ID
	}

	s.queries.DeleteSession(ctx, session.ID)
	tokens, err := s.issueTokens(ctx, session.UserID, workspaceID)
	if err != nil {
		return nil, err
	}
	return tokens, nil
}

func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	hash := hashToken(refreshToken)
	session, err := s.queries.GetSessionByRefreshHash(ctx, hash)
	if err != nil {
		return nil
	}
	return s.queries.DeleteSession(ctx, session.ID)
}

func (s *AuthService) GoogleCallback(ctx context.Context, code, clientID, clientSecret, redirectURI string) (*AuthResult, error) {
	email, err := exchangeGoogleCode(ctx, code, clientID, clientSecret, redirectURI)
	if err != nil {
		return nil, ErrInvalidGoogleToken
	}

	user, err := s.queries.GetUserByEmail(ctx, email)
	if err != nil {
		user, err = s.queries.CreateUser(ctx, db.CreateUserParams{
			Email:        email,
			PasswordHash: "google:" + email,
		})
		if err != nil {
			return nil, fmt.Errorf("create user: %w", err)
		}
		s.grantAdminIfConfigured(ctx, user.ID, email)
	}

	workspace, err := s.queries.GetWorkspaceByUserID(ctx, user.ID)
	if err != nil {
		workspace, err = s.queries.CreateWorkspace(ctx, user.ID)
		if err != nil {
			return nil, fmt.Errorf("create workspace: %w", err)
		}
	}
	if !workspace.AgencyID.Valid {
		s.ensureAgencyForWorkspace(ctx, user.ID, workspace.ID)
	}

	tokens, err := s.issueTokens(ctx, user.ID, workspace.ID)
	if err != nil {
		return nil, err
	}

	return &AuthResult{
		Tokens: *tokens,
		Workspace: WorkspaceInfo{
			ID:                 formatUUID(workspace.ID),
			OnboardingComplete: workspace.OnboardingComplete,
		},
	}, nil
}

func (s *AuthService) grantAdminIfConfigured(ctx context.Context, userID pgtype.UUID, email string) {
	email = strings.TrimSpace(strings.ToLower(email))
	if _, ok := s.adminEmails[email]; !ok {
		return
	}
	if _, err := s.queries.UpsertPlatformAdmin(ctx, db.UpsertPlatformAdminParams{UserID: userID}); err != nil {
		slog.Error("grant bootstrap platform admin failed", "email", email, "error", err)
	}
}

func (s *AuthService) ensureAgencyForWorkspace(ctx context.Context, userID, workspaceID pgtype.UUID) {
	ws, err := s.queries.GetWorkspaceByID(ctx, workspaceID)
	if err != nil || ws.AgencyID.Valid {
		return
	}
	agency, err := s.queries.CreateAgency(ctx, db.CreateAgencyParams{
		OwnerUserID: userID,
		Name:        "My Agency",
	})
	if err != nil {
		slog.Error("create agency for workspace failed", "workspace", formatUUID(workspaceID), "error", err)
		return
	}
	if _, err := s.queries.AddAgencyMember(ctx, db.AddAgencyMemberParams{
		AgencyID: agency.ID,
		UserID:   userID,
		Role:     AgencyRoleOwner,
	}); err != nil {
		slog.Error("add agency owner failed", "agency", formatUUID(agency.ID), "error", err)
		return
	}
	if _, err := s.queries.SetWorkspaceAgency(ctx, db.SetWorkspaceAgencyParams{
		ID:       workspaceID,
		AgencyID: agency.ID,
	}); err != nil {
		slog.Error("link workspace to agency failed", "workspace", formatUUID(workspaceID), "error", err)
	}
}

func exchangeGoogleCode(ctx context.Context, code, clientID, clientSecret, redirectURI string) (string, error) {
	data := url.Values{
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	}

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", data)
	if err != nil {
		return "", fmt.Errorf("google token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("google token exchange failed %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode google response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("google error: %s", result.Error)
	}
	if result.IDToken == "" {
		return "", fmt.Errorf("google response missing id_token")
	}

	email, err := verifyGoogleToken(result.IDToken)
	if err != nil {
		return "", err
	}
	return email, nil
}

func (s *AuthService) issueTokens(ctx context.Context, userID pgtype.UUID, workspaceID pgtype.UUID) (*TokenPair, error) {
	accessToken, err := crypto.SignAccessToken(formatUUID(userID), formatUUID(workspaceID), s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	refreshRaw, refreshHash, err := generateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	expiresAt := pgtype.Timestamptz{Time: time.Now().Add(7 * 24 * time.Hour), Valid: true}
	_, err = s.queries.CreateSession(ctx, db.CreateSessionParams{
		UserID:           userID,
		WorkspaceID:      workspaceID,
		RefreshTokenHash: refreshHash,
		ExpiresAt:        expiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	return &TokenPair{AccessToken: accessToken, RefreshToken: refreshRaw}, nil
}

func generateRefreshToken() (raw string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.URLEncoding.EncodeToString(b)
	hash = hashToken(raw)
	return raw, hash, nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func formatUUID(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func verifyGoogleToken(idToken string) (string, error) {
	url := "https://oauth2.googleapis.com/tokeninfo?id_token=" + idToken
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("google tokeninfo request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google tokeninfo returned %d", resp.StatusCode)
	}

	var result struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode google response: %w", err)
	}
	if result.Email == "" {
		return "", fmt.Errorf("google token missing email")
	}
	return result.Email, nil
}
