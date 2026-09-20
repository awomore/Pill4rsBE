package handler

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/awomore/Pill4rsBE/internal/config"
	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

// FlutterwaveHandler implements wallet top-ups via Flutterwave Standard
// payments, plus the webhook that credits the wallet. Credits are idempotent
// per Flutterwave transaction id and re-verified server-side.
type FlutterwaveHandler struct {
	cfg     *config.Config
	wallet  *service.WalletService
	guard   *service.SpendGuard
	queries *db.Queries
}

func NewFlutterwaveHandler(cfg *config.Config, wallet *service.WalletService, guard *service.SpendGuard, queries *db.Queries) *FlutterwaveHandler {
	return &FlutterwaveHandler{cfg: cfg, wallet: wallet, guard: guard, queries: queries}
}

type fundRequest struct {
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
	Email       string `json:"email"`
}

// Checkout creates a Flutterwave Standard payment and returns the hosted
// checkout link. The wallet is only credited by the webhook.
func (h *FlutterwaveHandler) Checkout(c echo.Context) error {
	if h.cfg.FlutterwaveSecretKey == "" {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "billing is not configured"})
	}
	ws, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}

	var req fundRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	if currency != service.CurrencyUSD && currency != service.CurrencyNGN {
		return badRequest(c, "currency must be USD or NGN")
	}
	if req.AmountMinor <= 0 {
		return badRequest(c, "amount_minor must be positive")
	}

	email := strings.TrimSpace(req.Email)
	if email == "" {
		email = h.userEmail(c)
	}
	if email == "" {
		return badRequest(c, "an email is required to start a payment")
	}

	txRef := fmt.Sprintf("pill4rs-%s-%d", ws.String(), time.Now().UnixNano())
	payload := map[string]any{
		"tx_ref":          txRef,
		"amount":          strconv.FormatFloat(float64(req.AmountMinor)/100, 'f', 2, 64),
		"currency":        currency,
		"redirect_url":    h.cfg.FrontendOrigin + "/settings/billing?status=complete",
		"payment_options": "card,banktransfer,ussd",
		"customer":        map[string]string{"email": email},
		"customizations":  map[string]string{"title": "Pill4rs wallet top-up"},
		"meta": map[string]string{
			"workspace_id": ws.String(),
			"currency":     currency,
		},
	}
	body, _ := json.Marshal(payload)

	httpReq, err := http.NewRequestWithContext(c.Request().Context(), http.MethodPost,
		h.cfg.FlutterwaveBaseURL+"/payments", bytes.NewReader(body))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to start payment"})
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+h.cfg.FlutterwaveSecretKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		slog.Error("flutterwave checkout failed", "error", err)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to start payment"})
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var out struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Link string `json:"link"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Status != "success" || out.Data.Link == "" {
		slog.Error("flutterwave checkout bad response", "status", resp.StatusCode, "body", string(raw))
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to start payment"})
	}
	return c.JSON(http.StatusOK, map[string]string{"checkout_url": out.Data.Link, "tx_ref": txRef})
}

// Webhook verifies Flutterwave's verif-hash, re-verifies the transaction, and
// credits the wallet. Replays are ignored (idempotent per transaction id).
func (h *FlutterwaveHandler) Webhook(c echo.Context) error {
	if !verifyFlutterwaveHash(c.Request().Header.Get("verif-hash"), h.cfg.FlutterwaveWebhookSecret) {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid signature"})
	}

	raw, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return badRequest(c, "failed to read body")
	}

	var evt struct {
		Event string `json:"event"`
		Data  struct {
			ID     json.Number `json:"id"`
			TxRef  string      `json:"tx_ref"`
			Status string      `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &evt); err != nil {
		return badRequest(c, "invalid event payload")
	}
	if evt.Data.Status != "successful" {
		return c.JSON(http.StatusOK, map[string]string{"status": "ignored"})
	}

	ctx := c.Request().Context()
	txID, _ := evt.Data.ID.Int64()

	// Re-verify server-side and trust Flutterwave's amount, not the payload's.
	tx, err := h.verifyTransaction(ctx, txID)
	if err != nil {
		slog.Error("flutterwave verify failed", "transaction", txID, "error", err)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to verify transaction"})
	}

	eventID := strings.TrimSpace(tx.ID.String())
	if eventID == "" || eventID == "0" {
		eventID = tx.TxRef
	}
	if eventID == "" {
		return badRequest(c, "event has no transaction id")
	}
	if h.wallet.ProviderEventSeen(ctx, eventID) {
		return c.JSON(http.StatusOK, map[string]string{"status": "already processed"})
	}

	meta := parseFlutterwaveMeta(tx.Meta)
	ws, err := workspaceFromMeta(meta)
	if err != nil {
		return badRequest(c, "missing workspace_id metadata")
	}
	currency := meta["currency"]
	if currency == "" {
		currency = strings.ToUpper(tx.Currency)
	}
	amount, _ := tx.Amount.Float64()
	amountMinor := int64(math.Round(amount * 100))
	if amountMinor <= 0 {
		return badRequest(c, "transaction has no amount")
	}

	if _, err := h.wallet.TopUp(ctx, ws, currency, amountMinor, "flutterwave", eventID); err != nil {
		var ve service.ValidationError
		if errors.As(err, &ve) {
			return badRequest(c, ve.Msg)
		}
		slog.Error("flutterwave webhook credit failed", "event", eventID, "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to credit wallet"})
	}
	_ = h.wallet.RecordProviderEvent(ctx, eventID, evt.Event)
	if h.guard != nil {
		h.guard.Clear(ctx, ws)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "credited"})
}

