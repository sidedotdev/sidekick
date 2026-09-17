package dev

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"sidekick/common"
	"sidekick/credential_proxy"
	"sidekick/credential_proxy/server"
	"sidekick/env"

	"go.temporal.io/sdk/workflow"
)

type credentialProxyProvisioningConfig struct {
	Port        int                                     `json:"port"`
	Definitions map[string]common.CredentialProxyConfig `json:"definitions"`
	Active      []string                                `json:"active"`
}

type credentialProxyLease struct {
	values    env.CommandEnvironment
	expiresAt time.Time
}

type credentialProxyProvisioner struct {
	gate     chan struct{}
	leases   map[[32]byte]credentialProxyLease
	now      func() time.Time
	activate func(context.Context, server.ActivationRequest) (server.Activation, error)
	renew    func(context.Context, string) (time.Time, error)
}

func newCredentialProxyProvisioner(activate func(context.Context, server.ActivationRequest) (server.Activation, error)) *credentialProxyProvisioner {
	return &credentialProxyProvisioner{
		gate: make(chan struct{}, 1), leases: make(map[[32]byte]credentialProxyLease),
		now: time.Now, activate: activate,
	}
}

func NewCommandActivities() *env.CommandActivities {
	provisioner := newCredentialProxyProvisioner(func(ctx context.Context, request server.ActivationRequest) (server.Activation, error) {
		socket, err := server.ControlSocketPath()
		if err != nil {
			return server.Activation{}, errors.New("credential proxy control unavailable")
		}
		return server.RequestCapability(ctx, socket, request)
	})
	provisioner.renew = func(ctx context.Context, token string) (time.Time, error) {
		socket, err := server.ControlSocketPath()
		if err != nil {
			return time.Time{}, errors.New("credential proxy control unavailable")
		}
		return server.RenewCapabilityExpiry(ctx, socket, token)
	}
	return &env.CommandActivities{
		Provisioners: map[string]env.CommandProvisioner{
			"credential-proxy": provisioner.Provision,
		},
	}
}

func (p *credentialProxyProvisioner) Provision(ctx context.Context, request env.CommandProvisioning) (env.CommandEnvironment, error) {
	if request.Scope == "" {
		return env.CommandEnvironment{}, errors.New("credential proxy requires a flow scope")
	}
	var config credentialProxyProvisioningConfig
	if err := json.Unmarshal(request.Config, &config); err != nil {
		return env.CommandEnvironment{}, errors.New("invalid credential proxy provisioning")
	}
	active := make([]string, 0, len(config.Active))
	for _, name := range config.Active {
		definition, exists := config.Definitions[name]
		if !exists || definition.Validate() != nil {
			return env.CommandEnvironment{}, errors.New("invalid credential proxy definition")
		}
		if definition.Auth.Side != nil && !credential_proxy.SideSourcesAllowed() {
			continue
		}
		active = append(active, name)
	}
	if len(active) == 0 {
		return env.CommandEnvironment{}, nil
	}
	if config.Port <= 0 || config.Port > 65535 {
		return env.CommandEnvironment{}, errors.New("credential proxy requires a configured reverse forward")
	}
	config.Active = active
	keyData, err := json.Marshal(struct {
		Scope  string
		Config credentialProxyProvisioningConfig
	}{request.Scope, config})
	if err != nil {
		return env.CommandEnvironment{}, errors.New("invalid credential proxy provisioning")
	}
	key := sha256.Sum256(keyData)
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return env.CommandEnvironment{}, ctx.Err()
	}
	for existing, lease := range p.leases {
		if !p.now().Before(lease.expiresAt) {
			delete(p.leases, existing)
		}
	}
	if lease, ok := p.leases[key]; ok {
		if p.renew == nil {
			return p.commandEnvironment(key, lease.values), nil
		}
		if expiry, err := p.renewLease(ctx, lease.values.Sensitive[0]); err == nil {
			lease.expiresAt = expiry
			p.leases[key] = lease
			return p.commandEnvironment(key, lease.values), nil
		}
		delete(p.leases, key)
	}
	controlCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	activation, err := p.activate(controlCtx, server.ActivationRequest{
		FlowID: request.Scope, Definitions: config.Definitions, Active: active,
	})
	if err != nil {
		return env.CommandEnvironment{}, err
	}
	if activation.Token == "" || !p.now().Before(activation.ExpiresAt) {
		return env.CommandEnvironment{}, errors.New("credential proxy returned an expired or invalid lease")
	}
	proxies := make(map[string]credential_proxy.ProvisionedProxy, len(active))
	for _, name := range active {
		binding, ok := activation.Proxies[name]
		if !ok {
			return env.CommandEnvironment{}, errors.New("credential proxy activation is missing a requested proxy")
		}
		proxies[name] = credential_proxy.ProvisionedProxy{
			URL:      fmt.Sprintf("http://127.0.0.1:%d%s%s", config.Port, credential_proxy.PathPrefix, name),
			Upstream: binding.Upstream, Side: binding.Side,
		}
	}
	data, err := json.Marshal(proxies)
	if err != nil {
		return env.CommandEnvironment{}, errors.New("invalid credential proxy endpoints")
	}
	values := env.CommandEnvironment{
		EnvVars: []string{
			credential_proxy.ProxiesEnvVar + "=" + string(data),
			credential_proxy.TokenEnvVar + "=" + activation.Token,
		},
		Sensitive: []string{activation.Token},
	}
	p.leases[key] = credentialProxyLease{values: values, expiresAt: activation.ExpiresAt}
	return p.commandEnvironment(key, values), nil
}

