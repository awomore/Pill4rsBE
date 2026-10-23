package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	TicketStatusOpen     = "open"
	TicketStatusPending  = "pending"
	TicketStatusResolved = "resolved"
	TicketStatusClosed   = "closed"

	TicketPriorityLow    = "low"
	TicketPriorityNormal = "normal"
	TicketPriorityHigh   = "high"
	TicketPriorityUrgent = "urgent"
)

var ErrTicketNotFound = errors.New("support ticket not found")

// SupportService manages client support tickets and the Pill4rs admin console.
type SupportService struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

func NewSupportService(pool *pgxpool.Pool, queries *db.Queries) *SupportService {
	return &SupportService{pool: pool, queries: queries}
}

type TicketView struct {
	Ticket   db.SupportTicket
	Messages []db.SupportMessage
}

// CreateTicket opens a ticket and records the first customer message.
func (s *SupportService) CreateTicket(ctx context.Context, workspaceID, userID uuid.UUID, subject, body, priority string) (db.SupportTicket, error) {
	subject = strings.TrimSpace(subject)
	body = strings.TrimSpace(body)
	if subject == "" {
		return db.SupportTicket{}, ValidationError{"subject is required"}
	}
	if len(subject) > 200 {
		return db.SupportTicket{}, ValidationError{"subject is too long (max 200 characters)"}
	}
	if body == "" {
		return db.SupportTicket{}, ValidationError{"message is required"}
	}
	var err error
	priority, err = NormalizeTicketPriority(priority)
	if err != nil {
		return db.SupportTicket{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.SupportTicket{}, fmt.Errorf("begin ticket tx: %w", err)
	}
	defer tx.Rollback(ctx)
	q := s.queries.WithTx(tx)

	ticket, err := q.CreateSupportTicket(ctx, db.CreateSupportTicketParams{
		WorkspaceID: pgUUID(workspaceID),
		CreatedBy:   pgUUID(userID),
		Subject:     subject,
		Priority:    priority,
	})
	if err != nil {
		return db.SupportTicket{}, err
	}
	if _, err := q.CreateSupportMessage(ctx, db.CreateSupportMessageParams{
		TicketID:     ticket.ID,
		AuthorUserID: pgUUID(userID),
		AuthorType:   "user",
		Body:         body,
	}); err != nil {
		return db.SupportTicket{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.SupportTicket{}, err
	}
	return ticket, nil
}

// ListWorkspaceTickets returns the active workspace's tickets.
func (s *SupportService) ListWorkspaceTickets(ctx context.Context, workspaceID uuid.UUID, limit, offset int32) ([]db.SupportTicket, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.queries.ListSupportTicketsByWorkspace(ctx, db.ListSupportTicketsByWorkspaceParams{
		WorkspaceID: pgUUID(workspaceID),
		Limit:       limit,
		Offset:      offset,
	})
}

// GetTicket loads a ticket and its visible messages for a workspace or admin.
func (s *SupportService) GetTicket(ctx context.Context, ticketID, workspaceID, userID uuid.UUID) (TicketView, error) {
	ticket, err := s.queries.GetSupportTicket(ctx, pgUUID(ticketID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TicketView{}, ErrTicketNotFound
		}
		return TicketView{}, err
	}
	admin, _ := s.queries.IsPlatformAdmin(ctx, pgUUID(userID))
	if !admin && formatUUID(ticket.WorkspaceID) != formatUUID(pgUUID(workspaceID)) {
		return TicketView{}, ErrTicketNotFound
	}
	messages, err := s.queries.ListSupportMessages(ctx, db.ListSupportMessagesParams{
		TicketID:        ticket.ID,
		IncludeInternal: admin,
	})
	if err != nil {
		return TicketView{}, err
	}
	return TicketView{Ticket: ticket, Messages: messages}, nil
}

// Reply adds a public reply or an admin-only internal note.
func (s *SupportService) Reply(ctx context.Context, ticketID, workspaceID, userID uuid.UUID, body string, internal bool) (db.SupportMessage, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return db.SupportMessage{}, ValidationError{"message is required"}
	}
	ticket, err := s.queries.GetSupportTicket(ctx, pgUUID(ticketID))
	if err != nil {
		return db.SupportMessage{}, ErrTicketNotFound
	}
	admin, _ := s.queries.IsPlatformAdmin(ctx, pgUUID(userID))
	if !admin && formatUUID(ticket.WorkspaceID) != formatUUID(pgUUID(workspaceID)) {
		return db.SupportMessage{}, ErrTicketNotFound
	}
	if internal && !admin {
		return db.SupportMessage{}, ValidationError{"only support admins can add internal notes"}
	}

	authorType := "user"
	if admin {
		authorType = "admin"
	}
	msg, err := s.queries.CreateSupportMessage(ctx, db.CreateSupportMessageParams{
		TicketID:     ticket.ID,
		AuthorUserID: pgUUID(userID),
		AuthorType:   authorType,
		Body:         body,
		Internal:     internal,
	})
	if err != nil {
		return db.SupportMessage{}, err
	}

	newStatus := ticket.Status
	if !internal {
		if admin {
			newStatus = TicketStatusPending
		} else {
			newStatus = TicketStatusOpen
		}
	}
	_, _ = s.queries.UpdateSupportTicket(ctx, db.UpdateSupportTicketParams{
		ID:              ticket.ID,
		Status:          newStatus,
		Priority:        ticket.Priority,
		AssignedAdminID: ticket.AssignedAdminID,
	})
	if admin {
		s.audit(ctx, userID, "support_reply", formatUUID(ticket.ID), map[string]any{"internal": internal})
	}
	return msg, nil
}

// UpdateTicket changes status, priority, and optionally assigns to the admin.
func (s *SupportService) UpdateTicket(ctx context.Context, adminID, ticketID uuid.UUID, status, priority string, assignToSelf bool) (db.SupportTicket, error) {
	ticket, err := s.queries.GetSupportTicket(ctx, pgUUID(ticketID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.SupportTicket{}, ErrTicketNotFound
		}
		return db.SupportTicket{}, err
	}
	if status == "" {
		status = ticket.Status
	}
	if priority == "" {
		priority = ticket.Priority
	}
	var verr error
	status, verr = NormalizeTicketStatus(status)
	if verr != nil {
		return db.SupportTicket{}, verr
	}
	priority, verr = NormalizeTicketPriority(priority)
	if verr != nil {
		return db.SupportTicket{}, verr
	}
	assigned := ticket.AssignedAdminID
	if assignToSelf {
		assigned = pgUUID(adminID)
	}
	updated, err := s.queries.UpdateSupportTicket(ctx, db.UpdateSupportTicketParams{
		ID:              ticket.ID,
		Status:          status,
		Priority:        priority,
		AssignedAdminID: assigned,
	})
	if err != nil {
		return db.SupportTicket{}, err
	}
	s.audit(ctx, adminID, "support_update", formatUUID(ticket.ID), map[string]any{
		"status": status, "priority": priority, "assigned_to_self": assignToSelf,
	})
	return updated, nil
}

// ListAllTickets is the admin console list.
func (s *SupportService) ListAllTickets(ctx context.Context, status, priority string, limit, offset int32) ([]db.SupportTicket, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if status != "" {
		var err error
		if status, err = NormalizeTicketStatus(status); err != nil {
			return nil, err
		}
	}
	if priority != "" {
		var err error
		if priority, err = NormalizeTicketPriority(priority); err != nil {
			return nil, err
		}
	}
	return s.queries.ListSupportTickets(ctx, db.ListSupportTicketsParams{
		Status: status, Priority: priority, Limit: limit, Offset: offset,
	})
}

// Summary returns counts by status.
func (s *SupportService) Summary(ctx context.Context) (map[string]int64, error) {
	rows, err := s.queries.GetSupportStatusCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{
		TicketStatusOpen: 0, TicketStatusPending: 0, TicketStatusResolved: 0, TicketStatusClosed: 0,
	}
	for _, r := range rows {
		out[r.Status] = r.Count
	}
	return out, nil
}

func NormalizeTicketStatus(status string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "":
		return TicketStatusOpen, nil
	case TicketStatusOpen:
		return TicketStatusOpen, nil
	case TicketStatusPending:
		return TicketStatusPending, nil
	case TicketStatusResolved:
		return TicketStatusResolved, nil
	case TicketStatusClosed:
		return TicketStatusClosed, nil
	default:
		return "", ValidationError{"status must be open, pending, resolved, or closed"}
	}
}

func NormalizeTicketPriority(priority string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "":
		return TicketPriorityNormal, nil
	case TicketPriorityLow:
		return TicketPriorityLow, nil
	case TicketPriorityNormal:
		return TicketPriorityNormal, nil
	case TicketPriorityHigh:
		return TicketPriorityHigh, nil
	case TicketPriorityUrgent:
		return TicketPriorityUrgent, nil
	default:
		return "", ValidationError{"priority must be low, normal, high, or urgent"}
	}
}

func (s *SupportService) audit(ctx context.Context, adminID uuid.UUID, action, ticketID string, metadata map[string]any) {
	var raw []byte
	if len(metadata) > 0 {
		raw, _ = json.Marshal(metadata)
	}
	_, _ = s.queries.InsertAdminAuditLog(ctx, db.InsertAdminAuditLogParams{
		AdminUserID: pgUUID(adminID),
		Action:      action,
		TargetType:  pgtype.Text{String: "support_ticket", Valid: true},
		TargetID:    pgtype.Text{String: ticketID, Valid: true},
		Metadata:    raw,
	})
}
