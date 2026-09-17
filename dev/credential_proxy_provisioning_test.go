package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/common"
	"sidekick/credential_proxy/server"
	"sidekick/env"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCredentialProxyProvisioningCache(t *testing.T) {
	t.Parallel()

	now := time.Now()
	expiry := now.Add(time.Hour)
	var calls atomic.Int32
	provisioner := newCredentialProxyProvisioner(func(ctx context.Context, request server.ActivationRequest) (server.Activation, error) {
		n := calls.Add(1)
		require.NotEmpty(t, request.FlowID)
		token := "first-token"
		if n > 1 {
			token = "next-token"
		}
		return server.Activation{
			Token: token,
			Proxies: map[string]server.Binding{
				"test": {Upstream: "https://example.com/v1"},
			},
			ExpiresAt: expiry,
		}, nil
	})
	provisioner.now = func() time.Time { return now }
	config, err := json.Marshal(credentialProxyProvisioningConfig{
		Port: 1234,
		Definitions: map[string]common.CredentialProxyConfig{
			"test": {
				Upstream: "https://example.com/v1",
				Auth: common.CredentialProxyAuthConfig{
					Bearer: &common.CredentialSource{Env: "FLOW_TEST_TOKEN"},
				},
			},
		},
		Active: []string{"test"},
	})
	require.NoError(t, err)
	request := env.CommandProvisioning{Kind: "credential-proxy", Scope: "flow", Config: config}
	first, err := provisioner.Provision(context.Background(), request)
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())

	now = now.Add(59 * time.Minute)
	reused, err := provisioner.Provision(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, first, reused)
	require.EqualValues(t, 1, calls.Load())

	now = now.Add(time.Minute)
	expiry = now.Add(time.Hour)
	replaced, err := provisioner.Provision(context.Background(), request)
	require.NoError(t, err)
	require.NotEqual(t, first.Sensitive, replaced.Sensitive)
	require.EqualValues(t, 2, calls.Load())

	request.Scope = "another-flow"
	_, err = provisioner.Provision(context.Background(), request)
	require.NoError(t, err)
	require.EqualValues(t, 3, calls.Load())
}

func TestCredentialProxyProvisioningRejectsExpiredLease(t *testing.T) {
	t.Parallel()

	provisioner := newCredentialProxyProvisioner(func(context.Context, server.ActivationRequest) (server.Activation, error) {
		return server.Activation{Token: "expired", ExpiresAt: time.Now().Add(-time.Second)}, nil
	})
	config, err := json.Marshal(credentialProxyProvisioningConfig{
		Port: 1234,
		Definitions: map[string]common.CredentialProxyConfig{
			"test": {
				Upstream: "https://example.com/v1",
				Auth: common.CredentialProxyAuthConfig{
					Bearer: &common.CredentialSource{Env: "FLOW_TEST_TOKEN"},
				},
			},
		},
		Active: []string{"test"},
	})
	require.NoError(t, err)
	_, err = provisioner.Provision(context.Background(), env.CommandProvisioning{
		Kind: "credential-proxy", Scope: "flow", Config: config,
	})
	require.Error(t, err)
}

func TestCredentialProxyProvisioningRecoversLostLease(t *testing.T) {
	t.Parallel()

	var generation int
	provisioner := newCredentialProxyProvisioner(func(context.Context, server.ActivationRequest) (server.Activation, error) {
		generation++
		return server.Activation{
			Token:     fmt.Sprintf("token-%d", generation),
			ExpiresAt: time.Now().Add(time.Hour),
			Proxies:   map[string]server.Binding{"test": {Upstream: "https://example.com"}},
		}, nil
	})
	valid := true
	provisioner.renew = func(context.Context, string) (time.Time, error) {
		if !valid {
			return time.Time{}, errors.New("capability unavailable")
		}
		return time.Now().Add(time.Hour), nil
	}
	config, err := json.Marshal(credentialProxyProvisioningConfig{
		Port: 1234,
		Definitions: map[string]common.CredentialProxyConfig{
			"test": {
				Upstream: "https://example.com",
				Auth: common.CredentialProxyAuthConfig{
					Bearer: &common.CredentialSource{Env: "FLOW_TEST_TOKEN"},
				},
			},
		},
		Active: []string{"test"},
	})
	require.NoError(t, err)
	request := env.CommandProvisioning{Scope: "flow", Config: config}
	first, err := provisioner.Provision(context.Background(), request)
	require.NoError(t, err)
	reused, err := provisioner.Provision(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, first.Sensitive, reused.Sensitive)
	require.Equal(t, 1, generation)

	valid = false
	recovered, err := provisioner.Provision(context.Background(), request)
	require.NoError(t, err)
	require.NotEqual(t, first.Sensitive, recovered.Sensitive)
	require.Equal(t, 2, generation)
}

func TestCredentialProxyRenewalUpdatesCachedExpiry(t *testing.T) {
	t.Parallel()
	now := time.Now()
	originalExpiry := now.Add(time.Hour)
	calls := 0
	provisioner := newCredentialProxyProvisioner(func(context.Context, server.ActivationRequest) (server.Activation, error) {
		calls++
		return server.Activation{
			Token: "token", ExpiresAt: originalExpiry,
			Proxies: map[string]server.Binding{"test": {Upstream: "https://example.com"}},
		}, nil
	})
	provisioner.now = func() time.Time { return now }
	provisioner.renew = func(context.Context, string) (time.Time, error) {
		return now.Add(time.Hour), nil
	}
	config, err := json.Marshal(credentialProxyProvisioningConfig{
		Port: 1234,
		Definitions: map[string]common.CredentialProxyConfig{
			"test": {
				Upstream: "https://example.com",
				Auth: common.CredentialProxyAuthConfig{
					Bearer: &common.CredentialSource{Env: "FLOW_TEST_TOKEN"},
				},
			},
		},
		Active: []string{"test"},
	})
	require.NoError(t, err)
	request := env.CommandProvisioning{Scope: "flow", Config: config}
	first, err := provisioner.Provision(context.Background(), request)
	require.NoError(t, err)
	now = originalExpiry.Add(-time.Second)
	for key := range provisioner.leases {
		require.NoError(t, provisioner.maintainLease(context.Background(), key, first.Sensitive[0]))
		require.Equal(t, now.Add(time.Hour), provisioner.leases[key].expiresAt)
	}
	now = originalExpiry.Add(time.Second)
	reused, err := provisioner.Provision(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, first.Sensitive, reused.Sensitive)
	require.Equal(t, 1, calls)
}
