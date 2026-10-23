package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeTicketStatus(t *testing.T) {
	for in, want := range map[string]string{"": TicketStatusOpen, "open": TicketStatusOpen, "PENDING": TicketStatusPending, "resolved": TicketStatusResolved, "closed": TicketStatusClosed} {
		got, err := NormalizeTicketStatus(in)
		if err != nil || got != want {
			t.Errorf("NormalizeTicketStatus(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := NormalizeTicketStatus("reopened"); err == nil {
		t.Error("expected invalid status")
	}
}

func TestNormalizeTicketPriority(t *testing.T) {
	for in, want := range map[string]string{"": TicketPriorityNormal, "low": TicketPriorityLow, "HIGH": TicketPriorityHigh, "urgent": TicketPriorityUrgent} {
		got, err := NormalizeTicketPriority(in)
		if err != nil || got != want {
			t.Errorf("NormalizeTicketPriority(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := NormalizeTicketPriority("critical"); err == nil {
		t.Error("expected invalid priority")
	}
}

func TestCreateTicketValidation(t *testing.T) {
	s := &SupportService{}
	ws, user := uuid.New(), uuid.New()
	cases := []struct{ subject, body, priority string }{
		{"", "body", "normal"},
		{"subject", "", "normal"},
		{"subject", "body", "critical"},
	}
	for _, tc := range cases {
		_, err := s.CreateTicket(context.Background(), ws, user, tc.subject, tc.body, tc.priority)
		var ve ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("expected ValidationError for %+v, got %v", tc, err)
		}
	}
}
