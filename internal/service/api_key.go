package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const apiKeyPrefix = "p4_live_"

// ErrInvalidAPIKey is returned when a presented key is missing, revoked, or
// unknown.
var ErrInvalidAPIKey = errors.New("invalid api key")

// DefaultScopes are granted when a key is created without explicit scopes.
var DefaultScopes = []string{
	"campaigns:read",
	"campaigns:write",
	"accounts:read",
	"accounts:write",
	"reports:read",
	"audiences:read",
	"audiences:write",
	"bidding:read",
	"bidding:write",
}

var allowedScopes = map[string]struct{}{
	"*":               {},
	"campaigns:read":  {},
	"campaigns:write": {},
	"accounts:read":   {},
	"accounts:write":  {},
	"reports:read":    {},
	"audiences:read":  {},
	"audiences:write": {},
	"bidding:read":    {},
	"bidding:write":   {},
}

// APIKeyService issues and verifies workspace-scoped API keys for external
// marketing brands. Only a SHA-256 hash of the key is stored; the plaintext is
// returned exactly once at creation.
type APIKeyService struct {
	queries *db.Queries
}

func NewAPIKeyService(queries *db.Queries) *APIKeyService {
	return &APIKeyService{queries: queries}
}

// Create issues a new key and returns the plaintext key plus its metadata.
func (s *APIKeyService) Create(ctx context.Context, workspaceID uuid.UUID, agencyID *uuid.UUID, name string, scopes []string) (string, db.ApiKey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", db.ApiKey{}, ValidationError{"name is required"}
	}
	if len(name) > 100 {
		return "", db.ApiKey{}, ValidationError{"name is too long (max 100 characters)"}
	}
	normalized, err := NormalizeScopes(scopes)
	if err != nil {
		return "", db.ApiKey{}, err
	}

	raw, prefix, hash, err := GenerateAPIKey()
	if err != nil {
		return "", db.ApiKey{}, err
	}

	var agency pgtype.UUID
	if agencyID != nil {
		agency = pgtype.UUID{Bytes: *agencyID, Valid: true}
	}
	row, err := s.queries.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		WorkspaceID: pgtype.UUID{Bytes: workspaceID, Valid: true},
		AgencyID:    agency,
		Name:        name,
		Prefix:      prefix,
		KeyHash:     hash,
		Scopes:      normalized,
	})
	if err != nil {
		return "", db.ApiKey{}, fmt.Errorf("create api key: %w", err)
	}
	return raw, row, nil
}

// List returns a workspace's keys (hashes included for internal use only; the
// handler never serializes them).
func (s *APIKeyService) List(ctx context.Context, workspaceID uuid.UUID) ([]db.ApiKey, error) {
	return s.queries.ListAPIKeysByWorkspace(ctx, pgtype.UUID{Bytes: workspaceID, Valid: true})
}

// Revoke permanently disables a key.
func (s *APIKeyService) Revoke(ctx context.Context, workspaceID, id uuid.UUID) error {
	return s.queries.RevokeAPIKey(ctx, db.RevokeAPIKeyParams{
		ID:          pgtype.UUID{Bytes: id, Valid: true},
		WorkspaceID: pgtype.UUID{Bytes: workspaceID, Valid: true},
	})
}

// VerifyAPIKey resolves a plaintext key to its workspace and scopes. It is the
// adapter used by the auth middleware.
func (s *APIKeyService) VerifyAPIKey(ctx context.Context, raw string) (string, string, []string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil, ErrInvalidAPIKey
	}
	row, err := s.queries.GetAPIKeyByHash(ctx, HashAPIKey(raw))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", nil, ErrInvalidAPIKey
		}
		return "", "", nil, err
	}
	if row.RevokedAt.Valid {
		return "", "", nil, ErrInvalidAPIKey
	}
	// Best-effort telemetry; never block authentication on it.
	_ = s.queries.TouchAPIKey(ctx, row.ID)
	agencyID := ""
	if row.AgencyID.Valid {
		agencyID = formatUUID(row.AgencyID)
	}
	return formatUUID(row.WorkspaceID), agencyID, row.Scopes, nil
}

// ResolveAgencyClient verifies that a client workspace belongs to the key's
// agency and returns its id. It powers the X-Client-Id header.
func (s *APIKeyService) ResolveAgencyClient(ctx context.Context, agencyID, clientID string) (string, error) {
	aid, err := uuid.Parse(agencyID)
	if err != nil {
		return "", ErrInvalidAPIKey
	}
	cid, err := uuid.Parse(clientID)
	if err != nil {
		return "", ErrInvalidAPIKey
	}
	ws, err := s.queries.GetAgencyWorkspace(ctx, db.GetAgencyWorkspaceParams{
		ID:       pgtype.UUID{Bytes: cid, Valid: true},
		AgencyID: pgtype.UUID{Bytes: aid, Valid: true},
	})
	if err != nil {
		return "", ErrInvalidAPIKey
	}
	return formatUUID(ws.ID), nil
}

// NormalizeScopes validates and dedupes requested scopes.
func NormalizeScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return append([]string(nil), DefaultScopes...), nil
	}
	seen := make(map[string]struct{}, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if scope == "" {
			continue
		}
		if _, ok := allowedScopes[scope]; !ok {
			return nil, ValidationError{"unknown scope: " + scope}
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	if len(out) == 0 {
		return append([]string(nil), DefaultScopes...), nil
	}
	return out, nil
}

// GenerateAPIKey returns a plaintext key, its display prefix, and its hash.
func GenerateAPIKey() (raw, prefix, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("generate api key: %w", err)
	}
	raw = apiKeyPrefix + base64.RawURLEncoding.EncodeToString(buf)
	prefix = raw[:len(apiKeyPrefix)+8]
	return raw, prefix, HashAPIKey(raw), nil
}

// HashAPIKey returns the lowercase hex SHA-256 of a key.
func HashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
