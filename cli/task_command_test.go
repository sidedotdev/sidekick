package main

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func initGitRepo(t *testing.T, dir string, branch string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init", "-b", branch},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		cmd := exec.CommandContext(context.Background(), args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "command %v failed: %s", args, string(out))
	}
}

func TestParseFlowOptions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		args           []string
		needsGitRepo   bool
		gitBranch      string
		expectedOpts   map[string]interface{}
		expectedErrMsg string
	}{
		{
			name: "defaults",
			args: []string{},
			expectedOpts: map[string]interface{}{
				"determineRequirements": true,
			},
		},
		{
			name:         "worktree flag sets repoMode",
			args:         []string{"--worktree"},
			needsGitRepo: true,
			gitBranch:    "main",
			expectedOpts: map[string]interface{}{
				"determineRequirements": true,
				"repoMode":              "worktree",
				"startBranch":           "main",
			},
		},
		{
			name: "start-branch implies repoMode worktree",
			args: []string{"--start-branch", "feature"},
			expectedOpts: map[string]interface{}{
				"determineRequirements": true,
				"repoMode":              "worktree",
				"startBranch":           "feature",
			},
		},
		{
			name: "env-type flag sets envType",
			args: []string{"--env-type", "devpod"},
			expectedOpts: map[string]interface{}{
				"determineRequirements": true,
				"envType":               "devpod",
			},
		},
		{
			name: "repo-mode flag sets repoMode",
			args: []string{"--repo-mode", "in_place"},
			expectedOpts: map[string]interface{}{
				"determineRequirements": true,
				"repoMode":              "in_place",
			},
		},
		{
			name:         "repo-mode worktree detects branch",
			args:         []string{"--repo-mode", "worktree"},
			needsGitRepo: true,
			gitBranch:    "main",
			expectedOpts: map[string]interface{}{
				"determineRequirements": true,
				"repoMode":              "worktree",
				"startBranch":           "main",
			},
		},
		{
			name:         "worktree flag overrides repo-mode in_place",
			args:         []string{"--repo-mode", "in_place", "--worktree"},
			needsGitRepo: true,
			gitBranch:    "main",
			expectedOpts: map[string]interface{}{
				"determineRequirements": true,
				"repoMode":              "worktree",
				"startBranch":           "main",
			},
		},
		{
			name:         "env-type and worktree together",
			args:         []string{"--env-type", "devpod", "--worktree"},
			needsGitRepo: true,
			gitBranch:    "develop",
			expectedOpts: map[string]interface{}{
				"determineRequirements": true,
				"envType":               "devpod",
				"repoMode":              "worktree",
				"startBranch":           "develop",
			},
		},
		{
			name: "env-type and start-branch together",
			args: []string{"--env-type", "devpod", "--start-branch", "my-branch"},
			expectedOpts: map[string]interface{}{
				"determineRequirements": true,
				"envType":               "devpod",
				"repoMode":              "worktree",
				"startBranch":           "my-branch",
			},
		},
		{
			name: "backward compat local_git_worktree via flow-option",
			args: []string{"--flow-option", "envType=local_git_worktree", "--start-branch", "main"},
			expectedOpts: map[string]interface{}{
				"determineRequirements": true,
				"envType":               "local",
				"repoMode":              "worktree",
				"startBranch":           "main",
			},
		},
		{
			name: "no-requirements flag",
			args: []string{"--no-requirements"},
			expectedOpts: map[string]interface{}{
				"determineRequirements": false,
			},
		},
		{
			name: "disable-human-in-the-loop flag",
			args: []string{"--disable-human-in-the-loop"},
			expectedOpts: map[string]interface{}{
				"determineRequirements": true,
				"configOverrides":       map[string]interface{}{"disableHumanInTheLoop": true},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var currentDir string
			if tc.needsGitRepo {
				dir := t.TempDir()
				initGitRepo(t, dir, tc.gitBranch)
				currentDir = dir
			} else {
				currentDir = t.TempDir()
			}

			var result map[string]interface{}
			var parseErr error

			app := &cli.Command{
				Name: "test",
				Commands: []*cli.Command{
					{
						Name: "task",
						Flags: []cli.Flag{
							&cli.BoolFlag{Name: "disable-human-in-the-loop"},
							&cli.BoolFlag{Name: "async"},
							&cli.StringFlag{Name: "flow", Value: "basic_dev"},
							&cli.BoolFlag{Name: "plan", Aliases: []string{"p"}},
							&cli.StringFlag{Name: "flow-options", Value: `{"determineRequirements": true}`},
							&cli.StringSliceFlag{Name: "flow-option", Aliases: []string{"O"}},
							&cli.BoolFlag{Name: "no-requirements", Aliases: []string{"n"}},
							&cli.BoolFlag{Name: "worktree", Aliases: []string{"w"}},
							&cli.StringFlag{Name: "repo-mode"},
							&cli.StringFlag{Name: "start-branch", Aliases: []string{"B"}},
							&cli.StringFlag{Name: "env-type"},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							result, parseErr = parseFlowOptions(ctx, cmd, currentDir)
							return nil
						},
					},
				},
			}

			args := append([]string{"test", "task"}, tc.args...)
			err := app.Run(context.Background(), args)
			require.NoError(t, err)

			if tc.expectedErrMsg != "" {
				require.Error(t, parseErr)
				assert.Contains(t, parseErr.Error(), tc.expectedErrMsg)
			} else {
				require.NoError(t, parseErr)
				assert.Equal(t, tc.expectedOpts, result)
			}
		})
	}
}

func TestParseFlowOptions_WorktreeDetachedHead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	initGitRepo(t, dir, "main")

	// Detach HEAD
	cmd := exec.CommandContext(context.Background(), "git", "checkout", "--detach")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	var parseErr error
	app := &cli.Command{
		Name: "test",
		Commands: []*cli.Command{
			{
				Name: "task",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "disable-human-in-the-loop"},
					&cli.BoolFlag{Name: "async"},
					&cli.StringFlag{Name: "flow", Value: "basic_dev"},
					&cli.BoolFlag{Name: "plan", Aliases: []string{"p"}},
					&cli.StringFlag{Name: "flow-options", Value: `{"determineRequirements": true}`},
					&cli.StringSliceFlag{Name: "flow-option", Aliases: []string{"O"}},
					&cli.BoolFlag{Name: "no-requirements", Aliases: []string{"n"}},
					&cli.BoolFlag{Name: "worktree", Aliases: []string{"w"}},
					&cli.StringFlag{Name: "repo-mode"},
					&cli.StringFlag{Name: "start-branch", Aliases: []string{"B"}},
					&cli.StringFlag{Name: "env-type"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					_, parseErr = parseFlowOptions(ctx, cmd, dir)
					return nil
				},
			},
		},
	}

	// Suppress stderr from urfave/cli help output
	oldStderr := os.Stderr
	os.Stderr, _ = os.Open(os.DevNull)
	defer func() { os.Stderr = oldStderr }()

	runErr := app.Run(context.Background(), []string{"test", "task", "--worktree"})
	require.NoError(t, runErr)
	require.Error(t, parseErr)
	assert.Contains(t, parseErr.Error(), "cannot use worktree with detached HEAD state")
}
