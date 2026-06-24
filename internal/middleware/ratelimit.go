package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"
)

func RateLimit(requestsPerMinute int) echo.MiddlewareFunc {
	if requestsPerMinute <= 0 {
		requestsPerMinute = 60
	}
	store := echomw.NewRateLimiterMemoryStoreWithConfig(
		echomw.RateLimiterMemoryStoreConfig{
			Rate:      rate.Limit(float64(requestsPerMinute) / 60.0),
			Burst:     requestsPerMinute,
			ExpiresIn: time.Minute,
		},
	)
	return echomw.RateLimiterWithConfig(echomw.RateLimiterConfig{
		Skipper: func(c echo.Context) bool {
			return c.Request().Method == http.MethodOptions ||
				c.Path() == "/health"
		},
		Store: store,
		IdentifierExtractor: func(c echo.Context) (string, error) {
			return c.RealIP(), nil
		},
		DenyHandler: func(c echo.Context, identifier string, err error) error {
			return c.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "too many requests, please try again later",
			})
		},
		ErrorHandler: func(c echo.Context, err error) error {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "rate limiter error",
			})
		},
		BeforeFunc: func(c echo.Context) {
			c.Response().Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
		},
	})
}
