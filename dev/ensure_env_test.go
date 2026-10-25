package dev

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/srv"
	"sidekick/utils"
)

const ensureEnvTestRepoDir = "/host/repo"

type ensureEnvTestInput struct {
	EnvType     string
	RepoMode    string
	StartBranch string
	RepoConfig  common.RepoConfig
	// LoadRepoConfig mirrors initial setup, which loads the repo config
	// through GetRepoConfig instead of passing RepoConfig.
	LoadRepoConfig bool
	AllowReuse     bool
	Attach         *domain.Worktree
}

func ensureEnvTestWorkflow(ctx workflow.Context, input ensureEnvTestInput) (ensuredEnv, error) {
	ctx = utils.NoRetryCtx(ctx)
	params := ensureEnvParams{
		WorkspaceId: "ws_test",
		RepoDir:     ensureEnvTestRepoDir,
		EnvType:     input.EnvType,
		RepoMode:    input.RepoMode,
		StartBranch: &input.StartBranch,
		RepoConfig:  input.RepoConfig,
		AllowReuse:  input.AllowReuse,
	}
	if input.LoadRepoConfig {
		params.LoadRepoConfig = func() (common.RepoConfig, error) {
			return GetRepoConfig(flow_action.ExecContext{
				Context:      ctx,
				EnvContainer: &env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: ensureEnvTestRepoDir}},
			})
		}
	}
	if input.Attach != nil {
		params.CreateWorktree = attachWorktree(ctx, *input.Attach)
	}
	return ensureEnv(ctx, params)
}

// ensureEnvMocks stubs every provisioning activity, recording the order in
// which they start and the arguments of their latest call.
type ensureEnvMocks struct {
	repoConfig    common.RepoConfig
	repoConfigErr error
	worktreeErr   error
	persistErr    error

	mu    sync.Mutex
	order []string
	calls map[string]mock.Arguments
}

func (m *ensureEnvMocks) record(name string) func(mock.Arguments) {
	return func(args mock.Arguments) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.order = append(m.order, name)
		m.calls[name] = args
	}
}

func (m *ensureEnvMocks) recorded() ([]string, map[string]mock.Arguments) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.order...), m.calls
}

func (m *ensureEnvMocks) register(wfEnv *testsuite.TestWorkflowEnvironment) {
	m.calls = map[string]mock.Arguments{}
	wfEnv.OnActivity(GetRepoConfigActivityV2, mock.Anything).
		Run(m.record("GetRepoConfig")).
		Return(GetRepoConfigActivityResult{Config: m.repoConfig}, m.repoConfigErr).Maybe()
	wfEnv.OnActivity(env.NewLocalGitWorktreeActivity, mock.Anything, mock.Anything, mock.Anything).
		Run(m.record("NewLocalGitWorktree")).
		Return(func(_ context.Context, _ env.LocalEnvParams, wt domain.Worktree) (env.EnvContainer, error) {
			if m.worktreeErr != nil {
				return env.EnvContainer{}, m.worktreeErr
			}
			return env.EnvContainer{Env: &env.LocalGitWorktreeEnv{WorkingDirectory: "/worktrees/" + wt.Name}}, nil
		}).Maybe()
	wfEnv.OnActivity(env.DevPodUpActivity, mock.Anything, mock.Anything).
		Run(m.record("DevPodUp")).Return(nil).Maybe()
	wfEnv.OnActivity(env.CreateDevPodWorktreeActivity, mock.Anything, mock.Anything).
		Run(m.record("CreateDevPodWorktree")).
		Return(func(_ context.Context, input env.CreateDevPodWorktreeInput) (env.CreateDevPodWorktreeOutput, error) {
			if m.worktreeErr != nil {
				return env.CreateDevPodWorktreeOutput{}, m.worktreeErr
			}
			return env.CreateDevPodWorktreeOutput{WorktreePath: "/workspaces/sidekick-worktrees/" + input.BranchName}, nil
		}).Maybe()
	wfEnv.OnActivity(env.CheckSandboxActivity, mock.Anything, mock.Anything).
		Run(m.record("CheckSandbox")).Return(env.CheckSandboxOutput{}, nil).Maybe()
	wfEnv.OnActivity(env.CreateSandboxActivity, mock.Anything, mock.Anything).
		Run(m.record("CreateSandbox")).
		Return(func(_ context.Context, input env.CreateSandboxInput) (env.CreateSandboxOutput, error) {
			return env.CreateSandboxOutput{SandboxName: input.Name, SSHHost: "sandbox.host", SSHPort: 2222}, nil
		}).Maybe()
	wfEnv.OnActivity(env.SyncRepoToRemoteActivity, mock.Anything, mock.Anything).
		Run(m.record("SyncRepoToRemote")).
		Return(env.SyncRepoToRemoteOutput{RemoteRepoDir: "/home/user/repo"}, nil).Maybe()
	// Deepening runs in the background concurrently with worktree creation,
	// so it is excluded from the recorded order.
	wfEnv.OnActivity(env.DeepenRepoActivity, mock.Anything, mock.Anything).
		Return(env.DeepenRepoOutput{}, nil).Maybe()
	wfEnv.OnActivity(env.CreateRemoteWorktreeActivity, mock.Anything, mock.Anything).
		Run(m.record("CreateRemoteWorktree")).
		Return(func(_ context.Context, input env.CreateRemoteWorktreeInput) (env.CreateRemoteWorktreeOutput, error) {
			if m.worktreeErr != nil {
				return env.CreateRemoteWorktreeOutput{}, m.worktreeErr
			}
			return env.CreateRemoteWorktreeOutput{WorktreePath: "/home/user/sidekick-worktrees/" + input.BranchName}, nil
		}).Maybe()
	var srvActivities srv.Activities
	wfEnv.OnActivity(srvActivities.PersistWorktree, mock.Anything, mock.Anything).
		Run(m.record("PersistWorktree")).Return(m.persistErr).Maybe()
}

