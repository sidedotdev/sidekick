package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sidekick/domain"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAllowedOrigins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantOrigins []string
		wantErr     bool
		errContains string
	}{
		{
			name:        "empty string",
			input:       "",
			wantOrigins: []string{},
			wantErr:     false,
		},
		{
			name:        "single valid origin",
			input:       "http://localhost:8855",
			wantOrigins: []string{"http://localhost:8855"},
			wantErr:     false,
		},
		{
			name:        "multiple valid origins",
			input:       "http://localhost:8855,https://example.com",
			wantOrigins: []string{"http://localhost:8855", "https://example.com"},
			wantErr:     false,
		},
		{
			name:        "origins with whitespace",
			input:       " http://localhost:8855 , https://example.com ",
			wantOrigins: []string{"http://localhost:8855", "https://example.com"},
			wantErr:     false,
		},
		{
			name:        "origin with trailing slash rejected",
			input:       "http://localhost:8855/",
			wantErr:     true,
			errContains: "must not have path",
		},
		{
			name:        "missing scheme",
			input:       "localhost:8855",
			wantErr:     true,
			errContains: "must have scheme and host",
		},
		{
			name:        "origin with path",
			input:       "http://localhost:8855/api",
			wantErr:     true,
			errContains: "must not have path",
		},
		{
			name:        "origin with query",
			input:       "http://localhost:8855?foo=bar",
			wantErr:     true,
			errContains: "must not have query",
		},
		{
			name:        "origin with fragment",
			input:       "http://localhost:8855#section",
			wantErr:     true,
			errContains: "must not have fragment",
		},
		{
			name:        "IPv6 origin",
			input:       "http://[::1]:8855",
			wantOrigins: []string{"http://[::1]:8855"},
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ao, err := ParseAllowedOrigins(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
			for _, origin := range tt.wantOrigins {
				assert.True(t, ao.IsAllowed(origin), "expected %s to be allowed", origin)
			}
		})
	}
}

