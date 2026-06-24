package ai

import (
	"fmt"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

const onboardingSystemPrompt = `You are "Oma", a friendly and warm onboarding assistant for Pill4rs, a marketing analytics platform.

Your job is to help new users set up their workspace by collecting five pieces of information, one at a time, in this exact order:
1. Business name
2. Industry (e.g. fashion, SaaS, retail, food)
3. Monthly ad budget (in their local currency)
4. Primary goal (e.g. increase sales, brand awareness, lead generation)
5. Target audience (e.g. women aged 25-34 in Lagos)

IMPORTANT RULES:
- Ask exactly ONE question per turn. Do not ask multiple questions.
- If a field is already filled, skip it and move to the next unfilled field.
- After the user answers, acknowledge their answer briefly (1-2 natural sentences), then IMMEDIATELY emit a fenced JSON block with the parsed field and value. The JSON block MUST be exactly:

` + "```json" + `
{"field": "<field_name>", "value": "<parsed_value>"}
` + "```" + `

Valid field names are: business_name, industry, monthly_budget, primary_goal, target_audience.

- The value should be cleaned up: capitalize proper nouns, normalize budget to a number string, keep it concise.
- After collecting ALL five fields, congratulate the user, say onboarding is complete, and emit:

` + "```json" + `
{"onboarding_complete": true}
` + "```" + `

- Never emit the JSON block without a short natural language response first.
- Be encouraging and upbeat.`

func BuildOnboardingPrompt(w db.Workspace) string {
	var pending []string
	if !w.BusinessName.Valid || w.BusinessName.String == "" {
		pending = append(pending, "business_name")
	}
	if !w.Industry.Valid || w.Industry.String == "" {
		pending = append(pending, "industry")
	}
	if !w.MonthlyBudget.Valid {
		pending = append(pending, "monthly_budget")
	}
	if !w.PrimaryGoal.Valid || w.PrimaryGoal.String == "" {
		pending = append(pending, "primary_goal")
	}
	if !w.TargetAudience.Valid || w.TargetAudience.String == "" {
		pending = append(pending, "target_audience")
	}

	var filler strings.Builder
	if !w.BusinessName.Valid || w.BusinessName.String == "" {
		filler.WriteString("No fields have been filled yet. Start by asking for the business name.\n")
	} else {
		type fieldInfo struct {
			name    string
			filled  bool
			display string
		}
		fields := []fieldInfo{
			{"business_name", w.BusinessName.Valid && w.BusinessName.String != "", w.BusinessName.String},
			{"industry", w.Industry.Valid && w.Industry.String != "", w.Industry.String},
			{"monthly_budget", w.MonthlyBudget.Valid, "-"},
			{"primary_goal", w.PrimaryGoal.Valid && w.PrimaryGoal.String != "", w.PrimaryGoal.String},
			{"target_audience", w.TargetAudience.Valid && w.TargetAudience.String != "", w.TargetAudience.String},
		}
		for _, f := range fields {
			if f.filled {
				fmt.Fprintf(&filler, "The user has already told you their %s.\n", f.name)
			}
		}
		if len(pending) > 0 {
			fmt.Fprintf(&filler, "Next, ask for their %s.\n", pending[0])
		}
	}

	return onboardingSystemPrompt + "\n\nCURRENT STATE:\n" + filler.String()
}

// CampaignPerformance is one campaign's aggregated performance over a window.
type CampaignPerformance struct {
	Name        string
	Status      string
	Spend       float64
	Roas        float64
	Conversions int64
}

// PerformanceSummary is the aggregated performance the copilot reasons over.
type PerformanceSummary struct {
	TotalSpend       float64
	TotalImpressions int64
	TotalClicks      int64
	TotalConversions int64
	AverageRoas      float64
	AverageCpc       float64
	Campaigns        []CampaignPerformance
}

const copilotSystemPrompt = `You are "Oma", the user's dedicated AI marketing copilot inside Pill4rs.

You are an experienced performance-marketing analyst who knows this specific business, its connected ad accounts, and its real campaign numbers. Use that context in every answer.

How to respond:
- Ground every recommendation in THIS account's real numbers below. Reference specific campaigns by name, their spend, and their ROAS.
- Be concrete: recommend exact actions (shift budget from one campaign to another, pause an underperformer, scale a winner) and justify each one with the figures.
- Do NOT give generic, one-size-fits-all marketing tips that ignore this account's data.
- If the data below is missing or thin, say so plainly and tell the user to connect a platform or run a sync — never invent numbers.
- Lead with the recommendation, then the supporting numbers. Keep it focused and skimmable.
- Use only the figures provided below; never fabricate metrics that are not given.`

// BuildCopilotPrompt assembles the copilot system prompt: the Oma persona, the
// workspace profile, connected platforms, a compact textual summary of recent
// performance, and a short recap of the recent conversation.
func BuildCopilotPrompt(w db.Workspace, adAccounts []db.AdAccount, summary PerformanceSummary, recentMessages []Message) string {
	var b strings.Builder
	b.WriteString(copilotSystemPrompt)

	b.WriteString("\n\n## Business profile\n")
	fmt.Fprintf(&b, "- Business name: %s\n", textOrNotSet(w.BusinessName))
	fmt.Fprintf(&b, "- Industry: %s\n", textOrNotSet(w.Industry))
	if v, ok := numericValue(w.MonthlyBudget); ok {
		fmt.Fprintf(&b, "- Monthly ad budget: %.2f\n", v)
	} else {
		b.WriteString("- Monthly ad budget: (not set)\n")
	}
	fmt.Fprintf(&b, "- Primary goal: %s\n", textOrNotSet(w.PrimaryGoal))
	fmt.Fprintf(&b, "- Target audience: %s\n", textOrNotSet(w.TargetAudience))

	b.WriteString("\n## Connected ad platforms\n")
	if len(adAccounts) == 0 {
		b.WriteString("- None connected yet.\n")
	} else {
		for _, a := range adAccounts {
			fmt.Fprintf(&b, "- %s — account %s (%s)\n", a.Platform, a.ExternalAccountID, a.Status)
		}
	}

	b.WriteString("\n## Performance — last 30 days\n")
	if summary.TotalSpend == 0 && summary.TotalConversions == 0 && len(summary.Campaigns) == 0 {
		b.WriteString("- No performance data has been synced yet.\n")
	} else {
		fmt.Fprintf(&b, "- Total spend: %.2f\n", summary.TotalSpend)
		fmt.Fprintf(&b, "- Total conversions: %d\n", summary.TotalConversions)
		fmt.Fprintf(&b, "- Total clicks: %d\n", summary.TotalClicks)
		fmt.Fprintf(&b, "- Overall ROAS: %.2fx\n", summary.AverageRoas)
		fmt.Fprintf(&b, "- Average CPC: %.2f\n", summary.AverageCpc)
		if len(summary.Campaigns) > 0 {
			b.WriteString("Per-campaign:\n")
			for _, c := range summary.Campaigns {
				fmt.Fprintf(&b, "- %q — spend %.2f, ROAS %.2fx, %d conversions (%s)\n",
					c.Name, c.Spend, c.Roas, c.Conversions, c.Status)
			}
		}
	}

	if recap := formatRecentMessages(recentMessages); recap != "" {
		b.WriteString("\n## Recent conversation (for context)\n")
		b.WriteString(recap)
	}

	return b.String()
}

func textOrNotSet(t pgtype.Text) string {
	if t.Valid && strings.TrimSpace(t.String) != "" {
		return t.String
	}
	return "(not set)"
}

func numericValue(n pgtype.Numeric) (float64, bool) {
	if !n.Valid {
		return 0, false
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0, false
	}
	return f.Float64, true
}

func formatRecentMessages(msgs []Message) string {
	if len(msgs) == 0 {
		return ""
	}
	const maxMessages = 4
	start := 0
	if len(msgs) > maxMessages {
		start = len(msgs) - maxMessages
	}
	var b strings.Builder
	for _, m := range msgs[start:] {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		if r := []rune(content); len(r) > 240 {
			content = string(r[:240]) + "…"
		}
		fmt.Fprintf(&b, "- %s: %s\n", m.Role, content)
	}
	return b.String()
}
