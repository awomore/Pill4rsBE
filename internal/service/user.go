package service

import (
	"context"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/crypto"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
)

// UserService is the signed-in user's profile, sessions, and workspace list.
type UserService struct {
	queries *db.Queries
	admin   *AdminService
}

func NewUserService(queries *db.Queries, admin *AdminService) *UserService {
	return &UserService{queries: queries, admin: admin}
}

type UserProfile struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	IsAdmin   bool   `json:"is_platform_admin"`
	CreatedAt string `json:"created_at"`
}

func (s *UserService) Profile(ctx context.Context, userID uuid.UUID) (UserProfile, error) {
	user, err := s.queries.GetUserByID(ctx, pgUUID(userID))
	if err != nil {
		return UserProfile{}, err
	}
	admin, _ := s.admin.IsAdmin(ctx, userID)
	return UserProfile{
		ID:        formatUUID(user.ID),
		Email:     user.Email,
		IsAdmin:   admin,
		CreatedAt: user.CreatedAt.Time.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

// ChangePassword verifies the current password before setting a new one.
func (s *UserService) ChangePassword(ctx context.Context, userID uuid.UUID, current, next string) error {
	if len(next) < 8 {
		return ValidationError{"new password must be at least 8 characters"}
	}
	user, err := s.queries.GetUserByID(ctx, pgUUID(userID))
	if err != nil {
		return err
	}
	if err := crypto.CheckPassword(user.PasswordHash, current); err != nil {
		return ValidationError{"current password is incorrect"}
	}
	hash, err := crypto.HashPassword(next)
	if err != nil {
		return err
	}
	return s.queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
		ID:           user.ID,
		PasswordHash: hash,
	})
}

func (s *UserService) ListSessions(ctx context.Context, userID uuid.UUID) ([]db.Session, error) {
	return s.queries.ListSessionsByUser(ctx, pgUUID(userID))
}

func (s *UserService) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error {
	return s.queries.DeleteSessionForUser(ctx, db.DeleteSessionForUserParams{
		ID:     pgUUID(sessionID),
		UserID: pgUUID(userID),
	})
}

func (s *UserService) ListWorkspaces(ctx context.Context, userID uuid.UUID) ([]db.Workspace, error) {
	return s.queries.ListWorkspacesForUser(ctx, pgUUID(userID))
}

func validEmail(email string) bool {
	email = strings.TrimSpace(email)
	return strings.Contains(email, "@") && strings.Contains(email, ".")
}
