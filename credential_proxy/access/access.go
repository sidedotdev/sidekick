// Package access supplies invocation-bound credential-proxy access for scripts.
package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"sidekick/common"
	"sidekick/credential_proxy"
	"sidekick/credential_proxy/server"
	"sidekick/scripting"

	"go.temporal.io/sdk/activity"
)

// Input contains original host invocation data, not values supplied by scripts.
type Input struct {
	RepoConfig  common.RepoConfig
	WorkspaceID string
	// WorkflowID is required outside Temporal and ignored inside an activity.
	WorkflowID string
	// Socket defaults to the host's private credential-proxy control socket.
	Socket string
}

// Result is runtime-only access data. It must not be returned through an activity.
type Result struct {
	Token     string                                       `json:"token"`
	ExpiresAt string                                       `json:"expiresAt"`
	Proxies   map[string]credential_proxy.ProvisionedProxy `json:"proxies"`
}

// Binding captures trusted configuration and identity for one invocation.
type Binding struct {
	parent      context.Context
	definitions []byte
	scope       string
	port        int
	activate    func(context.Context, server.ActivationRequest) (server.Activation, error)
}

// New captures configuration and identity before scripts can mutate their context.
func New(ctx context.Context, input Input) (*Binding, error) {
	if ctx == nil {
		return nil, errors.New("credential proxy access requires an invocation context")
	}
	workflowID := input.WorkflowID
	if activity.IsActivity(ctx) {
		workflowID = activity.GetInfo(ctx).WorkflowExecution.ID
	}
	if input.WorkspaceID == "" || workflowID == "" {
		return nil, errors.New("credential proxy access requires workspace and workflow identity")
	}
	definitions, err := json.Marshal(input.RepoConfig.CredentialProxies)
	if err != nil {
		return nil, errors.New("invalid credential proxy configuration")
	}
	scope, err := json.Marshal([]string{input.WorkspaceID, workflowID})
	if err != nil {
		return nil, errors.New("invalid credential proxy scope")
	}
	port := 0
	if common.ValidatePortForwards(input.RepoConfig.PortForwards) == nil {
		for _, forward := range input.RepoConfig.PortForwards {
			if forward.HostPort == server.HostPort {
				if port != 0 {
					port = 0
					break
				}
				port = forward.ContainerPortOrDefault()
			}
		}
	}
	socket := input.Socket
	return &Binding{
		parent: ctx, definitions: definitions, scope: string(scope),
		port: port,
		activate: func(ctx context.Context, request server.ActivationRequest) (server.Activation, error) {
			address := socket
			if address == "" {
				var err error
				address, err = server.ControlSocketPath()
				if err != nil {
					return server.Activation{}, errors.New("credential proxy control unavailable")
				}
			}
			return server.RequestCapability(ctx, address, request)
		},
	}, nil
}

// Host returns explicit bindings suitable for scripting.Input.Host.
func (b *Binding) Host() scripting.Host {
	return scripting.Host{
		"credentialProxies": {"acquireAccess": b.operation},
	}
}

func (b *Binding) operation(ctx context.Context, data json.RawMessage) (any, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != 1 {
		return nil, errors.New("credential proxy access requires only names")
	}
	raw, ok := fields["names"]
	var names []string
	if !ok || json.Unmarshal(raw, &names) != nil {
		return nil, errors.New("credential proxy names must be a string array")
	}
	return b.Acquire(ctx, names)
}

// Acquire reauthorizes every selection through the host service. Flow activation
// owns lease reuse and recovery; this binding has no cache or renewal lifecycle.
// Not caching is deliberate: per-command calls are a local unix-socket exchange
// that reuses the flow's lease, so their cost is negligible.
func (b *Binding) Acquire(actionCtx context.Context, names []string) (Result, error) {
	if err := b.parent.Err(); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithCancel(actionCtx)
	defer cancel()
	stop := context.AfterFunc(b.parent, cancel)
	defer stop()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(names) == 0 {
		return Result{}, errors.New("credential proxy access requires at least one name")
	}
	active := slices.Clone(names)
	slices.Sort(active)
	active = slices.Compact(active)
	var definitions map[string]common.CredentialProxyConfig
	if json.Unmarshal(b.definitions, &definitions) != nil {
		return Result{}, errors.New("invalid credential proxy configuration")
	}
	selected := make(map[string]common.CredentialProxyConfig, len(active))
	for _, name := range active {
		definition, ok := definitions[name]
		if !ok {
			return Result{}, errors.New("credential proxy name is not configured")
		}
		selected[name] = definition
	}
	if common.ValidateCredentialProxies(selected) != nil {
		return Result{}, errors.New("invalid credential proxy selection")
	}
	if b.port == 0 {
		return Result{}, errors.New("credential proxy requires a configured reverse forward")
	}
	activation, err := b.activate(ctx, server.ActivationRequest{
		Definitions: selected, Active: active, FlowID: b.scope,
	})
	if parentErr := b.parent.Err(); parentErr != nil {
		return Result{}, parentErr
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return Result{}, contextErr
	}
	if err != nil {
		return Result{}, errors.New("credential proxy access unavailable or denied")
	}
	if len(activation.Token) != 64 || !time.Now().Before(activation.ExpiresAt) ||
		len(activation.Proxies) != len(active) {
		return Result{}, errors.New("credential proxy returned an invalid lease")
	}
	proxies := make(map[string]credential_proxy.ProvisionedProxy, len(active))
	for _, name := range active {
		binding, ok := activation.Proxies[name]
		if !ok || binding.Upstream == "" {
			return Result{}, errors.New("credential proxy returned invalid metadata")
		}
		var side *credential_proxy.SideBinding
		if binding.Side != nil {
			copy := *binding.Side
			side = &copy
		}
		proxies[name] = credential_proxy.ProvisionedProxy{
			URL:      fmt.Sprintf("http://127.0.0.1:%d%s%s", b.port, credential_proxy.PathPrefix, name),
			Upstream: binding.Upstream,
			Side:     side,
		}
	}
	return Result{
		Token: activation.Token, ExpiresAt: activation.ExpiresAt.UTC().Format(time.RFC3339Nano),
		Proxies: proxies,
	}, nil
}