func runEnsureEnvTestWorkflow(t *testing.T, mocks *ensureEnvMocks, input ensureEnvTestInput) (ensuredEnv, error) {
	t.Helper()
	var workflowSuite testsuite.WorkflowTestSuite
	wfEnv := workflowSuite.NewTestWorkflowEnvironment()
	wfEnv.SetWorkerOptions(utils.TestWorkerOptions())
	mocks.register(wfEnv)
	wfEnv.ExecuteWorkflow(ensureEnvTestWorkflow, input)
	require.True(t, wfEnv.IsWorkflowCompleted())
	if err := wfEnv.GetWorkflowError(); err != nil {
		return ensuredEnv{}, err
	}
	var result ensuredEnv
	require.NoError(t, wfEnv.GetWorkflowResult(&result))
	return result, nil
}

func flowWorktree() *domain.Worktree {
	return &domain.Worktree{
		Id:               "wt_existing",
		FlowId:           "flow_existing",
		Name:             "side/flow-branch",
		Created:          time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		WorkspaceId:      "ws_test",
		WorkingDirectory: "/previous/env/worktree",
	}
}

func TestEnsureEnvLoadsRepoConfigAtProviderPoint(t *testing.T) {
	t.Parallel()
	loadedConfig := common.RepoConfig{
		DevPodConfig: common.DevPodEnvConfig{WorkspaceId: "custom-ws"},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}

	cases := []struct {
		name          string
		input         ensureEnvTestInput
		repoConfigErr error
		wantOrder     []string
		assertEnv     func(t *testing.T, result ensuredEnv, calls map[string]mock.Arguments)
	}{
		{
			name:      "local in place provisions without activities",
			input:     ensureEnvTestInput{EnvType: string(env.EnvTypeLocal), RepoMode: string(env.RepoModeInPlace)},
			wantOrder: nil,
			assertEnv: func(t *testing.T, result ensuredEnv, _ map[string]mock.Arguments) {
				assert.Equal(t, env.EnvTypeLocal, result.EnvContainer.Env.GetType())
				assert.Equal(t, ensureEnvTestRepoDir, result.EnvContainer.Env.GetWorkingDirectory())
				assert.Nil(t, result.Worktree)
			},
		},
		{
			name:      "legacy local worktree never loads config",
			input:     ensureEnvTestInput{EnvType: string(env.EnvTypeLocalGitWorktree), Attach: flowWorktree()},
			wantOrder: []string{"NewLocalGitWorktree", "PersistWorktree"},
			assertEnv: func(t *testing.T, result ensuredEnv, _ map[string]mock.Arguments) {
				assert.Equal(t, env.EnvTypeLocalGitWorktree, result.EnvContainer.Env.GetType())
				require.NotNil(t, result.Worktree)
			},
		},
		{
			name:      "devpod applies loaded workspace id and port forwards",
			input:     ensureEnvTestInput{EnvType: string(env.EnvTypeDevPod), RepoMode: string(env.RepoModeInPlace)},
			wantOrder: []string{"GetRepoConfig", "DevPodUp"},
			assertEnv: func(t *testing.T, result ensuredEnv, calls map[string]mock.Arguments) {
				upInput := calls["DevPodUp"].Get(1).(env.DevPodUpInput)
				assert.Equal(t, env.DevPodUpInput{WorkspacePath: ensureEnvTestRepoDir, WorkspaceId: "custom-ws"}, upInput)
				devPodEnv, ok := result.EnvContainer.Env.(*env.DevPodEnv)
				require.True(t, ok)
				assert.Equal(t, env.DevPodEnv{
					WorkingDirectory: "/workspaces/repo",
					WorkspaceName:    "custom-ws",
					LocalRepoDir:     ensureEnvTestRepoDir,
					PortForwards:     loadedConfig.PortForwards,
				}, *devPodEnv)
			},
		},
		{
			name:          "devpod falls back to defaults when config load fails",
			input:         ensureEnvTestInput{EnvType: string(env.EnvTypeDevPod), RepoMode: string(env.RepoModeInPlace)},
			repoConfigErr: temporal.NewNonRetryableApplicationError("no side.yml", "test", nil),
			wantOrder:     []string{"GetRepoConfig", "DevPodUp"},
			assertEnv: func(t *testing.T, result ensuredEnv, calls map[string]mock.Arguments) {
				upInput := calls["DevPodUp"].Get(1).(env.DevPodUpInput)
				assert.Equal(t, env.DevPodUpInput{WorkspacePath: ensureEnvTestRepoDir}, upInput)
				devPodEnv, ok := result.EnvContainer.Env.(*env.DevPodEnv)
				require.True(t, ok)
				assert.Equal(t, env.DevPodWorkspaceName(ensureEnvTestRepoDir), devPodEnv.WorkspaceName)
				assert.Empty(t, devPodEnv.PortForwards)
			},
		},
		{
			name:      "openshell loads config before checking the sandbox",
			input:     ensureEnvTestInput{EnvType: string(env.EnvTypeOpenShell), RepoMode: string(env.RepoModeWorktree), Attach: flowWorktree()},
			wantOrder: []string{"GetRepoConfig", "CheckSandbox", "CreateSandbox", "SyncRepoToRemote", "CreateRemoteWorktree", "PersistWorktree"},
			assertEnv: func(t *testing.T, result ensuredEnv, _ map[string]mock.Arguments) {
				openShellEnv, ok := result.EnvContainer.Env.(*env.OpenShellEnv)
				require.True(t, ok)
				assert.Equal(t, loadedConfig.PortForwards, openShellEnv.PortForwards)
			},
		},
		{
			name:      "modal loads config before creating the sandbox",
			input:     ensureEnvTestInput{EnvType: string(env.EnvTypeModal), RepoMode: string(env.RepoModeWorktree), Attach: flowWorktree()},
			wantOrder: []string{"GetRepoConfig", "CreateSandbox", "SyncRepoToRemote", "CreateRemoteWorktree", "PersistWorktree"},
			assertEnv: func(t *testing.T, result ensuredEnv, _ map[string]mock.Arguments) {
				modalEnv, ok := result.EnvContainer.Env.(*env.ModalEnv)
				require.True(t, ok)
				assert.Equal(t, loadedConfig.PortForwards, modalEnv.PortForwards)
				assert.Equal(t, "sandbox.host", modalEnv.SSHHost)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mocks := &ensureEnvMocks{repoConfig: loadedConfig, repoConfigErr: tc.repoConfigErr}
			tc.input.LoadRepoConfig = true
			result, err := runEnsureEnvTestWorkflow(t, mocks, tc.input)
			require.NoError(t, err)
			order, calls := mocks.recorded()
			assert.Equal(t, tc.wantOrder, order)
			tc.assertEnv(t, result, calls)
		})
	}
}