func TestAllowedOrigins_IsAllowed(t *testing.T) {
	t.Parallel()

	ao := &AllowedOrigins{
		origins: map[string]struct{}{
			"http://localhost:8855": {},
			"http://127.0.0.1:8855": {},
			"https://example.com":   {},
		},
	}

	tests := []struct {
		name   string
		origin string
		want   bool
	}{
		{"empty origin allowed", "", true},
		{"allowed localhost", "http://localhost:8855", true},
		{"allowed 127.0.0.1", "http://127.0.0.1:8855", true},
		{"allowed https", "https://example.com", true},
		{"disallowed different port", "http://localhost:9999", false},
		{"disallowed different host", "http://evil.com", false},
		{"disallowed different scheme", "https://localhost:8855", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ao.IsAllowed(tt.origin)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildDefaultAllowedOrigins(t *testing.T) {
	t.Run("default origins include server port", func(t *testing.T) {
		ao := BuildDefaultAllowedOrigins()
		assert.True(t, ao.IsAllowed("http://localhost:8855"))
		assert.True(t, ao.IsAllowed("http://127.0.0.1:8855"))
		assert.True(t, ao.IsAllowed("http://[::1]:8855"))
	})

	t.Run("development mode includes vite origins", func(t *testing.T) {
		t.Setenv("SIDE_APP_ENV", "development")
		ao := BuildDefaultAllowedOrigins()
		assert.True(t, ao.IsAllowed("http://localhost:5173"))
		assert.True(t, ao.IsAllowed("http://127.0.0.1:5173"))
	})

	t.Run("non-development mode excludes vite origins", func(t *testing.T) {
		t.Setenv("SIDE_APP_ENV", "production")
		ao := BuildDefaultAllowedOrigins()
		assert.False(t, ao.IsAllowed("http://localhost:5173"))
		assert.False(t, ao.IsAllowed("http://127.0.0.1:5173"))
	})
}

func TestGetAllowedOrigins(t *testing.T) {
	t.Run("uses SIDE_ALLOWED_ORIGINS when set", func(t *testing.T) {
		t.Setenv("SIDE_ALLOWED_ORIGINS", "http://custom.example.com")
		ao, err := GetAllowedOrigins()
		require.NoError(t, err)
		assert.True(t, ao.IsAllowed("http://custom.example.com"))
		assert.False(t, ao.IsAllowed("http://localhost:8855"))
	})

	t.Run("returns error for invalid SIDE_ALLOWED_ORIGINS", func(t *testing.T) {
		t.Setenv("SIDE_ALLOWED_ORIGINS", "not-a-valid-origin")
		_, err := GetAllowedOrigins()
		require.Error(t, err)
	})

	t.Run("uses defaults when SIDE_ALLOWED_ORIGINS unset", func(t *testing.T) {
		os.Unsetenv("SIDE_ALLOWED_ORIGINS")
		ao, err := GetAllowedOrigins()
		require.NoError(t, err)
		assert.True(t, ao.IsAllowed("http://localhost:8855"))
	})
}

func TestCORSMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	allowedOrigins := &AllowedOrigins{
		origins: map[string]struct{}{
			"http://localhost:8855": {},
		},
	}

	t.Run("request without Origin header passes through", func(t *testing.T) {
		t.Parallel()
		r := gin.New()
		r.Use(CORSMiddleware(allowedOrigins))
		r.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("request with allowed Origin gets CORS headers", func(t *testing.T) {
		t.Parallel()
		r := gin.New()
		r.Use(CORSMiddleware(allowedOrigins))
		r.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", "http://localhost:8855")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "http://localhost:8855", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "Origin", w.Header().Get("Vary"))
		assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	})

	t.Run("request with disallowed Origin returns 403", func(t *testing.T) {
		t.Parallel()
		r := gin.New()
		r.Use(CORSMiddleware(allowedOrigins))
		r.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", "http://evil.com")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("OPTIONS preflight with allowed Origin returns 204", func(t *testing.T) {
		t.Parallel()
		r := gin.New()
		r.Use(CORSMiddleware(allowedOrigins))
		r.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		req := httptest.NewRequest("OPTIONS", "/test", nil)
		req.Header.Set("Origin", "http://localhost:8855")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Equal(t, "http://localhost:8855", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "GET")
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "POST")
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Content-Type")
		assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Authorization")
		assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	})

	t.Run("OPTIONS preflight with disallowed Origin returns 403", func(t *testing.T) {
		t.Parallel()
		r := gin.New()
		r.Use(CORSMiddleware(allowedOrigins))
		r.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		req := httptest.NewRequest("OPTIONS", "/test", nil)
		req.Header.Set("Origin", "http://evil.com")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

func TestCheckWebSocketOrigin(t *testing.T) {
	t.Parallel()

	allowedOrigins := &AllowedOrigins{
		origins: map[string]struct{}{
			"http://localhost:8855": {},
		},
	}

	checkFn := CheckWebSocketOrigin(allowedOrigins)

	t.Run("allowed origin returns true", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest("GET", "/ws", nil)
		req.Header.Set("Origin", "http://localhost:8855")
		assert.True(t, checkFn(req))
	})

	t.Run("disallowed origin returns false", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest("GET", "/ws", nil)
		req.Header.Set("Origin", "http://evil.com")
		assert.False(t, checkFn(req))
	})

	t.Run("no origin returns true", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest("GET", "/ws", nil)
		assert.True(t, checkFn(req))
	})
}

func TestWebSocketOriginEnforcement(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctrl := NewMockController(t)
	allowedOrigins := &AllowedOrigins{
		origins: map[string]struct{}{
			"http://localhost:8855": {},
		},
	}
	router := DefineRoutes(ctrl, allowedOrigins)

	// Create test workspace and flow for websocket endpoint
	ctx := t.Context()
	workspace := domain.Workspace{Id: "test-ws"}
	err := ctrl.service.PersistWorkspace(ctx, workspace)
	require.NoError(t, err)
	flow := domain.Flow{Id: "test-flow", WorkspaceId: "test-ws"}
	err = ctrl.service.PersistFlow(ctx, flow)
	require.NoError(t, err)

	s := httptest.NewServer(router)
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http") + "/ws/v1/workspaces/test-ws/flows/test-flow/action_changes_ws"

	t.Run("websocket upgrade succeeds with allowed origin", func(t *testing.T) {
		header := http.Header{}
		header.Set("Origin", "http://localhost:8855")
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
		require.NoError(t, err)
		defer conn.Close()
		assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	})

	t.Run("websocket upgrade fails with disallowed origin", func(t *testing.T) {
		header := http.Header{}
		header.Set("Origin", "http://evil.com")
		_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
		require.Error(t, err)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("websocket upgrade succeeds without origin header", func(t *testing.T) {
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer conn.Close()
		assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	})
}
