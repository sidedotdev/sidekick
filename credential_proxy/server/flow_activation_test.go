package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/credential_proxy"

	"github.com/stretchr/testify/require"
)

func TestFlowActivationLifecycle(t *testing.T) {
	t.Parallel()

	now := time.Now()
	capabilities := NewCapabilities()
	capabilities.now = func() time.Time { return now }
	definitions := map[string]common.CredentialProxyConfig{
		"test": {
			Upstream: "https://example.com/v1",
			Auth: common.CredentialProxyAuthConfig{
				Bearer: &common.CredentialSource{Env: "FLOW_TEST_TOKEN"},
			},
		},
	}
	active := []string{"test"}

	first, err := capabilities.ActivateFlow("flow-a", definitions, definitions, active, true)
	require.NoError(t, err)
	require.NotEmpty(t, first.Token)

	now = now.Add(30 * time.Minute)
	reused, err := capabilities.ActivateFlow("flow-a", definitions, definitions, active, true)
	require.NoError(t, err)
	require.Equal(t, first.Token, reused.Token)
	require.Equal(t, first.Proxies, reused.Proxies)
	require.Equal(t, now.Add(time.Hour), reused.ExpiresAt)

	other, err := capabilities.ActivateFlow("flow-b", definitions, definitions, active, true)
	require.NoError(t, err)
	require.NotEqual(t, first.Token, other.Token)

	require.NoError(t, capabilities.RenewAuthorized(first.Token, definitions, true))
	now = now.Add(31 * time.Minute)
	require.NoError(t, capabilities.RenewAuthorized(first.Token, definitions, true))

	now = now.Add(time.Hour)
	require.Error(t, capabilities.RenewAuthorized(first.Token, definitions, true))
	replacement, err := capabilities.ActivateFlow("flow-a", definitions, definitions, active, true)
	require.NoError(t, err)
	require.NotEqual(t, first.Token, replacement.Token)
}

func TestFlowActivationConfigurationIsolation(t *testing.T) {
	t.Parallel()

	capabilities := NewCapabilities()
	auth := common.CredentialProxyAuthConfig{
		Bearer: &common.CredentialSource{Env: "FLOW_TEST_TOKEN"},
	}
	definitions := map[string]common.CredentialProxyConfig{
		"first":  {Upstream: "https://example.com/first", Auth: auth},
		"second": {Upstream: "https://example.com/second", Auth: auth},
	}
	first, err := capabilities.ActivateFlow("flow", definitions, definitions, []string{"first"}, true)
	require.NoError(t, err)
	second, err := capabilities.ActivateFlow("flow", definitions, definitions, []string{"second"}, true)
	require.NoError(t, err)
	require.NotEqual(t, first.Token, second.Token)
	require.Contains(t, first.Proxies, "first")
	require.NotContains(t, first.Proxies, "second")
	require.Contains(t, second.Proxies, "second")
	require.NotContains(t, second.Proxies, "first")
}

func TestFlowActivationConcurrentReuse(t *testing.T) {
	t.Parallel()

	capabilities := NewCapabilities()
	definitions := map[string]common.CredentialProxyConfig{
		"test": {
			Upstream: "https://example.com/v1",
			Auth: common.CredentialProxyAuthConfig{
				Bearer: &common.CredentialSource{Env: "FLOW_TEST_TOKEN"},
			},
		},
	}
	const count = 16
	activations := make([]Activation, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range activations {
		wg.Add(1)
		go func() {
			defer wg.Done()
			activations[i], errs[i] = capabilities.ActivateFlow(
				"flow", definitions, definitions, []string{"test"}, true,
			)
		}()
	}
	wg.Wait()
	for i := range activations {
		require.NoError(t, errs[i])
		require.NotEmpty(t, activations[i].Token)
		require.Equal(t, activations[0].Token, activations[i].Token)
		require.Equal(t, activations[0].Proxies, activations[i].Proxies)
	}
}

func TestFlowInferenceAcrossOriginalExpiry(t *testing.T) {
	t.Setenv("FLOW_TEST_TOKEN", "upstream-only")
	now := time.Now()
	capabilities := NewCapabilities()
	capabilities.now = func() time.Time { return now }
	calls := 0
	capabilities.transport = configuredRouteTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "Bearer upstream-only", r.Header.Get("Authorization"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"usage\":{\"total_tokens\":3}}\n\n")),
		}, nil
	})
	definitions := map[string]common.CredentialProxyConfig{
		"test": {
			Upstream: "https://example.com/v1",
			Auth: common.CredentialProxyAuthConfig{
				Bearer: &common.CredentialSource{Env: "FLOW_TEST_TOKEN"},
			},
		},
	}
	first, err := capabilities.ActivateFlow("flow", definitions, definitions, []string{"test"}, true)
	require.NoError(t, err)
	now = first.ExpiresAt.Add(-time.Second)
	reused, err := capabilities.ActivateFlow("flow", definitions, definitions, []string{"test"}, true)
	require.NoError(t, err)
	require.Equal(t, first.Token, reused.Token)
	require.True(t, reused.ExpiresAt.After(first.ExpiresAt))
	infer := func(expected int) {
		request := httptest.NewRequest(http.MethodPost, credential_proxy.PathPrefix+"test/responses", strings.NewReader(`{"input":"hello"}`))
		request.Header.Set("Authorization", "Bearer "+reused.Token)
		response := httptest.NewRecorder()
		capabilities.ServeHTTP(response, request)
		require.Equal(t, expected, response.Code)
		if expected == http.StatusOK {
			require.Contains(t, response.Body.String(), `"total_tokens":3`)
		}
	}
	now = first.ExpiresAt.Add(time.Second)
	infer(http.StatusOK)
	now = reused.ExpiresAt.Add(-time.Second)
	require.NoError(t, capabilities.RenewAuthorized(reused.Token, definitions, true))
	now = reused.ExpiresAt.Add(time.Second)
	infer(http.StatusOK)
	now = now.Add(time.Hour)
	infer(http.StatusUnauthorized)
	require.Equal(t, 2, calls)
}
