package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOffHoursHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("no off-hours config returns enabled=false", func(t *testing.T) {
		tmpDir := t.TempDir()
		configDir := filepath.Join(tmpDir, "sidekick")
		require.NoError(t, os.MkdirAll(configDir, 0755))

		configPath := filepath.Join(configDir, "config.yaml")
		configYAML := `
providers: []
`
		require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0644))

		t.Setenv("XDG_CONFIG_HOME", tmpDir)
		t.Setenv("XDG_CONFIG_DIRS", tmpDir)
		xdg.Reload()

		apiCtrl := NewMockController(t)
		router := DefineRoutes(apiCtrl)

		req, _ := http.NewRequest("GET", "/api/v1/off_hours", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var response OffHoursResponse
		err := json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.False(t, response.Enabled)
		assert.False(t, response.Blocked)
		assert.Empty(t, response.Message)
		assert.Empty(t, response.Windows)
	})

	t.Run("no config file returns enabled=false", func(t *testing.T) {
		tmpDir := t.TempDir()
		configDir := filepath.Join(tmpDir, "sidekick")
		require.NoError(t, os.MkdirAll(configDir, 0755))

		t.Setenv("XDG_CONFIG_HOME", tmpDir)
		t.Setenv("XDG_CONFIG_DIRS", tmpDir)
		xdg.Reload()

		apiCtrl := NewMockController(t)
		router := DefineRoutes(apiCtrl)

		req, _ := http.NewRequest("GET", "/api/v1/off_hours", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var response OffHoursResponse
		err := json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.False(t, response.Enabled)
	})

	t.Run("off-hours config with windows returns enabled=true", func(t *testing.T) {
		tmpDir := t.TempDir()
		configDir := filepath.Join(tmpDir, "sidekick")
		require.NoError(t, os.MkdirAll(configDir, 0755))

		configPath := filepath.Join(configDir, "config.yaml")
		configYAML := `
off_hours:
  message: "Go to sleep!"
  windows:
    - start: "03:00"
      end: "07:00"
    - days: ["saturday", "sunday"]
      start: "02:00"
      end: "08:00"
`
		require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0644))

		t.Setenv("XDG_CONFIG_HOME", tmpDir)
		t.Setenv("XDG_CONFIG_DIRS", tmpDir)
		xdg.Reload()

		apiCtrl := NewMockController(t)
		router := DefineRoutes(apiCtrl)

		req, _ := http.NewRequest("GET", "/api/v1/off_hours", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var response OffHoursResponse
		err := json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.True(t, response.Enabled)
		assert.Len(t, response.Windows, 2)
		assert.Equal(t, "03:00", response.Windows[0].Start)
		assert.Equal(t, "07:00", response.Windows[0].End)
		assert.Empty(t, response.Windows[0].Days)
		assert.Equal(t, []string{"saturday", "sunday"}, response.Windows[1].Days)
		assert.Equal(t, "02:00", response.Windows[1].Start)
		assert.Equal(t, "08:00", response.Windows[1].End)
	})

	t.Run("off-hours config with empty windows returns enabled=false", func(t *testing.T) {
		tmpDir := t.TempDir()
		configDir := filepath.Join(tmpDir, "sidekick")
		require.NoError(t, os.MkdirAll(configDir, 0755))

		configPath := filepath.Join(configDir, "config.yaml")
		configYAML := `
off_hours:
  message: "This message won't show"
  windows: []
`
		require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0644))

		t.Setenv("XDG_CONFIG_HOME", tmpDir)
		t.Setenv("XDG_CONFIG_DIRS", tmpDir)
		xdg.Reload()

		apiCtrl := NewMockController(t)
		router := DefineRoutes(apiCtrl)

		req, _ := http.NewRequest("GET", "/api/v1/off_hours", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var response OffHoursResponse
		err := json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.False(t, response.Enabled)
	})

	t.Run("response includes blocked status and message from evaluator", func(t *testing.T) {
		tmpDir := t.TempDir()
		configDir := filepath.Join(tmpDir, "sidekick")
		require.NoError(t, os.MkdirAll(configDir, 0755))

		configPath := filepath.Join(configDir, "config.yaml")
		configYAML := `
off_hours:
  message: "Custom blocking message"
  windows:
    - start: "00:00"
      end: "23:59"
`
		require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0644))

		t.Setenv("XDG_CONFIG_HOME", tmpDir)
		t.Setenv("XDG_CONFIG_DIRS", tmpDir)
		xdg.Reload()

		apiCtrl := NewMockController(t)
		router := DefineRoutes(apiCtrl)

		req, _ := http.NewRequest("GET", "/api/v1/off_hours", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var response OffHoursResponse
		err := json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.True(t, response.Enabled)
		assert.True(t, response.Blocked)
		assert.Equal(t, "Custom blocking message", response.Message)
		assert.NotNil(t, response.UnblockAt)
	})

	t.Run("default message when not configured", func(t *testing.T) {
		tmpDir := t.TempDir()
		configDir := filepath.Join(tmpDir, "sidekick")
		require.NoError(t, os.MkdirAll(configDir, 0755))

		configPath := filepath.Join(configDir, "config.yaml")
		configYAML := `
off_hours:
  windows:
    - start: "00:00"
      end: "23:59"
`
		require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0644))

		t.Setenv("XDG_CONFIG_HOME", tmpDir)
		t.Setenv("XDG_CONFIG_DIRS", tmpDir)
		xdg.Reload()

		apiCtrl := NewMockController(t)
		router := DefineRoutes(apiCtrl)

		req, _ := http.NewRequest("GET", "/api/v1/off_hours", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var response OffHoursResponse
		err := json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.True(t, response.Enabled)
		assert.True(t, response.Blocked)
		assert.Equal(t, "Time to rest!", response.Message)
	})
}
