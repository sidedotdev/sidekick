package dev

import (
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	envStateLocal   = env.EnvContainer{Env: &env.LocalGitWorktreeEnv{WorkingDirectory: "/repo-worktrees/flow"}}
	envStateDevPod  = env.EnvContainer{Env: &env.DevPodEnv{WorkingDirectory: "/workspaces/repo-flow", WorkspaceName: "repo"}}
	envStateForeign = env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/repo"}}
)

func TestFlowEnvStateResolvesLineageToCurrentEnv(t *testing.T) {
	t.Parallel()
	state := newFlowEnvState(&flow_action.GlobalState{})

	require.Equal(t, envStateLocal, state.Resolve(envStateLocal), "nothing published yet")

	current := envStateLocal
	state.publish(&current)
	require.Equal(t, envStateLocal, state.Resolve(envStateLocal))
	require.Equal(t, envStateForeign, state.Resolve(envStateForeign))

	current = envStateDevPod
	state.publish(&current)
	state.publish(&current)
	require.Equal(t, envStateDevPod, state.Resolve(envStateLocal))
	require.Equal(t, envStateDevPod, state.Resolve(envStateDevPod))
	require.Equal(t, envStateForeign, state.Resolve(envStateForeign))
	require.Equal(t, []env.EnvIdentity{env.IdentityOf(envStateLocal), env.IdentityOf(envStateDevPod)}, state.lineage())
}

func TestFlowEnvStateTracksSwitchInProgress(t *testing.T) {
	t.Parallel()
	state := newFlowEnvState(&flow_action.GlobalState{})
	require.False(t, state.SwitchInProgress())
	require.Nil(t, state.envSwitch())

	state.setEnvSwitch(envSwitchState{FlowActionId: "fa_1", From: "local", To: "devpod"})
	require.True(t, state.SwitchInProgress())
	target := envStateDevPod
	state.envSwitch().Target = &target
	require.Equal(t, &target, state.envSwitch().Target)

	state.clearEnvSwitch()
	require.False(t, state.SwitchInProgress())
	require.Nil(t, state.envSwitch())
}

func TestFlowEnvStateRetiredEnvs(t *testing.T) {
	t.Parallel()
	state := newFlowEnvState(&flow_action.GlobalState{})
	require.Empty(t, state.retiredEnvs())
	require.Nil(t, state.takeRetiredOfType(env.EnvTypeDevPod))

	localWorktree := &domain.Worktree{Id: "wt_1", Name: "side/flow", WorkingDirectory: "/repo-worktrees/flow"}
	state.retire(retiredEnv{EnvContainer: envStateLocal, Worktree: localWorktree})
	state.retire(retiredEnv{EnvContainer: envStateDevPod})
	state.retire(retiredEnv{EnvContainer: envStateLocal, Worktree: localWorktree})
	require.Equal(t, []retiredEnv{
		{EnvContainer: envStateDevPod},
		{EnvContainer: envStateLocal, Worktree: localWorktree},
	}, state.retiredEnvs(), "retiring an env again replaces its earlier entry")

	taken := state.takeRetiredOfType(env.EnvTypeDevPod)
	require.NotNil(t, taken)
	require.Equal(t, retiredEnv{EnvContainer: envStateDevPod}, *taken)
	require.Equal(t, []retiredEnv{{EnvContainer: envStateLocal, Worktree: localWorktree}}, state.retiredEnvs())
	require.Nil(t, state.takeRetiredOfType(env.EnvTypeModal))
}
