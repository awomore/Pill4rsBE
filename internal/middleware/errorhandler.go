package middleware

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"
)

// JSONErrorHandler is an echo.HTTPErrorHandler that renders every error as a
// consistent { "error": "message" } JSON body. It covers framework-level errors
// (404, 405, 413, ...) and recovered panics in addition to errors returned by
// handlers, so the API never leaks Echo's default { "message": ... } shape.
func JSONErrorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}

	status := http.StatusInternalServerError
	message := "internal server error"

	var he *echo.HTTPError
	if errors.As(err, &he) {
		status = he.Code
		if msg, ok := he.Message.(string); ok && msg != "" {
			message = msg
		} else if text := http.StatusText(he.Code); text != "" {
			message = text
		}
	}

	if status >= http.StatusInternalServerError {
		slog.LogAttrs(c.Request().Context(), slog.LevelError, "unhandled error",
			slog.String("method", c.Request().Method),
			slog.String("path", c.Request().URL.Path),
			slog.String("error", err.Error()),
		)
		message = "internal server error"
	}

	var writeErr error
	if c.Request().Method == http.MethodHead {
		writeErr = c.NoContent(status)
	} else {
		writeErr = c.JSON(status, map[string]string{"error": message})
	}
	if writeErr != nil {
		slog.Error("failed to write error response", "error", writeErr)
	}
}
