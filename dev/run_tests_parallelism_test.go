package dev

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/env"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func TestRunTestsEnvironmentParallelism(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		envType   env.EnvType
		configEnv string
		limit     int
		wantPeak  int
		legacy    bool
		heartbeat bool
		failTest  bool
	}{
		{name: "local", envType: env.EnvTypeLocal, limit: 2, wantPeak: 2},
		{name: "worktree", envType: env.EnvTypeLocalGitWorktree, limit: 2, wantPeak: 2},
		{name: "devpod", envType: env.EnvTypeDevPod, limit: 1, wantPeak: 1},
		{name: "openshell", envType: env.EnvTypeOpenShell, limit: 2, wantPeak: 2},
		{name: "modal", envType: env.EnvTypeModal, limit: 2, wantPeak: 2},
		{name: "unlimited", envType: env.EnvTypeLocal, limit: 0, wantPeak: 5},
		{name: "other environment unaffected", envType: env.EnvTypeModal, configEnv: "devpod", limit: 1, wantPeak: 5},
		{name: "limit exceeds command count", envType: env.EnvTypeLocal, limit: 10, wantPeak: 5},
		{name: "old workflow remains unlimited", envType: env.EnvTypeLocal, limit: 1, wantPeak: 5, legacy: true},
		{name: "heartbeat retries", envType: env.EnvTypeLocal, limit: 2, wantPeak: 2, heartbeat: true},
		{name: "test failure does not stop queue", envType: env.EnvTypeLocal, limit: 1, wantPeak: 1, failTest: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := new(RunTestsTestSuite)
			s.SetT(t)
			s.SetupTest()
			t.Cleanup(func() { s.AfterTest("", "") })

			configEnv := tt.configEnv
			if configEnv == "" {
				configEnv = string(tt.envType)
			}
			config := fmt.Sprintf("%s:\n  max_parallel_test_commands: %d\n", configEnv, tt.limit)
			dir := s.envContainer.Env.GetWorkingDirectory()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "side.yml"), []byte(config), 0600))
			var err error
			s.devContext.RepoConfig, err = GetRepoConfigActivity(s.envContainer)
			require.NoError(t, err)
			for i := 0; i < 5; i++ {
				s.devContext.RepoConfig.TestCommands = append(s.devContext.RepoConfig.TestCommands,
					common.CommandConfig{Command: fmt.Sprintf("command-%d", i)})
			}

			switch tt.envType {
			case env.EnvTypeLocalGitWorktree:
				s.envContainer.Env = &env.LocalGitWorktreeEnv{}
			case env.EnvTypeDevPod:
				s.envContainer.Env = &env.DevPodEnv{}
			case env.EnvTypeOpenShell:
				s.envContainer.Env = &env.OpenShellEnv{}
			case env.EnvTypeModal:
				s.envContainer.Env = &env.ModalEnv{}
			}
			if tt.legacy {
				s.env.OnGetVersion("max-parallel-test-commands", workflow.DefaultVersion, workflow.Version(1)).
					Return(workflow.DefaultVersion).Maybe()
			}

			var mu sync.Mutex
			running, peak := 0, 0
			calls := make(map[string]int)
			s.env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
				if info.ActivityType.Name != "EnvRunCommandActivity" {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				running++
				peak = max(peak, running)
			})
			s.env.SetOnActivityCompletedListener(func(info *activity.Info, _ converter.EncodedValue, _ error) {
				if info.ActivityType.Name != "EnvRunCommandActivity" {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				running--
			})
			s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
				After(time.Second).
				Return(func(_ context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
					mu.Lock()
					defer mu.Unlock()
					command := input.Args[2]
					calls[command]++
					if tt.heartbeat && calls[command] == 1 {
						return env.EnvRunCommandActivityOutput{}, temporal.NewTimeoutError(enumspb.TIMEOUT_TYPE_HEARTBEAT, nil)
					}
					if tt.failTest && command == "command-0" {
						return env.EnvRunCommandActivityOutput{ExitStatus: 1, Stderr: "test failed"}, nil
					}
					return env.EnvRunCommandActivityOutput{}, nil
				})

			s.env.ExecuteWorkflow(s.wrapperWorkflow)
			require.NoError(t, s.env.GetWorkflowError())
			var result TestResult
			require.NoError(t, s.env.GetWorkflowResult(&result))
			require.Equal(t, !tt.failTest, result.TestsPassed)
			require.Equal(t, tt.wantPeak, peak)
			require.Zero(t, running)
			require.Len(t, calls, 5)
			for command, count := range calls {
				want := 1
				if tt.heartbeat {
					want = 2
				}
				require.Equal(t, want, count, command)
				require.Contains(t, result.Output, "Test Command: "+command)
			}
		})
	}
}

func TestRepoConfigRejectsNegativeTestCommandLimits(t *testing.T) {
	t.Parallel()
	for _, envType := range []string{"local", "local_git_worktree", "devpod", "openshell", "modal"} {
		for _, format := range []string{"yml", "toml", "json"} {
			t.Run(envType+"/"+format, func(t *testing.T) {
				t.Parallel()
				var content string
				switch format {
				case "yml":
					content = fmt.Sprintf("%s:\n  max_parallel_test_commands: -1\n", envType)
				case "toml":
					content = fmt.Sprintf("[%s]\nmax_parallel_test_commands = -1\n", envType)
				case "json":
					content = fmt.Sprintf(`{"%s":{"max_parallel_test_commands":-1}}`, envType)
				}
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "side."+format), []byte(content), 0600))
				local, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: dir})
				require.NoError(t, err)
				container := env.EnvContainer{Env: local}
				_, legacyErr := GetRepoConfigActivity(container)
				_, currentErr := GetRepoConfigActivityV2(container)
				for _, err := range []error{legacyErr, currentErr} {
					require.Error(t, err)
					require.True(t, strings.Contains(err.Error(), envType+".max_parallel_test_commands"), err.Error())
				}
			})
		}
	}
}
