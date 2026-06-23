package ai

import (
	"fmt"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/db"
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
