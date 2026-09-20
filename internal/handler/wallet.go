package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// WalletHandler exposes the prepaid wallet: balances, ledger, and top-ups.
type WalletHandler struct {
	wallet *service.WalletService
	guard  *service.SpendGuard
}

func NewWalletHandler(wallet *service.WalletService, guard *service.SpendGuard) *WalletHandler {
	return &WalletHandler{wallet: wallet, guard: guard}
}

// Get returns the wallet summary (all currency balances).
func (h *WalletHandler) Get(c echo.Context) error {
	ws, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	summary, err := h.wallet.Summary(c.Request().Context(), ws)
	if err != nil {
		slog.Error("wallet summary failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to load wallet"})
	}
	return c.JSON(http.StatusOK, summary)
}

// Transactions returns the wallet ledger, newest first.
func (h *WalletHandler) Transactions(c echo.Context) error {
	ws, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	limit := int32(50)
	offset := int32(0)
	if v := c.QueryParam("limit"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil {
			limit = int32(n)
		}
	}
	if v := c.QueryParam("offset"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil {
			offset = int32(n)
		}
	}
	txs, err := h.wallet.ListTransactions(c.Request().Context(), ws, limit, offset)
	if err != nil {
		slog.Error("wallet transactions failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to load transactions"})
	}
	out := make([]map[string]interface{}, 0, len(txs))
	for _, t := range txs {
		m := map[string]interface{}{
			"id":                  formatUUID(t.ID),
			"currency":            t.Currency,
			"kind":                t.Kind,
			"amount_minor":        t.AmountMinor,
			"balance_after_minor": t.BalanceAfterMinor,
			"created_at":          t.CreatedAt.Time,
		}
		if t.SourceType.Valid {
			m["source_type"] = t.SourceType.String
		}
		if t.SourceID.Valid {
			m["source_id"] = t.SourceID.String
		}
		out = append(out, m)
	}
	return c.JSON(http.StatusOK, out)
}

type topUpRequest struct {
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
	Provider    string `json:"provider"`
	Reference   string `json:"reference"`
}

// TopUp credits the wallet from a payment-provider reference. In production the
// provider webhook calls the same service path; this endpoint is for
// provider-confirmed top-ups (and manual credits).
func (h *WalletHandler) TopUp(c echo.Context) error {
	ws, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	var req topUpRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	provider := req.Provider
	if provider == "" {
		provider = "manual"
	}
	if _, err := h.wallet.TopUp(c.Request().Context(), ws, req.Currency, req.AmountMinor, provider, req.Reference); err != nil {
		var ve service.ValidationError
		if errors.As(err, &ve) {
			return badRequest(c, ve.Msg)
		}
		slog.Error("wallet top-up failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to top up wallet"})
	}
	if h.guard != nil {
		h.guard.Clear(c.Request().Context(), ws)
	}
	summary, err := h.wallet.Summary(c.Request().Context(), ws)
	if err != nil {
		return c.JSON(http.StatusOK, map[string]string{"status": "credited"})
	}
	return c.JSON(http.StatusOK, summary)
}
