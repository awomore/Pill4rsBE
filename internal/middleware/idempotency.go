package middleware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/idempotency"
	"github.com/labstack/echo/v4"
)

const IdempotencyHeader = "Idempotency-Key"

// Idempotency makes mutating API requests safe to retry. Requests that include
// an Idempotency-Key are recorded with a hash of method+path+body; replays
// return the original response instead of executing the handler again.
func Idempotency(store idempotency.Store) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			key := strings.TrimSpace(c.Request().Header.Get(IdempotencyHeader))
			if key == "" || store == nil {
				return next(c)
			}
			if len(key) > 255 {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "idempotency key is too long (max 255 characters)"})
			}

			workspaceID, _ := c.Get("workspace_id").(string)
			if workspaceID == "" {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing workspace"})
			}

			endpoint := c.Request().Method + " " + c.Path()
			body, err := io.ReadAll(c.Request().Body)
			if err != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
			}
			c.Request().Body = io.NopCloser(bytes.NewReader(body))
			hash := requestHash(endpoint, body)

			rec, found, err := store.Get(c.Request().Context(), workspaceID, endpoint, key)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to read idempotency record"})
			}
			if found {
				return replayIdempotent(c, rec, hash)
			}

			started, err := store.Begin(c.Request().Context(), workspaceID, endpoint, key, hash)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create idempotency record"})
			}
			if !started {
				rec, found, err = store.Get(c.Request().Context(), workspaceID, endpoint, key)
				if err != nil || !found {
					return c.JSON(http.StatusConflict, map[string]string{"error": "a request with this idempotency key is already in progress"})
				}
				return replayIdempotent(c, rec, hash)
			}

			capture := &captureWriter{ResponseWriter: c.Response().Writer, status: http.StatusOK}
			c.Response().Writer = capture
			handlerErr := next(c)
			c.Response().Writer = capture.ResponseWriter

			if handlerErr != nil {
				// Let the caller retry with the same key.
				_ = store.Delete(c.Request().Context(), workspaceID, endpoint, key)
				return handlerErr
			}

			// Cache 2xx/4xx responses; 5xx is treated as transient and deletable.
			if capture.status >= 200 && capture.status < 500 {
				_ = store.Complete(c.Request().Context(), workspaceID, endpoint, key, capture.status, capture.body.Bytes())
			} else {
				_ = store.Delete(c.Request().Context(), workspaceID, endpoint, key)
			}
			return nil
		}
	}
}

func replayIdempotent(c echo.Context, rec idempotency.Record, hash string) error {
	if rec.RequestHash != hash {
		return c.JSON(http.StatusUnprocessableEntity, map[string]string{
			"error": "idempotency key was reused with a different request",
		})
	}
	if rec.Status != "completed" {
		return c.JSON(http.StatusConflict, map[string]string{
			"error": "a request with this idempotency key is already in progress",
		})
	}

	c.Response().Header().Set("Idempotency-Replayed", "true")
	if len(rec.ResponseBody) == 0 {
		return c.NoContent(rec.ResponseStatus)
	}
	return c.Blob(rec.ResponseStatus, "application/json; charset=UTF-8", rec.ResponseBody)
}

func requestHash(endpoint string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(endpoint))
	h.Write([]byte{'\n'})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

type captureWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func (w *captureWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(w.status)
	}
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *captureWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
