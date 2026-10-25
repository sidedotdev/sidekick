package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/domain"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProvidersHandlersConfigLoadError(t *testing.T) {
	t.Parallel()

	for _, workspaceScoped := range []bool{false, true} {
		name := "profile"
		if workspaceScoped {
			name = "workspace"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctrl := newLocalConfigController(t, common.LocalConfig{}, errors.New("invalid config: invalid provider type: unsupported"))
			ctrl.secretManager = testSecretManager{secrets: map[string]string{
				"OPENAI_API_KEY": "test-key",
			}}
			path := "/api/v1/providers"
			if workspaceScoped {
				workspace := domain.Workspace{Id: "ws_provider_error", Name: "Provider error"}
				require.NoError(t, ctrl.service.PersistWorkspace(t.Context(), workspace))
				path = "/api/v1/workspaces/" + workspace.Id + "/providers"
			}

			router := DefineRoutes(ctrl, TestAllowedOrigins())
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))

			require.Equal(t, http.StatusInternalServerError, rr.Code)
			var response struct {
				Error string `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
			require.Contains(t, response.Error, "Failed to load sidekick config")
			require.Contains(t, response.Error, "invalid provider type: unsupported")
		})
	}
}