func TestEnsureEnvAttachesExistingWorktree(t *testing.T) {
	t.Parallel()
	repoConfig := common.RepoConfig{
		DevPodConfig: common.DevPodEnvConfig{WorkspaceId: "custom-ws"},
		PortForwards: []common.PortForwardConfig{{HostPort: 18855}},
	}

	cases := []struct {
		envType          env.EnvType
		createActivity   string
		wantEnvType      env.EnvType
		wantWorkingDir   string
		assertCreateCall func(t *testing.T, args mock.Arguments)
	}{
		{
			envType:        env.EnvTypeLocal,
			createActivity: "NewLocalGitWorktree",
			wantEnvType:    env.EnvTypeLocalGitWorktree,
			wantWorkingDir: "/worktrees/side/flow-branch",
			assertCreateCall: func(t *testing.T, args mock.Arguments) {
				params := args.Get(1).(env.LocalEnvParams)
				assert.True(t, params.AllowReuse)
				require.NotNil(t, params.StartBranch)
				assert.Equal(t, "side/flow-branch", *params.StartBranch)
				wt := args.Get(2).(domain.Worktree)
				assert.Equal(t, domain.Worktree{Id: "wt_existing", FlowId: "flow_existing", Name: "side/flow-branch", WorkspaceId: "ws_test"}, wt)
			},
		},
		{
			envType:        env.EnvTypeDevPod,
			createActivity: "CreateDevPodWorktree",
			wantEnvType:    env.EnvTypeDevPod,
			wantWorkingDir: "/workspaces/sidekick-worktrees/side/flow-branch",
			assertCreateCall: func(t *testing.T, args mock.Arguments) {
				input := args.Get(1).(env.CreateDevPodWorktreeInput)
				assert.True(t, input.AllowReuse)
				assert.Equal(t, "side/flow-branch", input.BranchName)
				assert.Equal(t, "side/flow-branch", input.StartBranch)
				assert.Equal(t, "custom-ws", input.EnvContainer.Env.(*env.DevPodEnv).WorkspaceName)
			},
		},
		{
			envType:        env.EnvTypeOpenShell,
			createActivity: "CreateRemoteWorktree",
			wantEnvType:    env.EnvTypeOpenShell,
			wantWorkingDir: "/home/user/sidekick-worktrees/side/flow-branch",
			assertCreateCall: func(t *testing.T, args mock.Arguments) {
				input := args.Get(1).(env.CreateRemoteWorktreeInput)
				assert.True(t, input.AllowReuse)
				assert.Equal(t, "side/flow-branch", input.BranchName)
				assert.Equal(t, ensureEnvTestRepoDir, input.LocalRepoDir)
			},
		},
		{
			envType:        env.EnvTypeModal,
			createActivity: "CreateRemoteWorktree",
			wantEnvType:    env.EnvTypeModal,
			wantWorkingDir: "/home/user/sidekick-worktrees/side/flow-branch",
			assertCreateCall: func(t *testing.T, args mock.Arguments) {
				input := args.Get(1).(env.CreateRemoteWorktreeInput)
				assert.True(t, input.AllowReuse)
				assert.Equal(t, "side/flow-branch", input.BranchName)
				assert.Equal(t, ensureEnvTestRepoDir, input.LocalRepoDir)
			},
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.envType), func(t *testing.T) {
			t.Parallel()
			mocks := &ensureEnvMocks{}
			existing := flowWorktree()
			result, err := runEnsureEnvTestWorkflow(t, mocks, ensureEnvTestInput{
				EnvType:     string(tc.envType),
				RepoMode:    string(env.RepoModeWorktree),
				StartBranch: existing.Name,
				RepoConfig:  repoConfig,
				AllowReuse:  true,
				Attach:      existing,
			})
			require.NoError(t, err)

			order, calls := mocks.recorded()
			require.Contains(t, calls, tc.createActivity)
			assert.NotContains(t, order, "GetRepoConfig")
			require.GreaterOrEqual(t, len(order), 2, "activity order: %v", order)
			assert.Equal(t, []string{tc.createActivity, "PersistWorktree"}, order[len(order)-2:])
			tc.assertCreateCall(t, calls[tc.createActivity])
			if syncArgs, ok := calls["SyncRepoToRemote"]; ok {
				assert.Contains(t, syncArgs.Get(1).(env.SyncRepoToRemoteInput).Branches, existing.Name)
			}

			wantWorktree := *existing
			wantWorktree.WorkingDirectory = tc.wantWorkingDir
			require.Contains(t, calls, "PersistWorktree")
			persisted := calls["PersistWorktree"].Get(1).(domain.Worktree)
			assert.True(t, wantWorktree.Created.Equal(persisted.Created))
			persisted.Created = wantWorktree.Created
			assert.Equal(t, wantWorktree, persisted)

			require.NotNil(t, result.Worktree)
			assert.Equal(t, tc.wantWorkingDir, result.Worktree.WorkingDirectory)
			assert.Equal(t, existing.Name, result.Worktree.Name)
			assert.Equal(t, existing.Id, result.Worktree.Id)
			assert.Equal(t, tc.wantEnvType, result.EnvContainer.Env.GetType())
			assert.Equal(t, tc.wantWorkingDir, result.EnvContainer.Env.GetWorkingDirectory())
		})
	}
}

func TestEnsureEnvFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		mocks       *ensureEnvMocks
		input       ensureEnvTestInput
		wantErr     string
		wantPersist bool
	}{
		{
			name:        "worktree persistence failure",
			mocks:       &ensureEnvMocks{persistErr: temporal.NewNonRetryableApplicationError("db down", "test", nil)},
			input:       ensureEnvTestInput{EnvType: string(env.EnvTypeDevPod), RepoMode: string(env.RepoModeWorktree), AllowReuse: true, Attach: flowWorktree()},
			wantErr:     "failed to persist worktree",
			wantPersist: true,
		},
		{
			name:    "worktree attach failure skips persistence",
			mocks:   &ensureEnvMocks{worktreeErr: temporal.NewNonRetryableApplicationError("worktree add failed", "test", nil)},
			input:   ensureEnvTestInput{EnvType: string(env.EnvTypeLocal), RepoMode: string(env.RepoModeWorktree), AllowReuse: true, Attach: flowWorktree()},
			wantErr: "failed to attach worktree side/flow-branch",
		},
		{
			name:    "worktree mode requires a strategy",
			mocks:   &ensureEnvMocks{},
			input:   ensureEnvTestInput{EnvType: string(env.EnvTypeLocal), RepoMode: string(env.RepoModeWorktree)},
			wantErr: "requires a worktree strategy",
		},
		{
			name:    "unsupported env type",
			mocks:   &ensureEnvMocks{},
			input:   ensureEnvTestInput{EnvType: "bogus"},
			wantErr: "unsupported environment type: bogus",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := runEnsureEnvTestWorkflow(t, tc.mocks, tc.input)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			order, _ := tc.mocks.recorded()
			assert.Equal(t, tc.wantPersist, containsString(order, "PersistWorktree"))
		})
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type permissionsTestResult struct {
	Permissions common.CommandPermissionConfig
}

