package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// SupportHandler serves client support tickets and the admin support console.
type SupportHandler struct {
	svc *service.SupportService
}

func NewSupportHandler(svc *service.SupportService) *SupportHandler {
	return &SupportHandler{svc: svc}
}

type createTicketRequest struct {
	Subject  string `json:"subject"`
	Message  string `json:"message"`
	Priority string `json:"priority"`
}

func (h *SupportHandler) Create(c echo.Context) error {
	ws, user, ok := supportIDs(c)
	if !ok {
		return nil
	}
	var req createTicketRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	ticket, err := h.svc.CreateTicket(c.Request().Context(), ws, user, req.Subject, req.Message, req.Priority)
	if err != nil {
		return supportError(c, err)
	}
	return c.JSON(http.StatusCreated, ticketMap(ticket))
}

func (h *SupportHandler) List(c echo.Context) error {
	ws, _, ok := supportIDs(c)
	if !ok {
		return nil
	}
	limit, offset := pageParams(c)
	rows, err := h.svc.ListWorkspaceTickets(c.Request().Context(), ws, limit, offset)
	if err != nil {
		return supportError(c, err)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, t := range rows {
		out = append(out, ticketMap(t))
	}
	return c.JSON(http.StatusOK, out)
}

func (h *SupportHandler) Get(c echo.Context) error {
	ws, user, ok := supportIDs(c)
	if !ok {
		return nil
	}
	ticketID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid ticket id")
	}
	view, err := h.svc.GetTicket(c.Request().Context(), ticketID, ws, user)
	if err != nil {
		return supportError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"ticket":   ticketMap(view.Ticket),
		"messages": messageMaps(view.Messages),
	})
}

type replyRequest struct {
	Message  string `json:"message"`
	Internal bool   `json:"internal"`
}

func (h *SupportHandler) Reply(c echo.Context) error {
	ws, user, ok := supportIDs(c)
	if !ok {
		return nil
	}
	ticketID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid ticket id")
	}
	var req replyRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	msg, err := h.svc.Reply(c.Request().Context(), ticketID, ws, user, req.Message, req.Internal)
	if err != nil {
		return supportError(c, err)
	}
	return c.JSON(http.StatusCreated, messageMap(msg))
}

func (h *SupportHandler) AdminList(c echo.Context) error {
	limit, offset := pageParams(c)
	rows, err := h.svc.ListAllTickets(c.Request().Context(), c.QueryParam("status"), c.QueryParam("priority"), limit, offset)
	if err != nil {
		return supportError(c, err)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, t := range rows {
		out = append(out, ticketMap(t))
	}
	return c.JSON(http.StatusOK, out)
}

func (h *SupportHandler) AdminGet(c echo.Context) error {
	ws, user, ok := supportIDs(c)
	if !ok {
		return nil
	}
	ticketID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid ticket id")
	}
	view, err := h.svc.GetTicket(c.Request().Context(), ticketID, ws, user)
	if err != nil {
		return supportError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"ticket":   ticketMap(view.Ticket),
		"messages": messageMaps(view.Messages),
	})
}

func (h *SupportHandler) AdminReply(c echo.Context) error {
	ws, user, ok := supportIDs(c)
	if !ok {
		return nil
	}
	ticketID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid ticket id")
	}
	var req replyRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	msg, err := h.svc.Reply(c.Request().Context(), ticketID, ws, user, req.Message, req.Internal)
	if err != nil {
		return supportError(c, err)
	}
	return c.JSON(http.StatusCreated, messageMap(msg))
}

type updateTicketRequest struct {
	Status       string `json:"status"`
	Priority     string `json:"priority"`
	AssignToSelf bool   `json:"assign_to_self"`
}

func (h *SupportHandler) AdminUpdate(c echo.Context) error {
	_, user, ok := supportIDs(c)
	if !ok {
		return nil
	}
	ticketID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid ticket id")
	}
	var req updateTicketRequest
	if err := c.Bind(&req); err != nil {
		return badRequest(c, "invalid request body")
	}
	ticket, err := h.svc.UpdateTicket(c.Request().Context(), user, ticketID, req.Status, req.Priority, req.AssignToSelf)
	if err != nil {
		return supportError(c, err)
	}
	return c.JSON(http.StatusOK, ticketMap(ticket))
}

func (h *SupportHandler) AdminSummary(c echo.Context) error {
	counts, err := h.svc.Summary(c.Request().Context())
	if err != nil {
		return supportError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"counts": counts})
}

func supportIDs(c echo.Context) (uuid.UUID, uuid.UUID, bool) {
	ws, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		_ = badRequest(c, "invalid workspace id")
		return uuid.Nil, uuid.Nil, false
	}
	user, err := uuid.Parse(userIDOf(c))
	if err != nil {
		_ = c.JSON(http.StatusForbidden, map[string]string{"error": "support requires a user session"})
		return uuid.Nil, uuid.Nil, false
	}
	return ws, user, true
}

func pageParams(c echo.Context) (int32, int32) {
	limit, offset := int32(50), int32(0)
	if v := c.QueryParam("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = int32(n)
		}
	}
	if v := c.QueryParam("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			offset = int32(n)
		}
	}
	return limit, offset
}

func ticketMap(t db.SupportTicket) map[string]any {
	return map[string]any{
		"id":           formatUUID(t.ID),
		"workspace_id": formatUUID(t.WorkspaceID),
		"subject":      t.Subject,
		"status":       t.Status,
		"priority":     t.Priority,
		"created_by":   formatUUID(t.CreatedBy),
		"assigned_to":  formatUUID(t.AssignedAdminID),
		"created_at":   t.CreatedAt.Time,
		"updated_at":   t.UpdatedAt.Time,
	}
}

func messageMap(m db.SupportMessage) map[string]any {
	return map[string]any{
		"id":             formatUUID(m.ID),
		"author_type":    m.AuthorType,
		"author_user_id": formatUUID(m.AuthorUserID),
		"body":           m.Body,
		"internal":       m.Internal,
		"created_at":     m.CreatedAt.Time,
	}
}

func messageMaps(msgs []db.SupportMessage) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageMap(m))
	}
	return out
}

func supportError(c echo.Context, err error) error {
	var ve service.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest(c, ve.Msg)
	case errors.Is(err, service.ErrTicketNotFound):
		return c.JSON(http.StatusNotFound, map[string]string{"error": "support ticket not found"})
	default:
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "support request failed"})
	}
}
