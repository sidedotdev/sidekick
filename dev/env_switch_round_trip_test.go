package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sidekick/coding/git"
	"sidekick/coding/unix"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/persisted_ai"
	"sidekick/srv"
	"sidekick/srv/sqlite"
	"sidekick/temporalmeta"
	"sidekick/workspace"

	"github.com/adrg/xdg"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const roundTripQuery = "envSwitchRoundTrip"
const roundTripProbeSignal = "envSwitchProbe"

const roundTripInspectScript = `set -eu
git branch --show-current
git rev-parse HEAD
git status --porcelain=v1 --untracked-files=all
git diff --binary
git diff --cached --binary
cat untracked.txt
`

type envSwitchRoundTripInput struct {
	WorkspaceId string                 `json:"workspaceId"`
	RepoDir     string                 `json:"repoDir"`
	FlowId      string                 `json:"flowId"`
	Overrides   common.ConfigOverrides `json:"overrides"`
}

type envSwitchRoundTripState struct {
	Current          env.EnvContainer                  `json:"current"`
	Worktree         domain.Worktree                   `json:"worktree"`
	Retired          []retiredEnv                      `json:"retired"`
	SwitchInProgress bool                              `json:"switchInProgress"`
	Attempts         int                               `json:"attempts"`
	DuringSwitch     []bool                            `json:"duringSwitch"`
	Probes           []env.EnvRunCommandActivityOutput `json:"probes"`
}

func envSwitchRoundTripParent(ctx workflow.Context, input envSwitchRoundTripInput) error {
	ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: input.FlowId})
	return workflow.ExecuteChildWorkflow(ctx, envSwitchRoundTripWorkflow, input).Get(ctx, nil)
}

func envSwitchRoundTripWorkflow(ctx workflow.Context, input envSwitchRoundTripInput) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
	startBranch := "main"
	dCtx, err := SetupDevContext(ctx, input.WorkspaceId, input.RepoDir,
		string(env.EnvTypeLocal), string(env.RepoModeWorktree), &startBranch,
		"environment switch round trip", input.Overrides)
	if err != nil {
		return err
	}
	defer handleFlowCancel(dCtx)
	if err := SetupEnvSettingsHandlers(dCtx); err != nil {
		return err
	}

	state := newFlowEnvState(dCtx.GlobalState)
	original := *dCtx.EnvContainer
	var observed envSwitchRoundTripState
	if err := workflow.SetQueryHandler(ctx, roundTripQuery, func() (envSwitchRoundTripState, error) {
		result := observed
		result.Current = *dCtx.EnvContainer
		result.Worktree = *dCtx.Worktree
		result.Retired = state.retiredEnvs()
		result.SwitchInProgress = state.SwitchInProgress()
		return result, nil
	}); err != nil {
		return err
	}

	signals := workflow.GetSignalChannel(ctx, roundTripProbeSignal)
	for {
		canceled := false
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(ctx.Done(), func(workflow.ReceiveChannel, bool) { canceled = true })
		selector.AddReceive(signals, func(ch workflow.ReceiveChannel, _ bool) {
			var token string
			ch.Receive(ctx, &token)
		})
		selector.Select(ctx)
		if canceled {
			return ctx.Err()
		}
		observed.Attempts++
		observed.DuringSwitch = append(observed.DuringSwitch, state.SwitchInProgress())
		var output env.EnvRunCommandActivityOutput
		err := workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer: original,
			Command:      "sh",
			Args:         []string{"-c", roundTripInspectScript},
		}).Get(dCtx, &output)
		if err != nil {
			return err
		}
		observed.Probes = append(observed.Probes, output)
	}
}

// The barrier leaves export and provisioning real while giving the signal a
// deterministic opportunity to reach the workflow's activity gate.
type envSwitchExportBarrier struct {
	entered chan struct{}
	release chan struct{}
	stop    chan struct{}
	once    sync.Once
}

