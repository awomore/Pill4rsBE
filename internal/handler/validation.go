package handler

import (
	"math/big"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// badRequest writes a consistent { "error": "message" } 400 response.
func badRequest(c echo.Context, msg string) error {
	return c.JSON(http.StatusBadRequest, map[string]string{"error": msg})
}

// validWholeNonNegative trims s and reports whether it is a non-negative whole
// number, returning the normalized (trimmed) value.
func validWholeNonNegative(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, 10); !ok {
		return "", false
	}
	if n.Sign() < 0 {
		return "", false
	}
	return s, true
}
