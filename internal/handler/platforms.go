package handler

import (
	"log/slog"
	"net/http"

	"github.com/awomore/Pill4rsBE/internal/platforms"
	"github.com/labstack/echo/v4"
)

type PlatformsHandler struct{}

func NewPlatformsHandler() *PlatformsHandler {
	return &PlatformsHandler{}
}

func (h *PlatformsHandler) Capabilities(c echo.Context) error {
	caps, err := platforms.Capabilities()
	if err != nil {
		slog.Error("capabilities load failed", "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not load capabilities"})
	}
	return c.JSON(http.StatusOK, caps)
}