func (b *envSwitchExportBarrier) export(ctx context.Context, input env.ExportForSwitchInput) (env.ExportForSwitchOutput, error) {
	select {
	case b.entered <- struct{}{}:
	case <-ctx.Done():
		return env.ExportForSwitchOutput{}, ctx.Err()
	case <-b.stop:
		return env.ExportForSwitchOutput{}, fmt.Errorf("round trip stopped")
	}
	select {
	case <-b.release:
		return env.ExportForSwitchActivity(ctx, input)
	case <-ctx.Done():
		return env.ExportForSwitchOutput{}, ctx.Err()
	case <-b.stop:
		return env.ExportForSwitchOutput{}, fmt.Errorf("round trip stopped")
	}
}

func registerEnvSwitchRoundTripActivities(w worker.Worker, service *srv.Delegator, temporalClient client.Client, barrier *envSwitchExportBarrier) {
	for _, fn := range []interface{}{
		env.NewLocalGitWorktreeActivity, env.DevPodUpActivity, env.CreateDevPodWorktreeActivity,
		env.CreateSandboxActivity, env.CheckSandboxActivity, env.StopSandboxActivity, env.DeleteSandboxActivity,
		env.SyncRepoToRemoteActivity, env.DeepenRepoActivity, env.CreateRemoteWorktreeActivity,
		env.RestoreFromSwitchActivity, env.VerifySwitchActivity, env.UpdateEnvConfigActivity,
		env.EnvRunCommandActivity, env.GetEnvironmentInfoActivity,
		git.GetGitUserConfigActivity, git.WakeWorktreeActivity,
		GetRepoConfigActivity, GetRepoConfigActivityV2, ResolveToolNameMappingActivity,
		common.GetLocalConfig, common.BaseCommandPermissionsActivity, unix.RunCommandActivity,
		&srv.Activities{Service: service},
		&workspace.Activities{Storage: service},
		&flow_action.FlowActivities{Service: service},
		&common.KVActivities{Storage: service},
		&persisted_ai.ChatHistoryActivities{Storage: service},
		&persisted_ai.Llm2Activities{Storage: service, Streamer: service},
		&temporalmeta.TemporalMetaActivities{Client: temporalClient},
	} {
		w.RegisterActivity(fn)
	}
	w.RegisterActivityWithOptions(barrier.export, activity.RegisterOptions{Name: "ExportForSwitchActivity"})
	w.RegisterActivityWithOptions(roundTripCleanupWorktree, activity.RegisterOptions{Name: "CleanupWorktreeActivity"})
}

func roundTripCommand(t *testing.T, ctx context.Context, container env.EnvContainer, script string) string {
	t.Helper()
	output, err := container.Env.RunCommand(ctx, env.EnvRunCommandInput{
		Command: "sh",
		Args:    []string{"-c", script},
	})
	require.NoError(t, err)
	require.Zero(t, output.ExitStatus, "%s\n%s", output.Stdout, output.Stderr)
	return output.Stdout
}

func roundTripHistory(t *testing.T, ctx context.Context, c client.Client, flowId string) []*historypb.HistoryEvent {
	t.Helper()
	iter := c.GetWorkflowHistory(ctx, flowId, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var events []*historypb.HistoryEvent
	for iter.HasNext() {
		event, err := iter.Next()
		require.NoError(t, err)
		events = append(events, event)
	}
	return events
}

func requireRoundTripProbeHistory(t *testing.T, events []*historypb.HistoryEvent, expected []env.EnvIdentity) {
	t.Helper()
	var actual []env.EnvIdentity
	for _, event := range events {
		scheduled := event.GetActivityTaskScheduledEventAttributes()
		if scheduled == nil || scheduled.ActivityType.Name != "EnvRunCommandActivity" {
			continue
		}
		var input env.EnvRunCommandActivityInput
		require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(scheduled.Input, &input))
		if input.Command == "sh" && len(input.Args) == 2 && input.Args[1] == roundTripInspectScript {
			actual = append(actual, env.IdentityOf(input.EnvContainer))
		}
	}
	require.Equal(t, expected, actual, "history must contain the resolved container, not the stale activity argument")
}

