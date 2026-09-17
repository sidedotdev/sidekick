package dev

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"sidekick/common"
	"sidekick/credential_proxy"
	"sidekick/credential_proxy/server"
	"sidekick/env"
)

type credentialProxyCommandEnv struct {
	env.Env
	run func(context.Context, env.EnvRunCommandInput) (env.EnvRunCommandOutput, error)
}

func (e *credentialProxyCommandEnv) GetType() env.EnvType {
	return env.EnvTypeModal
}

func (e *credentialProxyCommandEnv) RunCommand(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
	return e.run(ctx, input)
}

func TestCredentialProxyCommandRuntimeLifecycle(t *testing.T) {
	t.Parallel()

	for _, outcome := range []string{"success", "failure", "cancellation"} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()

			token := strings.Repeat("a", 64)
			var grants, revocations atomic.Int32
			socket := filepath.Join(t.TempDir(), "control.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			control := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/activate":
					grants.Add(1)
					_ = json.NewEncoder(w).Encode(server.Activation{
						Token:   token,
						Proxies: map[string]server.Binding{"internal": {Upstream: "https://example.invalid"}},
					})
				case "/revoke":
					if r.Header.Get("Authorization") != "Bearer "+token {
						t.Error("revocation missing capability")
					}
					revocations.Add(1)
					w.WriteHeader(http.StatusNoContent)
				default:
					http.NotFound(w, r)
				}
			})}
			defer control.Close()
			go func() { _ = control.Serve(listener) }()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fake := &credentialProxyCommandEnv{
				run: func(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
					joined := strings.Join(input.EnvVars, "\n")
					if !strings.Contains(joined, token) ||
						!strings.Contains(joined, "http://127.0.0.1:32855/credential-proxies/internal") {
						t.Error("runtime input missing scoped capability")
					}
					if input.Command != "sh" || !strings.Contains(joined, "EXISTING=value") {
						t.Error("original command input was not preserved")
					}
					output := env.EnvRunCommandOutput{Stdout: token, Stderr: token}
					switch outcome {
					case "failure":
						return output, errors.New("command failed: " + token)
					case "cancellation":
						cancel()
						return output, ctx.Err()
					default:
						return output, nil
					}
				},
			}
			config := common.RepoConfig{
				CredentialProxies: map[string]common.CredentialProxyConfig{
					"internal": {
						Upstream: "https://example.invalid",
						Auth: common.CredentialProxyAuthConfig{
							Bearer: &common.CredentialSource{Env: "UNUSED_SECRET"},
						},
					},
				},
				PortForwards: []common.PortForwardConfig{{HostPort: 22855, ContainerPort: 32855}},
			}
			config.ModalConfig.CredentialProxies = []string{"internal"}
			input := env.EnvRunCommandActivityInput{
				EnvContainer: env.EnvContainer{Env: fake},
				Command:      "sh",
				Args:         []string{"-c", "true"},
				EnvVars:      []string{"EXISTING=value"},
			}
			output, err := runCredentialProxyCommand(ctx, input, config, socket)
			if outcome == "success" && err != nil {
				t.Fatal(err)
			}
			if outcome != "success" && err == nil {
				t.Fatal("command error was lost")
			}
			if outcome == "cancellation" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation identity lost: %v", err)
			}
			if strings.Contains(output.Stdout+output.Stderr, token) ||
				(err != nil && strings.Contains(err.Error(), token)) {
				t.Fatal("capability escaped through command result")
			}
			if len(input.EnvVars) != 1 {
				t.Fatal("runtime injection mutated activity input")
			}
			if grants.Load() != 1 || revocations.Load() != 1 {
				t.Fatalf("grants = %d, revocations = %d", grants.Load(), revocations.Load())
			}
		})
	}
}