func cloneCommandEnvironment(values env.CommandEnvironment) env.CommandEnvironment {
	return env.CommandEnvironment{
		EnvVars:   append([]string(nil), values.EnvVars...),
		Sensitive: append([]string(nil), values.Sensitive...),
	}
}

func commandProvisioning(dCtx DevContext, envType env.EnvType) *env.CommandProvisioning {
	active := activeCredentialProxies(envType, dCtx.RepoConfig)
	if len(active) == 0 {
		return nil
	}
	port := 0
	for _, forward := range dCtx.RepoConfig.PortForwards {
		if forward.HostPort == server.HostPort {
			port = forward.ContainerPortOrDefault()
			break
		}
	}
	data, err := json.Marshal(credentialProxyProvisioningConfig{
		Port: port, Definitions: dCtx.RepoConfig.CredentialProxies, Active: active,
	})
	if err != nil {
		panic("invalid credential proxy configuration")
	}
	scope, err := json.Marshal([]string{
		dCtx.WorkspaceId, workflow.GetInfo(dCtx).WorkflowExecution.ID,
	})
	if err != nil {
		panic("invalid flow scope")
	}
	return &env.CommandProvisioning{Kind: "credential-proxy", Scope: string(scope), Config: data}
}

func (p *credentialProxyProvisioner) renewLease(ctx context.Context, token string) (time.Time, error) {
	controlCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	expiry, err := p.renew(controlCtx, token)
	if err != nil {
		return time.Time{}, err
	}
	if !p.now().Before(expiry) {
		return time.Time{}, errors.New("credential proxy returned an expired lease")
	}
	return expiry, nil
}

func (p *credentialProxyProvisioner) commandEnvironment(key [32]byte, values env.CommandEnvironment) env.CommandEnvironment {
	result := cloneCommandEnvironment(values)
	if p.renew != nil {
		token := values.Sensitive[0]
		result.KeepAlive = func(ctx context.Context) error {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					if err := p.maintainLease(ctx, key, token); err != nil {
						if ctx.Err() != nil {
							return ctx.Err()
						}
						return errors.New("credential proxy access could not be renewed")
					}
				}
			}
		}
	}
	return result
}

func (p *credentialProxyProvisioner) maintainLease(ctx context.Context, key [32]byte, token string) error {
	expiry, err := p.renewLease(ctx, token)
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if lease, ok := p.leases[key]; ok && lease.values.Sensitive[0] == token {
		if err != nil {
			delete(p.leases, key)
		} else if expiry.After(lease.expiresAt) {
			lease.expiresAt = expiry
			p.leases[key] = lease
		}
	}
	return err
}
