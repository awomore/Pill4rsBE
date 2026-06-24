package ai

import (
	"strings"
	"testing"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }

func numeric(t *testing.T, s string) pgtype.Numeric {
	t.Helper()
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		t.Fatalf("scan numeric %q: %v", s, err)
	}
	return n
}

func TestBuildCopilotPromptIncludesContext(t *testing.T) {
	ws := db.Workspace{
		BusinessName:   text("Acme Fashion"),
		Industry:       text("fashion"),
		MonthlyBudget:  numeric(t, "50000"),
		PrimaryGoal:    text("increase sales"),
		TargetAudience: text("women 25-34 in Lagos"),
	}
	adAccounts := []db.AdAccount{
		{Platform: "meta", ExternalAccountID: "act_123456", Status: "active"},
	}
	summary := PerformanceSummary{
		TotalSpend:       1234.56,
		TotalClicks:      500,
		TotalConversions: 42,
		AverageRoas:      3.2,
		AverageCpc:       0.49,
		Campaigns: []CampaignPerformance{
			{Name: "Spring Sale", Status: "ACTIVE", Spend: 800.00, Roas: 4.1, Conversions: 30},
			{Name: "Retargeting", Status: "PAUSED", Spend: 434.56, Roas: 1.2, Conversions: 12},
		},
	}
	recent := []Message{
		{Role: "user", Content: "How are my campaigns doing?"},
	}

	got := BuildCopilotPrompt(ws, adAccounts, summary, recent)

	mustContain := []string{
		"Oma",                     // persona
		"Acme Fashion", "fashion", // workspace profile
		"50000.00", // monthly budget
		"increase sales", "women 25-34 in Lagos",
		"meta", "act_123456", "active", // connected platform
		"1234.56",              // total spend
		"3.20x",                // overall ROAS
		"Spring Sale", "4.10x", // per-campaign spend/ROAS
		"Retargeting", "1.20x",
		"Do NOT give generic",         // numbers-grounded instruction
		"How are my campaigns doing?", // recent conversation recap
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q\n---\n%s", want, got)
		}
	}
}

func TestBuildCopilotPromptEmptyState(t *testing.T) {
	got := BuildCopilotPrompt(db.Workspace{}, nil, PerformanceSummary{}, nil)

	mustContain := []string{
		"Oma",
		"(not set)",
		"None connected yet.",
		"No performance data has been synced yet.",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("empty-state prompt missing %q\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "Recent conversation") {
		t.Errorf("empty-state prompt should not include a recent conversation section\n%s", got)
	}
}

func TestFormatRecentMessagesTruncates(t *testing.T) {
	long := strings.Repeat("x", 500)
	out := formatRecentMessages([]Message{{Role: "user", Content: long}})
	if !strings.Contains(out, "…") {
		t.Error("expected long message to be truncated with ellipsis")
	}
	if len([]rune(out)) > 300 {
		t.Errorf("expected truncated output, got %d runes", len([]rune(out)))
	}
}

func TestFormatRecentMessagesKeepsLastFew(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "two"},
		{Role: "user", Content: "three"},
		{Role: "assistant", Content: "four"},
		{Role: "user", Content: "five"},
	}
	out := formatRecentMessages(msgs)
	if strings.Contains(out, "one") {
		t.Errorf("expected oldest message to be dropped\n%s", out)
	}
	if !strings.Contains(out, "five") {
		t.Errorf("expected most recent message to be kept\n%s", out)
	}
}