func TestCredentialProxyCommandWithoutActivationNeedsNoCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")

	called := false
	fake := &credentialProxyCommandEnv{
		run: func(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
			called = true
			if len(input.EnvVars) != 1 || input.EnvVars[0] != "EXISTING=value" {
				t.Fatal("ordinary command environment changed")
			}
			return env.EnvRunCommandOutput{Stdout: "ordinary output"}, nil
		},
	}
	output, err := CredentialProxyCommandActivity(context.Background(), CredentialProxyCommandInput{
		Command: env.EnvRunCommandActivityInput{
			EnvContainer: env.EnvContainer{Env: fake},
			Command:      "true",
			EnvVars:      []string{"EXISTING=value"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called || output.Stdout != "ordinary output" {
		t.Fatal("ordinary command did not run")
	}
}

func TestCredentialProxyCommandSidePolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		appEnv  string
		enabled string
		active  []string
		wantRun bool
	}{
		{"production", "production", "", []string{"api", "subscription"}, true},
		{"explicit disable", "development", "false", []string{"api", "subscription"}, true},
		{"enabled unavailable", "development", "", []string{"api"}, false},
		{"disabled mixed", "development", "false", []string{"api", "generic"}, false},
		{"production generic", "production", "", []string{"generic"}, false},
		{"disabled missing", "development", "false", []string{"missing"}, false},
		{"disabled invalid", "development", "false", []string{"invalid"}, false},
		{"enabled missing", "development", "", []string{"missing"}, false},
		{"enabled invalid", "development", "", []string{"invalid"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SIDE_APP_ENV", tc.appEnv)
			t.Setenv("SIDE_CREDENTIAL_PROXY_SIDE_AUTH_ENABLED", tc.enabled)
			t.Setenv("XDG_CACHE_HOME", t.TempDir())

			var config common.RepoConfig
			if err := json.Unmarshal([]byte(`{
				"credentialProxies": {
					"api": {"auth":{"side":{"provider":"openai","authType":"api"}}},
					"subscription": {"auth":{"side":{"provider":"openai","authType":"subscription"}}},
					"generic": {"upstream":"https://example.invalid","auth":{"bearer":{"env":"UNUSED"}}},
					"invalid": {"auth":{"side":{"provider":"openai","authType":"bogus"}}}
				}
			}`), &config); err != nil {
				t.Fatal(err)
			}
			if len(config.CredentialProxies) != 4 {
				t.Fatal("test definitions were not loaded")
			}
			config.PortForwards = []common.PortForwardConfig{{HostPort: 22855, ContainerPort: 32855}}
			config.ModalConfig.CredentialProxies = tc.active
			before, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			fake := &credentialProxyCommandEnv{
				run: func(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
					called = true
					if input.Command != "true" || len(input.EnvVars) != 1 || input.EnvVars[0] != "EXISTING=value" {
						t.Fatal("ordinary command input changed")
					}
					return env.EnvRunCommandOutput{Stdout: "ordinary output"}, nil
				},
			}
			output, err := CredentialProxyCommandActivity(context.Background(), CredentialProxyCommandInput{
				Command: env.EnvRunCommandActivityInput{
					EnvContainer: env.EnvContainer{Env: fake},
					Command:      "true",
					EnvVars:      []string{"EXISTING=value"},
				},
				RepoConfig: config,
			})
			if called != tc.wantRun {
				t.Fatalf("command ran = %v, want %v; error: %v", called, tc.wantRun, err)
			}
			if tc.wantRun {
				if err != nil || output.Stdout != "ordinary output" {
					t.Fatalf("ordinary command failed: output=%+v error=%v", output, err)
				}
			} else if err == nil {
				t.Fatal("unavailable or invalid proxy did not fail closed")
			}
			after, marshalErr := json.Marshal(config)
			if marshalErr != nil || string(before) != string(after) {
				t.Fatal("activity mutated repository configuration")
			}
		})
	}
}

func TestCredentialProxyCommandDisabledMixedActivation(t *testing.T) {
	t.Setenv("SIDE_APP_ENV", "development")
	t.Setenv("SIDE_CREDENTIAL_PROXY_SIDE_AUTH_ENABLED", "false")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	socket, err := server.ControlSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("b", 64)
	var grants, revocations atomic.Int32
	control := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/activate":
			var request server.ActivationRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if len(request.Active) != 1 || request.Active[0] != "generic" {
				t.Errorf("unexpected activation names: %v", request.Active)
				http.Error(w, "unexpected activation", http.StatusBadRequest)
				return
			}
			grants.Add(1)
			_ = json.NewEncoder(w).Encode(server.Activation{
				Token:   token,
				Proxies: map[string]server.Binding{"generic": {Upstream: "https://example.invalid"}},
			})
		case "/revoke":
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("revocation missing capability")
			}
			revocations.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})}
	defer control.Close()
	go func() { _ = control.Serve(listener) }()

	var config common.RepoConfig
	if err := json.Unmarshal([]byte(`{
		"credentialProxies": {
			"api": {"auth":{"side":{"provider":"openai","authType":"api"}}},
			"generic": {"upstream":"https://example.invalid","auth":{"bearer":{"env":"UNUSED"}}}
		}
	}`), &config); err != nil {
		t.Fatal(err)
	}
	config.ModalConfig.CredentialProxies = []string{"api", "generic"}
	config.PortForwards = []common.PortForwardConfig{{HostPort: 22855, ContainerPort: 32855}}
	called := false
	fake := &credentialProxyCommandEnv{
		run: func(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
			called = true
			vars := make(map[string]string)
			for _, entry := range input.EnvVars {
				key, value, _ := strings.Cut(entry, "=")
				vars[key] = value
			}
			var proxies map[string]credential_proxy.ProvisionedProxy
			if err := json.Unmarshal([]byte(vars[credential_proxy.ProxiesEnvVar]), &proxies); err != nil {
				t.Fatal(err)
			}
			if len(proxies) != 1 || proxies["generic"].URL != "http://127.0.0.1:32855/credential-proxies/generic" ||
				proxies["generic"].Side != nil {
				t.Fatalf("unexpected proxies: %v", proxies)
			}
			if vars[credential_proxy.TokenEnvVar] != token {
				t.Fatal("generic capability missing")
			}
			return env.EnvRunCommandOutput{Stdout: "completed"}, nil
		},
	}
	output, err := CredentialProxyCommandActivity(context.Background(), CredentialProxyCommandInput{
		Command: env.EnvRunCommandActivityInput{
			EnvContainer: env.EnvContainer{Env: fake},
			Command:      "true",
		},
		RepoConfig: config,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called || output.Stdout != "completed" || grants.Load() != 1 || revocations.Load() != 1 {
		t.Fatalf("called=%v grants=%d revocations=%d output=%q", called, grants.Load(), revocations.Load(), output.Stdout)
	}
	if len(config.ModalConfig.CredentialProxies) != 2 || config.ModalConfig.CredentialProxies[0] != "api" {
		t.Fatal("activity mutated original activation slice")
	}
}
