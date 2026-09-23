package access

import (
	"context"

	"sidekick/env"
	"sidekick/scripting"
)

// HookScriptHost exposes credential-proxy access to run_command hook scripts.
type HookScriptHost struct{}

var _ env.HookScriptHostProvider = HookScriptHost{}

func (HookScriptHost) HookScriptHost(ctx context.Context, hookCtx env.CommandHookContext) (scripting.Host, error) {
	binding, err := New(ctx, Input{
		RepoConfig:  hookCtx.RepoConfig,
		WorkspaceID: hookCtx.WorkspaceId,
	})
	if err != nil {
		return nil, err
	}
	return binding.Host(), nil
}