func requireRoundTripCleanupHistory(t *testing.T, events []*historypb.HistoryEvent, containers []env.EnvContainer, sandbox env.SandboxEnv) {
	t.Helper()
	completed := make(map[int64]bool)
	for _, event := range events {
		if attrs := event.GetActivityTaskCompletedEventAttributes(); attrs != nil {
			completed[attrs.ScheduledEventId] = true
		}
	}
	var cleaned []env.EnvIdentity
	var deleted []string
	for _, event := range events {
		attrs := event.GetActivityTaskScheduledEventAttributes()
		if attrs == nil {
			continue
		}
		switch attrs.ActivityType.Name {
		case "CleanupWorktreeActivity":
			var container env.EnvContainer
			var dir, branch, reason string
			require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(attrs.Input, &container, &dir, &branch, &reason))
			require.True(t, completed[event.EventId], "worktree cleanup did not succeed: %s", dir)
			require.Equal(t, container.Env.GetWorkingDirectory(), dir)
			cleaned = append(cleaned, env.IdentityOf(container))
		case "DeleteSandboxActivity":
			var input env.DeleteSandboxInput
			require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(attrs.Input, &input))
			require.True(t, completed[event.EventId], "sandbox deletion did not succeed")
			require.Equal(t, sandbox.GetType(), input.EnvType)
			deleted = append(deleted, input.SandboxName)
		}
	}
	var expected []env.EnvIdentity
	for _, container := range containers {
		expected = append(expected, env.IdentityOf(container))
	}
	require.ElementsMatch(t, expected, cleaned)
	require.Equal(t, []string{sandbox.GetSandboxName()}, deleted)
}

