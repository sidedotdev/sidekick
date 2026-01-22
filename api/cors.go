package api

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"sidekick/common"

	"github.com/gin-gonic/gin"
)

// AllowedOrigins holds the parsed set of allowed origins for CORS and websocket checks.
type AllowedOrigins struct {
	origins map[string]struct{}
}

// IsAllowed checks if the given origin is in the allowlist.
// Returns true if origin is empty (non-browser clients) or if it matches an allowed origin.
func (ao *AllowedOrigins) IsAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	_, ok := ao.origins[origin]
	return ok
}

// ParseAllowedOrigins parses and validates a comma-separated list of origins.
// Each origin must be a valid URL with scheme and host, and no path/query/fragment.
func ParseAllowedOrigins(originsStr string) (*AllowedOrigins, error) {
	origins := make(map[string]struct{})
	if originsStr == "" {
		return &AllowedOrigins{origins: origins}, nil
	}

	for _, origin := range strings.Split(originsStr, ",") {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}

		parsed, err := url.Parse(origin)
		if err != nil {
			return nil, fmt.Errorf("invalid origin %q: %w", origin, err)
		}

		if parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("invalid origin %q: must have scheme and host", origin)
		}

		if parsed.Path != "" {
			return nil, fmt.Errorf("invalid origin %q: must not have path", origin)
		}

		if parsed.RawQuery != "" {
			return nil, fmt.Errorf("invalid origin %q: must not have query", origin)
		}

		if parsed.Fragment != "" {
			return nil, fmt.Errorf("invalid origin %q: must not have fragment", origin)
		}

		// Normalize: scheme://host[:port]
		normalized := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
		origins[normalized] = struct{}{}
	}

	return &AllowedOrigins{origins: origins}, nil
}

// BuildDefaultAllowedOrigins constructs the default allowlist based on server port and app environment.
func BuildDefaultAllowedOrigins() *AllowedOrigins {
	port := common.GetServerPort()
	origins := make(map[string]struct{})

	// Default local UI origins
	origins[fmt.Sprintf("http://localhost:%d", port)] = struct{}{}
	origins[fmt.Sprintf("http://127.0.0.1:%d", port)] = struct{}{}
	origins[fmt.Sprintf("http://[::1]:%d", port)] = struct{}{}

	// Vite dev server origins when in development mode
	if os.Getenv("SIDE_APP_ENV") == "development" {
		origins["http://localhost:5173"] = struct{}{}
		origins["http://127.0.0.1:5173"] = struct{}{}
	}

	return &AllowedOrigins{origins: origins}
}

// GetAllowedOrigins returns the configured allowed origins.
// If SIDE_ALLOWED_ORIGINS is set, it parses that; otherwise uses defaults.
// Returns an error if SIDE_ALLOWED_ORIGINS contains invalid entries.
func GetAllowedOrigins() (*AllowedOrigins, error) {
	envOrigins := os.Getenv("SIDE_ALLOWED_ORIGINS")
	if envOrigins != "" {
		return ParseAllowedOrigins(envOrigins)
	}
	return BuildDefaultAllowedOrigins(), nil
}

// CORSMiddleware returns a Gin middleware that enforces Origin allowlist and sets CORS headers.
func CORSMiddleware(allowedOrigins *AllowedOrigins) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		// If Origin header is present, validate it
		if origin != "" {
			if !allowedOrigins.IsAllowed(origin) {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}

			// Set CORS headers for allowed origins
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")

			// Handle preflight requests
			if c.Request.Method == http.MethodOptions {
				c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
				c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type")
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
		}

		c.Next()
	}
}

// CheckWebSocketOrigin returns a function suitable for websocket.Upgrader.CheckOrigin
// that uses the shared allowlist.
func CheckWebSocketOrigin(allowedOrigins *AllowedOrigins) func(r *http.Request) bool {
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		return allowedOrigins.IsAllowed(origin)
	}
}
