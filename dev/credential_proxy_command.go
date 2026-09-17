package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"sidekick/common"
	"sidekick/credential_proxy"
	"sidekick/credential_proxy/server"
	"sidekick/env"
)

type CredentialProxyCommandInput struct {
	Command    env.EnvRunCommandActivityInput `json:"command"`
	RepoConfig common.RepoConfig              `json:"repoConfig"`
}

func CredentialProxyCommandActivity(ctx context.Context, input CredentialProxyCommandInput) (env.EnvRunCommandActivityOutput, error) {
	envType := input.Command.EnvContainer.Env.GetType()
	active := activeCredentialProxies(envType, input.RepoConfig)
	// Process policy belongs in activities, not replayed workflow selection.
	if !credential_proxy.SideSourcesAllowed() {
		filtered := make([]string, 0, len(active))
		for _, name := range active {
			definition, exists := input.RepoConfig.CredentialProxies[name]
			if exists && definition.Auth.Side != nil && definition.Validate() == nil {
				continue
			}
			filtered = append(filtered, name)
		}
		active = filtered
		switch envType {
		case env.EnvTypeModal:
			input.RepoConfig.ModalConfig.CredentialProxies = filtered
		case env.EnvTypeDevPod:
			input.RepoConfig.DevPodConfig.CredentialProxies = filtered
		case env.EnvTypeOpenShell:
			input.RepoConfig.OpenShellConfig.CredentialProxies = filtered
		}
	}
	if len(active) == 0 {
		return env.EnvRunCommandActivity(ctx, input.Command)
	}
	socket, err := server.ControlSocketPath()
	if err != nil {
		return env.EnvRunCommandActivityOutput{}, errors.New("credential proxy control unavailable")
	}
	return runCredentialProxyCommand(ctx, input.Command, input.RepoConfig, socket)
}

func runCredentialProxyCommand(ctx context.Context, input env.EnvRunCommandActivityInput, config common.RepoConfig, socket string) (env.EnvRunCommandActivityOutput, error) {
	active := activeCredentialProxies(input.EnvContainer.Env.GetType(), config)
	if len(active) == 0 {
		return env.EnvRunCommandActivity(ctx, input)
	}
	port := 0
	for _, forward := range config.PortForwards {
		if forward.HostPort == server.HostPort {
			port = forward.ContainerPortOrDefault()
			break
		}
	}
	if port == 0 {
		return env.EnvRunCommandActivityOutput{}, errors.New("credential proxy requires a configured reverse forward")
	}
	activation, err := server.RequestCapability(ctx, socket, server.ActivationRequest{
		Definitions: config.CredentialProxies,
		Active:      active,
	})
	if err != nil {
		return env.EnvRunCommandActivityOutput{}, err
	}
	token := activation.Token
	runCtx, cancel := context.WithCancel(ctx)
	renewed := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				renewed <- nil
				return
			case <-ticker.C:
				if err := server.RenewCapability(runCtx, socket, token); err != nil {
					cancel()
					renewed <- err
					return
				}
			}
		}
	}()
	defer func() {
		cancel()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		// Server-side expiry bounds access if the control channel is unavailable.
		_ = server.RevokeCapability(cleanupCtx, socket, token)
	}()

	proxies := make(map[string]credential_proxy.ProvisionedProxy, len(active))
	for _, name := range active {
		binding, bound := activation.Proxies[name]
		if !bound {
			return env.EnvRunCommandActivityOutput{}, errors.New("credential proxy activation is missing a requested proxy")
		}
		proxies[name] = credential_proxy.ProvisionedProxy{
			URL:      fmt.Sprintf("http://127.0.0.1:%d%s%s", port, credential_proxy.PathPrefix, name),
			Upstream: binding.Upstream,
			Side:     binding.Side,
		}
	}
	data, err := json.Marshal(proxies)
	if err != nil {
		return env.EnvRunCommandActivityOutput{}, errors.New("invalid credential proxy endpoints")
	}
	input.EnvVars = append(append([]string(nil), input.EnvVars...),
		credential_proxy.ProxiesEnvVar+"="+string(data),
		credential_proxy.TokenEnvVar+"="+token,
	)
	output, commandErr := env.EnvRunCommandActivity(runCtx, input)
	cancel()
	renewErr := <-renewed
	output.Stdout = strings.ReplaceAll(output.Stdout, token, "[REDACTED]")
	output.Stderr = strings.ReplaceAll(output.Stderr, token, "[REDACTED]")
	if ctx.Err() != nil {
		return output, ctx.Err()
	}
	if renewErr != nil {
		return output, errors.New("credential proxy renewal failed")
	}
	if commandErr != nil {
		return output, errors.New(strings.ReplaceAll(commandErr.Error(), token, "[REDACTED]"))
	}
	return output, nil
}

func activeCredentialProxies(envType env.EnvType, config common.RepoConfig) []string {
	switch envType {
	case env.EnvTypeModal:
		return config.ModalConfig.CredentialProxies
	case env.EnvTypeDevPod:
		return config.DevPodConfig.CredentialProxies
	case env.EnvTypeOpenShell:
		return config.OpenShellConfig.CredentialProxies
	default:
		return nil
	}
}