func runEnvSwitchRoundTrip(t *testing.T, targetEnvType env.EnvType, configure func(*common.RepoConfig)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	root := t.TempDir()
	repoDir := filepath.Join(root, "repo-"+strings.ToLower(ksuid.New().String()))
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, ".devcontainer"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".devcontainer", "devcontainer.json"),
		[]byte(`{"name":"env-switch","build":{"dockerfile":"Dockerfile"}}`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".devcontainer", "Dockerfile"), []byte(
		"FROM debian:bookworm-slim\nRUN apt-get update && apt-get install -y git openssh-client ca-certificates tar gzip && rm -rf /var/lib/apt/lists/*\n"), 0644))

	config := common.RepoConfig{EnvType: string(env.EnvTypeLocal), RepoMode: string(env.RepoModeWorktree), DisableHumanInTheLoop: true}
	if configure != nil {
		configure(&config)
	}
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "side.yml"), []byte("env_type: local\nrepo_mode: worktree\ndisable_human_in_the_loop: true\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "tracked.txt"), []byte("base\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "staged.txt"), []byte("base\n"), 0644))
	for _, args := range [][]string{
		{"init", "-b", "main"}, {"config", "user.name", "Environment Switch Test"},
		{"config", "user.email", "env-switch@example.invalid"},
		{"add", "."}, {"commit", "-m", "initial fixture"},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = repoDir
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, output)
	}

	// Isolate Sidekick without hiding provider credentials from subprocesses.
	// An unknown model provider uses deterministic branch-name fallback.
	previousConfigHome, previousConfigDirs, previousDataHome := xdg.ConfigHome, xdg.ConfigDirs, xdg.DataHome
	xdg.ConfigHome, xdg.ConfigDirs, xdg.DataHome = filepath.Join(root, "config"), nil, filepath.Join(root, "data")
	t.Cleanup(func() {
		xdg.ConfigHome, xdg.ConfigDirs, xdg.DataHome = previousConfigHome, previousConfigDirs, previousDataHome
	})

	service := srv.NewDelegator(sqlite.NewTestSqliteStorage(t, "env_switch_"+ksuid.New().String()), srv.NewMemoryStreamer())
	input := envSwitchRoundTripInput{
		WorkspaceId: "ws_" + ksuid.New().String(),
		RepoDir:     repoDir,
		FlowId:      "env-switch-" + ksuid.New().String(),
		Overrides: common.ConfigOverrides{
			DevPodConfig: &config.DevPodConfig, OpenShellConfig: &config.OpenShellConfig, ModalConfig: &config.ModalConfig,
		},
	}
	sandboxName := env.DevPodWorkspaceName(repoDir)
	switch targetEnvType {
	case env.EnvTypeOpenShell:
		sandboxName = env.OpenShellSandboxName(repoDir)
	case env.EnvTypeModal:
		sandboxName = env.ModalSandboxName(input.WorkspaceId, repoDir, input.FlowId)
	}
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if _, err := env.DeleteSandboxActivity(cleanupCtx, env.DeleteSandboxInput{
			EnvType: targetEnvType, SandboxName: sandboxName,
		}); err != nil {
			t.Logf("failure-path sandbox cleanup: %v", err)
		}
	})
	require.NoError(t, service.PersistWorkspace(ctx, domain.Workspace{
		Id: input.WorkspaceId, Name: t.Name(), LocalRepoDir: repoDir, ConfigMode: "workspace",
	}))
	require.NoError(t, service.PersistWorkspaceConfig(ctx, input.WorkspaceId, domain.WorkspaceConfig{
		LLM: common.LLMConfig{Defaults: []common.ModelConfig{{Provider: "env-switch-test", Model: "unavailable"}}},
	}))

	server, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{
		ClientOptions: &client.Options{
			ContextPropagators: []workflow.ContextPropagator{flow_action.NewFlowActionIdPropagator()},
		},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, server.Stop()) }()
	c := server.Client()
	barrier := &envSwitchExportBarrier{
		entered: make(chan struct{}, 1), release: make(chan struct{}), stop: make(chan struct{}),
	}
	defer barrier.once.Do(func() { close(barrier.stop) })
	w := worker.New(c, input.FlowId, worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{env.NewEnvSwitchGateInterceptor()},
	})
	w.RegisterWorkflow(envSwitchRoundTripParent)
	w.RegisterWorkflow(envSwitchRoundTripWorkflow)
	registerEnvSwitchRoundTripActivities(w, service, c, barrier)
	require.NoError(t, w.Start())
	defer w.Stop()

	parent, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: input.FlowId + "-parent", TaskQueue: input.FlowId,
	}, envSwitchRoundTripParent, input)
	require.NoError(t, err)
	defer func() {
		barrier.once.Do(func() { close(barrier.stop) })
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_ = c.CancelWorkflow(cleanupCtx, input.FlowId, "")
		_ = parent.Get(cleanupCtx, nil)
	}()

	query := func() (envSwitchRoundTripState, error) {
		value, err := c.QueryWorkflow(ctx, input.FlowId, "", roundTripQuery)
		if err != nil {
			return envSwitchRoundTripState{}, err
		}
		var state envSwitchRoundTripState
		err = value.Get(&state)
		return state, err
	}
	var initial envSwitchRoundTripState
	require.Eventually(t, func() bool {
		initial, err = query()
		return err == nil
	}, time.Minute, 100*time.Millisecond, "workflow did not initialize: %v", err)
	require.Equal(t, env.EnvTypeLocalGitWorktree, initial.Current.Env.GetType())
	require.DirExists(t, initial.Worktree.WorkingDirectory)
	initialHead := strings.TrimSpace(roundTripCommand(t, ctx, initial.Current, "git rev-parse HEAD"))

	roundTripCommand(t, ctx, initial.Current, `set -eu
printf 'local unstaged\n' >> tracked.txt
printf 'local staged\n' >> staged.txt
git add staged.txt
printf 'local untracked\n' > untracked.txt
`)
	expected := roundTripCommand(t, ctx, initial.Current, roundTripInspectScript)
	require.Contains(t, expected, " M tracked.txt")
	require.Contains(t, expected, "M  staged.txt")
	require.Contains(t, expected, "?? untracked.txt")

	var probeEnvs []env.EnvIdentity
	switchTo := func(envType env.EnvType, probeCount int) envSwitchRoundTripState {
		t.Helper()
		handle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID: input.FlowId, UpdateName: UpdateNameEnv,
			WaitForStage: client.WorkflowUpdateStageCompleted,
			Args: []interface{}{EnvSettings{
				EnvType: string(envType), Modal: config.ModalConfig, PortForwards: config.PortForwards,
			}},
		})
		require.NoError(t, err)
		var result EnvUpdateResult
		require.NoError(t, handle.Get(ctx, &result))
		require.NotEmpty(t, result.FlowActionId)
		select {
		case <-barrier.entered:
		case <-ctx.Done():
			t.Fatal("export barrier was not reached")
		}
		action, err := service.GetFlowAction(ctx, input.WorkspaceId, result.FlowActionId)
		require.NoError(t, err)
		require.Equal(t, ActionTypeEnvSwitch, action.ActionType)
		require.Equal(t, domain.ActionStatusStarted, action.ActionStatus)
		require.NoError(t, c.SignalWorkflow(ctx, input.FlowId, "", roundTripProbeSignal, "inspect"))
		require.Eventually(t, func() bool {
			state, err := query()
			return err == nil && state.Attempts == probeCount
		}, 10*time.Second, 50*time.Millisecond)
		blocked, err := query()
		require.NoError(t, err)
		require.True(t, blocked.SwitchInProgress)
		require.True(t, blocked.DuringSwitch[probeCount-1])
		require.Len(t, blocked.Probes, probeCount-1)
		requireRoundTripProbeHistory(t, roundTripHistory(t, ctx, c, input.FlowId), probeEnvs)
		select {
		case barrier.release <- struct{}{}:
		case <-ctx.Done():
			t.Fatal("export barrier could not be released")
		}
		require.Eventually(t, func() bool {
			action, err = service.GetFlowAction(ctx, input.WorkspaceId, result.FlowActionId)
			return err == nil && (action.ActionStatus == domain.ActionStatusComplete || action.ActionStatus == domain.ActionStatusFailed)
		}, 5*time.Minute, 100*time.Millisecond)
		require.Equal(t, domain.ActionStatusComplete, action.ActionStatus, "%s", action.ActionResult)
		var state envSwitchRoundTripState
		require.Eventually(t, func() bool {
			state, err = query()
			return err == nil && !state.SwitchInProgress && len(state.Probes) == probeCount
		}, 30*time.Second, 50*time.Millisecond)
		for _, event := range roundTripHistory(t, ctx, c, input.FlowId) {
			if attrs := event.GetActivityTaskScheduledEventAttributes(); attrs != nil {
				require.NotContains(t, []string{"CleanupWorktreeActivity", "StopSandboxActivity", "DeleteSandboxActivity"},
					attrs.ActivityType.Name, "switching must retire environments without cleaning them up")
			}
		}
		probe := state.Probes[probeCount-1]
		require.Zero(t, probe.ExitStatus, "%s", probe.Stderr)
		require.Equal(t, expected, probe.Stdout)
		require.Equal(t, initialHead, strings.TrimSpace(roundTripCommand(t, ctx, state.Current, "git rev-parse HEAD")))
		require.Equal(t, initial.Worktree.Name, strings.TrimSpace(roundTripCommand(t, ctx, state.Current, "git branch --show-current")))
		require.Equal(t, initial.Worktree.Name, state.Worktree.Name)
		require.Equal(t, initial.Worktree.Id, state.Worktree.Id)
		probeEnvs = append(probeEnvs, env.IdentityOf(state.Current))
		requireRoundTripProbeHistory(t, roundTripHistory(t, ctx, c, input.FlowId), probeEnvs)
		return state
	}

	remote := switchTo(targetEnvType, 1)
	require.Equal(t, targetEnvType, remote.Current.Env.GetType())
	require.Len(t, remote.Retired, 1)
	require.Equal(t, env.IdentityOf(initial.Current), env.IdentityOf(remote.Retired[0].EnvContainer))
	require.DirExists(t, initial.Worktree.WorkingDirectory)
	require.Equal(t, expected, roundTripCommand(t, ctx, initial.Current, roundTripInspectScript))
	sandbox, ok := remote.Current.Env.(env.SandboxEnv)
	require.True(t, ok)

	// The return leg must import remote changes, not merely expose the WIP
	// left behind in the retired local worktree.
	roundTripCommand(t, ctx, remote.Current, `set -eu
printf 'remote unstaged\n' >> tracked.txt
printf 'remote staged\n' >> staged.txt
git add staged.txt
printf 'remote untracked\n' >> untracked.txt
`)
	expected = roundTripCommand(t, ctx, remote.Current, roundTripInspectScript)
	local := switchTo(env.EnvTypeLocal, 2)
	require.Equal(t, env.EnvTypeLocalGitWorktree, local.Current.Env.GetType())
	require.Equal(t, initial.Worktree.WorkingDirectory, local.Worktree.WorkingDirectory)
	require.Len(t, local.Retired, 1)
	require.Equal(t, env.IdentityOf(remote.Current), env.IdentityOf(local.Retired[0].EnvContainer))
	require.Equal(t, expected, roundTripCommand(t, ctx, remote.Current, roundTripInspectScript))
	require.True(t, roundTripSandboxExists(t, ctx, targetEnvType, sandbox.GetSandboxName()),
		"retirement must not stop or delete the sandbox")

	// Cancellation deliberately preserves dirty worktrees. Remove only the
	// fixture WIP after all preservation assertions to exercise removal.
	for _, container := range []env.EnvContainer{local.Current, remote.Current} {
		roundTripCommand(t, ctx, container, "git reset --hard HEAD && git clean -fd")
	}
	require.NoError(t, c.CancelWorkflow(ctx, input.FlowId, ""))
	require.Error(t, parent.Get(ctx, nil))
	requireRoundTripCleanupHistory(t, roundTripHistory(t, ctx, c, input.FlowId),
		[]env.EnvContainer{local.Current, remote.Current}, sandbox)
	require.NoDirExists(t, initial.Worktree.WorkingDirectory)
	require.False(t, roundTripSandboxExists(t, ctx, targetEnvType, sandbox.GetSandboxName()))
}

func roundTripSandboxExists(t *testing.T, ctx context.Context, envType env.EnvType, name string) bool {
	t.Helper()
	if envType == env.EnvTypeDevPod {
		output, err := exec.CommandContext(ctx, "devpod", "list", "--output", "json").Output()
		require.NoError(t, err)
		var workspaces []struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(output, &workspaces))
		for _, workspace := range workspaces {
			require.NotEmpty(t, workspace.ID, "unexpected devpod list schema")
			if workspace.ID == name {
				return true
			}
		}
		return false
	}
	check, err := env.CheckSandboxActivity(ctx, env.CheckSandboxInput{EnvType: envType, SandboxName: name})
	require.NoError(t, err)
	return check.Alive
}

func roundTripCleanupWorktree(ctx context.Context, container env.EnvContainer, dir, branch, reason string) error {
	if err := git.CleanupWorktreeActivity(ctx, container, dir, branch, reason); err != nil {
		return err
	}
	parent := env.EnvWithDir(container.Env, filepath.Dir(dir))
	if _, err := parent.Stat(ctx, dir); !os.IsNotExist(err) {
		return fmt.Errorf("worktree still accessible after cleanup at %s: %v", dir, err)
	}
	return nil
}