func envDerivedPermissionsTestWorkflow(ctx workflow.Context, envType string) (permissionsTestResult, error) {
	ctx = utils.NoRetryCtx(ctx)
	permissions, err := envDerivedCommandPermissions(ctx, envType,
		common.RepoConfig{CommandPermissions: common.CommandPermissionConfig{AutoApprove: []common.CommandPattern{{Pattern: "repo-cmd"}}}},
		common.LocalPublicConfig{CommandPermissions: common.CommandPermissionConfig{AutoApprove: []common.CommandPattern{{Pattern: "local-cmd"}}}},
		domain.WorkspaceConfig{CommandPermissions: common.CommandPermissionConfig{AutoApprove: []common.CommandPattern{{Pattern: "workspace-cmd"}}}},
	)
	return permissionsTestResult{Permissions: permissions}, err
}

func TestEnvDerivedCommandPermissionsVersionGates(t *testing.T) {
	t.Parallel()
	layered := []common.CommandPattern{
		{Pattern: "local-cmd", Source: common.CommandPatternSourceLocalConfig},
		{Pattern: "repo-cmd", Source: common.CommandPatternSourceRepoConfig},
		{Pattern: "workspace-cmd", Source: common.CommandPatternSourceWorkspaceConfig},
	}
	activityBase := common.CommandPermissionConfig{AutoApprove: []common.CommandPattern{{Pattern: "activity-base-cmd"}}}
	inlineBase := common.TagCommandPatternSources(common.BaseCommandPermissions(), common.CommandPatternSourceBase).AutoApprove

	cases := []struct {
		name            string
		envType         string
		baseVersion     workflow.Version
		sandboxVersion  workflow.Version
		wantActivityEnv *string
		wantBase        []common.CommandPattern
	}{
		{
			name:            "isolated sandbox env uses sandbox base source",
			envType:         string(env.EnvTypeModal),
			baseVersion:     1,
			sandboxVersion:  1,
			wantActivityEnv: utils.Ptr(string(env.EnvTypeModal)),
			wantBase:        []common.CommandPattern{{Pattern: "activity-base-cmd", Source: common.CommandPatternSourceBaseIsolatedSandbox}},
		},
		{
			name:            "local env uses local base source",
			envType:         string(env.EnvTypeLocal),
			baseVersion:     1,
			sandboxVersion:  1,
			wantActivityEnv: utils.Ptr(string(env.EnvTypeLocal)),
			wantBase:        []common.CommandPattern{{Pattern: "activity-base-cmd", Source: common.CommandPatternSourceBase}},
		},
		{
			name:            "pre-sandbox version ignores env type",
			envType:         string(env.EnvTypeDevPod),
			baseVersion:     1,
			sandboxVersion:  workflow.DefaultVersion,
			wantActivityEnv: utils.Ptr(""),
			wantBase:        []common.CommandPattern{{Pattern: "activity-base-cmd", Source: common.CommandPatternSourceBase}},
		},
		{
			name:           "pre-activity version computes base inline",
			envType:        string(env.EnvTypeOpenShell),
			baseVersion:    workflow.DefaultVersion,
			sandboxVersion: 1,
			wantBase:       inlineBase,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var workflowSuite testsuite.WorkflowTestSuite
			wfEnv := workflowSuite.NewTestWorkflowEnvironment()
			wfEnv.SetWorkerOptions(utils.TestWorkerOptions())
			wfEnv.OnGetVersion("base-command-permissions-activity", workflow.DefaultVersion, 1).Return(tc.baseVersion)
			wfEnv.OnGetVersion("sandbox-command-permissions", workflow.DefaultVersion, 1).Return(tc.sandboxVersion)

			var activityInputs []common.BaseCommandPermissionsInput
			wfEnv.OnActivity(common.BaseCommandPermissionsActivity, mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					activityInputs = append(activityInputs, args.Get(1).(common.BaseCommandPermissionsInput))
				}).
				Return(activityBase, nil).Maybe()

			wfEnv.ExecuteWorkflow(envDerivedPermissionsTestWorkflow, tc.envType)
			require.True(t, wfEnv.IsWorkflowCompleted())
			require.NoError(t, wfEnv.GetWorkflowError())
			var result permissionsTestResult
			require.NoError(t, wfEnv.GetWorkflowResult(&result))

			if tc.wantActivityEnv == nil {
				assert.Empty(t, activityInputs)
			} else {
				assert.Equal(t, []common.BaseCommandPermissionsInput{{EnvType: *tc.wantActivityEnv}}, activityInputs)
			}
			assert.Equal(t, append(append([]common.CommandPattern{}, tc.wantBase...), layered...), result.Permissions.AutoApprove)
		})
	}
}
