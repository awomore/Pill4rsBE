package google

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// AccountBudgetProposal is the result of creating/updating an account budget.
type AccountBudgetProposal struct {
	ResourceName string
	Status       string
}

// SetAccountSpendCap proposes an account-level spending limit equal to
// spendCapMinor. Google reviews proposals asynchronously; the returned resource
// name is the proposal, not the final budget.
func (c *Client) SetAccountSpendCap(ctx context.Context, refreshToken, customerID string, spendCapMinor int64) (*AccountBudgetProposal, error) {
	if spendCapMinor < 0 {
		return nil, fmt.Errorf("spend cap must not be negative")
	}
	at, err := c.accessToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	spendMicros := strconv.FormatInt(spendCapMinor*10_000, 10)
	proposal := map[string]any{
		"proposalType":                "UPDATE",
		"proposedSpendingLimitMicros": spendMicros,
	}

	budgetResource, err := c.activeAccountBudget(ctx, at, customerID)
	if err != nil {
		return nil, err
	}
	if budgetResource != "" {
		proposal["accountBudget"] = budgetResource
	} else {
		billingSetup, err := c.approvedBillingSetup(ctx, at, customerID)
		if err != nil {
			return nil, err
		}
		if billingSetup == "" {
			return nil, fmt.Errorf("no approved google ads billing setup is available for this account")
		}
		proposal["billingSetup"] = billingSetup
		proposal["proposalType"] = "CREATE"
		proposal["proposedName"] = "Pill4rs wallet cap"
		proposal["proposedStartTimeType"] = "NOW"
		proposal["proposedEndTimeType"] = "FOREVER"
	}

	body := map[string]any{"operation": map[string]any{"create": proposal}}
	path := fmt.Sprintf("/customers/%s/accountBudgetProposals:mutate", customerID)
	raw, err := c.adsPost(ctx, at, path, body)
	if err != nil {
		return nil, err
	}
	var out struct {
		Result struct {
			ResourceName string `json:"resourceName"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode account budget proposal: %w", err)
	}
	if out.Result.ResourceName == "" {
		return nil, fmt.Errorf("account budget proposal returned no resource name")
	}
	return &AccountBudgetProposal{ResourceName: out.Result.ResourceName}, nil
}

func (c *Client) activeAccountBudget(ctx context.Context, accessToken, customerID string) (string, error) {
	query := "SELECT account_budget.resource_name FROM account_budget WHERE account_budget.status = 'APPROVED' LIMIT 1"
	raw, err := c.search(ctx, accessToken, customerID, query)
	if err != nil {
		return "", err
	}
	var out struct {
		Results []struct {
			AccountBudget struct {
				ResourceName string `json:"resourceName"`
			} `json:"accountBudget"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode active account budget: %w", err)
	}
	if len(out.Results) == 0 {
		return "", nil
	}
	return out.Results[0].AccountBudget.ResourceName, nil
}

func (c *Client) approvedBillingSetup(ctx context.Context, accessToken, customerID string) (string, error) {
	query := "SELECT billing_setup.resource_name FROM billing_setup WHERE billing_setup.status = 'APPROVED' LIMIT 1"
	raw, err := c.search(ctx, accessToken, customerID, query)
	if err != nil {
		return "", err
	}
	var out struct {
		Results []struct {
			BillingSetup struct {
				ResourceName string `json:"resourceName"`
			} `json:"billingSetup"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("decode billing setup: %w", err)
	}
	if len(out.Results) == 0 {
		return "", nil
	}
	return out.Results[0].BillingSetup.ResourceName, nil
}
