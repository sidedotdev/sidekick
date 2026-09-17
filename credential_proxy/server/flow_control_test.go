package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"sidekick/common"
)

type disconnectedActivationWriter struct {
	header http.Header
}

func (w *disconnectedActivationWriter) Header() http.Header { return w.header }
func (w *disconnectedActivationWriter) WriteHeader(int)     {}
func (w *disconnectedActivationWriter) Write([]byte) (int, error) {
	return 0, errors.New("caller disconnected")
}

func TestFlowActivationDisconnectedCallerPreservesLease(t *testing.T) {
	t.Parallel()

	definitions := map[string]common.CredentialProxyConfig{
		"test": {
			Upstream: "https://example.com/v1",
			Auth: common.CredentialProxyAuthConfig{
				Bearer: &common.CredentialSource{Env: "FLOW_TEST_TOKEN"},
			},
		},
	}
	capabilities := NewCapabilities()
	handler := NewControlHandler(capabilities, func() (map[string]common.CredentialProxyConfig, error) {
		return definitions, nil
	}, func() bool { return true })
	body, err := json.Marshal(ActivationRequest{
		FlowID: "flow", Definitions: definitions, Active: []string{"test"},
	})
	require.NoError(t, err)
	activate := func(w http.ResponseWriter) {
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/activate", bytes.NewReader(body)))
	}
	first := httptest.NewRecorder()
	activate(first)
	require.Equal(t, http.StatusOK, first.Code)
	var original Activation
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &original))

	activate(&disconnectedActivationWriter{header: make(http.Header)})

	next := httptest.NewRecorder()
	activate(next)
	require.Equal(t, http.StatusOK, next.Code)
	var reused Activation
	require.NoError(t, json.Unmarshal(next.Body.Bytes(), &reused))
	require.Equal(t, original.Token, reused.Token)
	require.False(t, original.ExpiresAt.IsZero())
	require.False(t, reused.ExpiresAt.Before(original.ExpiresAt))
}
