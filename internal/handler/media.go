package handler

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/awomore/Pill4rsBE/internal/service"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// MediaHandler manages creative media (images/videos) for ad delivery.
type MediaHandler struct {
	svc *service.MediaService
}

func NewMediaHandler(svc *service.MediaService) *MediaHandler {
	return &MediaHandler{svc: svc}
}

// Upload accepts a multipart "file" field (image or video) and stores it,
// returning the asset with its public URL for use as creative media.
func (h *MediaHandler) Upload(c echo.Context) error {
	ws, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}

	file, err := c.FormFile("file")
	if err != nil {
		return badRequest(c, "a file field is required")
	}
	src, err := file.Open()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to read upload"})
	}
	defer src.Close()

	data, err := io.ReadAll(src)
	if err != nil {
		return badRequest(c, "failed to read upload")
	}

	asset, err := h.svc.Upload(c.Request().Context(), ws, file.Filename, file.Header.Get("Content-Type"), data)
	if err != nil {
		var ve service.ValidationError
		if errors.As(err, &ve) {
			return badRequest(c, ve.Msg)
		}
		slog.Error("media upload failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to store media"})
	}
	return c.JSON(http.StatusCreated, mediaToMap(asset))
}

// List returns the workspace's media assets.
func (h *MediaHandler) List(c echo.Context) error {
	ws, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	assets, err := h.svc.List(c.Request().Context(), ws)
	if err != nil {
		slog.Error("media list failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list media"})
	}
	out := make([]map[string]interface{}, 0, len(assets))
	for _, a := range assets {
		out = append(out, mediaToMap(a))
	}
	return c.JSON(http.StatusOK, out)
}

// Delete removes a media asset.
func (h *MediaHandler) Delete(c echo.Context) error {
	ws, err := uuid.Parse(workspaceIDOf(c))
	if err != nil {
		return badRequest(c, "invalid workspace id")
	}
	assetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return badRequest(c, "invalid media id")
	}
	if err := h.svc.Delete(c.Request().Context(), ws, assetID); err != nil {
		slog.Error("media delete failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to delete media"})
	}
	return c.NoContent(http.StatusNoContent)
}

func mediaToMap(a db.MediaAsset) map[string]interface{} {
	m := map[string]interface{}{
		"id":         formatUUID(a.ID),
		"kind":       a.Kind,
		"url":        a.PublicUrl,
		"mime":       a.Mime,
		"bytes":      a.Bytes,
		"status":     a.Status,
		"created_at": a.CreatedAt.Time,
	}
	if a.Width.Valid {
		m["width"] = a.Width.Int32
	}
	if a.Height.Valid {
		m["height"] = a.Height.Int32
	}
	if a.DurationMs.Valid {
		m["duration_ms"] = a.DurationMs.Int64
	}
	return m
}
