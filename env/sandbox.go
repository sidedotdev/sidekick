package env

import (
	"context"
	"encoding/json"
	"fmt"
)

// SandboxEnv is implemented by env types whose environment lives in a named
// remote sandbox with its own lifecycle (create/check/stop/delete).
type SandboxEnv interface {
	Env
	GetSandboxName() string
}

var (
	_ SandboxEnv = (*DevPodEnv)(nil)
	_ SandboxEnv = (*OpenShellEnv)(nil)
	_ SandboxEnv = (*ModalEnv)(nil)
)

func (e *DevPodEnv) GetSandboxName() string    { return e.WorkspaceName }
func (e *OpenShellEnv) GetSandboxName() string { return e.SandboxName }
func (e *ModalEnv) GetSandboxName() string     { return e.SandboxName }

// SandboxProvider implements sandbox lifecycle management for one env type.
// Providers self-register via RegisterSandboxProvider so that workflow code
// and activity registration only reference the generic
// Create/Check/Stop/DeleteSandboxActivity functions. This registry is the
// seam intended to eventually let env providers be loaded as plugins.
type SandboxProvider interface {
	CreateSandbox(ctx context.Context, input CreateSandboxInput) (CreateSandboxOutput, error)
	CheckSandbox(ctx context.Context, input CheckSandboxInput) (CheckSandboxOutput, error)
	StopSandbox(ctx context.Context, input StopSandboxInput) error
	DeleteSandbox(ctx context.Context, input DeleteSandboxInput) error
}

var sandboxProviders = map[EnvType]SandboxProvider{}

// RegisterSandboxProvider makes a provider available to the generic sandbox
// lifecycle activities for the given env type.
func RegisterSandboxProvider(envType EnvType, provider SandboxProvider) {
	sandboxProviders[envType] = provider
}

func getSandboxProvider(envType EnvType) (SandboxProvider, error) {
	provider, ok := sandboxProviders[envType]
	if !ok {
		return nil, fmt.Errorf("no sandbox provider registered for env type %q", envType)
	}
	return provider, nil
}

type CreateSandboxInput struct {
	EnvType EnvType `json:"envType"`
	// Name requests a specific sandbox name. Providers reuse a live sandbox
	// with the same name instead of failing.
	Name string `json:"name,omitempty"`
	// RepoDir is the local repository the sandbox is being created for.
	RepoDir string `json:"repoDir,omitempty"`
	// Config carries provider-specific configuration as opaque JSON; each
	// provider unmarshals it into its own config type.
	Config json.RawMessage `json:"config,omitempty"`
}

type CreateSandboxOutput struct {
	SandboxName string `json:"sandboxName"`
	// SSHHost and SSHPort are set by providers whose sandboxes are reached
	// via a fixed SSH endpoint resolved at creation time.
	SSHHost string `json:"sshHost,omitempty"`
	SSHPort int    `json:"sshPort,omitempty"`
	Reused  bool   `json:"reused,omitempty"`
}

type CheckSandboxInput struct {
	EnvType     EnvType `json:"envType"`
	SandboxName string  `json:"sandboxName"`
}

type CheckSandboxOutput struct {
	Alive bool `json:"alive"`
	// SSHHost and SSHPort are set (when alive) by providers whose sandboxes
	// are reached via a fixed SSH endpoint.
	SSHHost string `json:"sshHost,omitempty"`
	SSHPort int    `json:"sshPort,omitempty"`
}

type StopSandboxInput struct {
	EnvType     EnvType `json:"envType"`
	SandboxName string  `json:"sandboxName"`
}

type StopSandboxOutput struct{}

type DeleteSandboxInput struct {
	EnvType     EnvType `json:"envType"`
	SandboxName string  `json:"sandboxName"`
}

type DeleteSandboxOutput struct{}

// CreateSandboxActivity creates (or reuses) a sandbox via the provider
// registered for the input's env type.
func CreateSandboxActivity(ctx context.Context, input CreateSandboxInput) (CreateSandboxOutput, error) {
	provider, err := getSandboxProvider(input.EnvType)
	if err != nil {
		return CreateSandboxOutput{}, err
	}
	return provider.CreateSandbox(ctx, input)
}

// CheckSandboxActivity reports whether a named sandbox is alive via the
// provider registered for the input's env type.
func CheckSandboxActivity(ctx context.Context, input CheckSandboxInput) (CheckSandboxOutput, error) {
	provider, err := getSandboxProvider(input.EnvType)
	if err != nil {
		return CheckSandboxOutput{}, err
	}
	return provider.CheckSandbox(ctx, input)
}

// StopSandboxActivity stops a sandbox without necessarily deleting it.
// Providers without a stop-without-delete lifecycle treat this as delete.
func StopSandboxActivity(ctx context.Context, input StopSandboxInput) (StopSandboxOutput, error) {
	provider, err := getSandboxProvider(input.EnvType)
	if err != nil {
		return StopSandboxOutput{}, err
	}
	return StopSandboxOutput{}, provider.StopSandbox(ctx, input)
}

// DeleteSandboxActivity deletes a sandbox via the provider registered for the
// input's env type. Deleting a sandbox that no longer exists is a no-op.
func DeleteSandboxActivity(ctx context.Context, input DeleteSandboxInput) (DeleteSandboxOutput, error) {
	provider, err := getSandboxProvider(input.EnvType)
	if err != nil {
		return DeleteSandboxOutput{}, err
	}
	return DeleteSandboxOutput{}, provider.DeleteSandbox(ctx, input)
}