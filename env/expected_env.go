package env

import (
	"encoding/json"
	"errors"

	"go.temporal.io/sdk/workflow"
)

// ErrEnvChanged means an expected-environment dispatch scheduled no activity.
var ErrEnvChanged = errors.New("execution environment changed")

type expectedEnvKey struct{}

type expectedEnv struct {
	instance EnvInstance
	snapshot string
}

func snapshotExpectedEnv(container EnvContainer) (expectedEnv, error) {
	data, err := json.Marshal(container)
	if err != nil {
		return expectedEnv{}, err
	}
	return expectedEnv{instance: InstanceOf(container), snapshot: string(data)}, nil
}

// WithExpectedEnv requires env-carrying dispatches to still target container.
// Unlike pinning, this rejects stale targets rather than allowing execution on
// a retired env. The snapshot includes serialized configuration, not pointers
// into mutable workflow state. Use the returned context only for the dispatch.
func WithExpectedEnv(ctx workflow.Context, container EnvContainer) (workflow.Context, error) {
	expected, err := snapshotExpectedEnv(container)
	if err != nil {
		return nil, err
	}
	return workflow.WithValue(ctx, expectedEnvKey{}, expected), nil
}
