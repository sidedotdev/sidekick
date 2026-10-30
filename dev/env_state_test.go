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
	require.Nil(t, state.retiredOfType(env.EnvTypeDevPod))

	localWorktree := &domain.Worktree{Id: "wt_1", Name: "side/flow", WorkingDirectory: "/repo-worktrees/flow"}
	state.retire(retiredEnv{EnvContainer: envStateLocal, Worktree: localWorktree})
	state.retire(retiredEnv{EnvContainer: envStateDevPod})
	state.retire(retiredEnv{EnvContainer: envStateLocal, Worktree: localWorktree})
	require.Equal(t, []retiredEnv{
		{EnvContainer: envStateDevPod},
		{EnvContainer: envStateLocal, Worktree: localWorktree},
	}, state.retiredEnvs(), "retiring an env again replaces its earlier entry")

	found := state.retiredOfType(env.EnvTypeDevPod)
	require.NotNil(t, found)
	require.Equal(t, retiredEnv{EnvContainer: envStateDevPod}, *found)
	require.Len(t, state.retiredEnvs(), 2, "looking a retired env up must not drop it")
	require.Nil(t, state.retiredOfType(env.EnvTypeModal))

	state.markStopRequested(envStateDevPod)
	require.Equal(t, retiredEnv{EnvContainer: envStateDevPod, StopRequested: true}, *state.retiredEntry(envStateDevPod))
	state.markStopped(envStateDevPod)
	require.Equal(t, retiredEnv{EnvContainer: envStateDevPod, StopRequested: true, Stopped: true}, *state.retiredEntry(envStateDevPod))
	require.Nil(t, state.retiredEntry(envStateForeign))

	state.unretire(envStateDevPod)
	require.Equal(t, []retiredEnv{{EnvContainer: envStateLocal, Worktree: localWorktree}}, state.retiredEnvs())
}

// A sandbox is one physical env regardless of the directory it is recorded
// at, and a record made mid-provisioning (no worktree yet) must not replace
// the record that knows which worktree to clean up.
func TestFlowEnvStateMatchesSandboxesByName(t *testing.T) {
	t.Parallel()
	worktree := &domain.Worktree{Id: "wt_1", Name: "side/flow", WorkingDirectory: "/root/worktrees/flow"}
	atWorktree := env.EnvContainer{Env: &env.ModalEnv{WorkingDirectory: "/root/worktrees/flow", SandboxName: "side--repo-abc"}}
	atRepo := env.EnvContainer{Env: &env.ModalEnv{WorkingDirectory: "/root/repo", SandboxName: "side--repo-abc"}}
	other := env.EnvContainer{Env: &env.ModalEnv{WorkingDirectory: "/root/worktrees/flow", SandboxName: "side--repo-abc-r1"}}

	state := newFlowEnvState(&flow_action.GlobalState{})
	state.retire(retiredEnv{EnvContainer: atWorktree, Worktree: worktree})
	state.retire(retiredEnv{EnvContainer: atRepo})
	require.Equal(t, []retiredEnv{{EnvContainer: atWorktree, Worktree: worktree}}, state.retiredEnvs())

	state.markStopRequested(atRepo)
	require.True(t, state.retiredEntry(atWorktree).StopRequested)
	require.Nil(t, state.retiredEntry(other))

	state.retire(retiredEnv{EnvContainer: atRepo, Worktree: worktree})
	require.Equal(t, []retiredEnv{{EnvContainer: atRepo, Worktree: worktree, StopRequested: true}}, state.retiredEnvs(),
		"a record with a worktree replaces the earlier one but keeps the sandbox's lifecycle flags")

	state.unretire(atWorktree)
	require.Empty(t, state.retiredEnvs())
}

// TestFlowEnvStateRetiresSandboxReplacementsSeparately covers a same-type
// sandbox replacement: predecessor and successor share the working directory
// (one logical identity, so stale references resolve to the successor) but are
// different sandboxes, each of which must be cleaned up.
func TestFlowEnvStateRetiresSandboxReplacementsSeparately(t *testing.T) {
	t.Parallel()
	predecessor := env.EnvContainer{Env: &env.ModalEnv{WorkingDirectory: "/root/repo", SandboxName: "side--repo-abc"}}
	successor := env.EnvContainer{Env: &env.ModalEnv{WorkingDirectory: "/root/repo", SandboxName: "side--repo-abc-r1"}}
	require.Equal(t, env.IdentityOf(predecessor), env.IdentityOf(successor))
	require.NotEqual(t, env.InstanceOf(predecessor), env.InstanceOf(successor))

	state := newFlowEnvState(&flow_action.GlobalState{})
	state.retire(retiredEnv{EnvContainer: predecessor})
	state.unretire(successor)
	require.Equal(t, []retiredEnv{{EnvContainer: predecessor}}, state.retiredEnvs(),
		"publishing the successor must not drop the predecessor's retired entry")

	state.retire(retiredEnv{EnvContainer: successor})
	require.Equal(t, []retiredEnv{{EnvContainer: predecessor}, {EnvContainer: successor}}, state.retiredEnvs())

	state.unretire(predecessor)
	require.Equal(t, []retiredEnv{{EnvContainer: successor}}, state.retiredEnvs())

	current := successor
	state.publish(&current)
	require.Equal(t, successor, state.Resolve(predecessor), "lineage still resolves by logical identity")
}