type flutterwaveTransaction struct {
	ID       json.Number     `json:"id"`
	TxRef    string          `json:"tx_ref"`
	Status   string          `json:"status"`
	Amount   json.Number     `json:"amount"`
	Currency string          `json:"currency"`
	Meta     json.RawMessage `json:"meta"`
}

func (h *FlutterwaveHandler) verifyTransaction(ctx context.Context, id int64) (flutterwaveTransaction, error) {
	if id == 0 {
		return flutterwaveTransaction{}, fmt.Errorf("missing transaction id")
	}
	url := fmt.Sprintf("%s/transactions/%d/verify", h.cfg.FlutterwaveBaseURL, id)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return flutterwaveTransaction{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+h.cfg.FlutterwaveSecretKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return flutterwaveTransaction{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var out struct {
		Status string                 `json:"status"`
		Data   flutterwaveTransaction `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return flutterwaveTransaction{}, err
	}
	if out.Status != "success" || out.Data.Status != "successful" {
		return flutterwaveTransaction{}, fmt.Errorf("transaction not successful")
	}
	return out.Data, nil
}

func (h *FlutterwaveHandler) userEmail(c echo.Context) string {
	userID, _ := c.Get("user_id").(string)
	id, err := uuid.Parse(userID)
	if err != nil {
		return ""
	}
	user, err := h.queries.GetUserByID(c.Request().Context(), pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		return ""
	}
	return user.Email
}

// verifyFlutterwaveHash compares the verif-hash header to the configured secret
// hash in constant time.
func verifyFlutterwaveHash(header, secret string) bool {
	if secret == "" || header == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(secret)) == 1
}

// parseFlutterwaveMeta normalizes the `meta` field, which Flutterwave returns
// either as an object or as an array of {metaname, metavalue} entries.
func parseFlutterwaveMeta(raw json.RawMessage) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		for k, v := range obj {
			switch t := v.(type) {
			case string:
				out[k] = t
			case float64:
				out[k] = strconv.FormatFloat(t, 'f', -1, 64)
			default:
				b, _ := json.Marshal(t)
				out[k] = string(b)
			}
		}
		return out
	}
	var arr []struct {
		MetaName  string `json:"metaname"`
		MetaValue string `json:"metavalue"`
	}
	if err := json.Unmarshal(raw, &arr); err == nil {
		for _, m := range arr {
			out[m.MetaName] = m.MetaValue
		}
	}
	return out
}

func workspaceFromMeta(meta map[string]string) (uuid.UUID, error) {
	return uuid.Parse(meta["workspace_id"])
}
