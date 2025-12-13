package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestHelpFlags(t *testing.T) {
	// Save original os.Args
	oldOsArgs := os.Args
	defer func() { os.Args = oldOsArgs }()

	tests := []struct {
		name     string
		args     []string // Arguments to pass to the CLI application, including program name
		exitCode int
		contains []string
	}{
		{
			name:     "long help flag for root",
			args:     []string{"side", "--help"},
			exitCode: 0,
			contains: []string{
				"NAME:",
				"side - CLI for Sidekick",
				"USAGE:",
				"COMMANDS:",
				"init",
				"start",
				"version",
				"task",
			},
		},
		{
			name:     "short help flag for root",
			args:     []string{"side", "-h"},
			exitCode: 0,
			contains: []string{
				"NAME:",
				"side - CLI for Sidekick",
				"USAGE:",
				"COMMANDS:",
				"init",
				"start",
				"version",
				"task",
			},
		},
		{
			name:     "help command",
			args:     []string{"side help"},
			exitCode: 0,
			contains: []string{
				"NAME:",
				"side - CLI for Sidekick",
				"USAGE:",
				"COMMANDS:",
				"init",
				"start",
				"version",
				"task",
			},
		},
		{
			name:     "no args shows root command help",
			args:     []string{"side"},
			exitCode: 0,
			contains: []string{
				"NAME:",
				"side - CLI for Sidekick",
				"USAGE:",
				"COMMANDS:",
				"init",
				"start",
				"version",
				"task",
			},
		},
		{
			name:     "task help subcommand",
			args:     []string{"side", "task", "help"},
			exitCode: 0,
			contains: []string{
				"NAME:",
				"side task - ",
				"USAGE:",
				"side task [options] <task description>",
			},
		},
		{
			name:     "task command long help flag",
			args:     []string{"side", "task", "--help"},
			exitCode: 0,
			contains: []string{
				"NAME:",
				"side task - ",
				"USAGE:",
				"side task [options] <task description>",
			},
		},
		{
			name:     "task command short help flag",
			args:     []string{"side", "task", "-h"},
			exitCode: 0,
			contains: []string{
				"NAME:",
				"side task - ",
				"USAGE:",
				"side task [options] <task description>",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture stdout
			oldStdout := os.Stdout
			r, w, pipeErr := os.Pipe()
			require.NoError(t, pipeErr)
			os.Stdout = w

			// Run the CLI app by calling the setup function from cli.go
			// tt.args already includes the program name as os.Args[0]
			runErr := setupAndRunInteractiveCli(tt.args)
			// Restore stdout and read output
			errClose := w.Close()
			require.NoError(t, errClose)
			os.Stdout = oldStdout

			var buf bytes.Buffer
			_, errCopy := io.Copy(&buf, r)
			require.NoError(t, errCopy)
			output := buf.String()

			// Check exit code based on the error returned by app.Run
			if tt.exitCode == 0 {
				if runErr != nil {
					t.Errorf("expected exit code 0 (nil error), got error: %v. Output:\n%s", runErr, output)
				}
			} else {
				if runErr == nil {
					t.Errorf("expected exit code %d (non-nil error), but got nil error. Output:\n%s", tt.exitCode, output)
				} else {
					exitErr, ok := runErr.(cli.ExitCoder)
					if !ok {
						// urfave/cli/v3 might return a generic error for some parsing issues, often implying exit code 1
						// For this test, we are specific about cli.ExitCoder if tt.exitCode is not 0.
						// If it's a general error, it might not be an ExitCoder.
						// Defaulting to checking if it's a non-zero exit if not ExitCoder might be too broad.
						// For now, strict check for ExitCoder if non-zero expected.
						t.Errorf("expected error to be cli.ExitCoder for non-zero exit, but it's type %T. Error: %v. Output:\n%s", runErr, runErr, output)
					} else if exitErr.ExitCode() != tt.exitCode {
						t.Errorf("expected exit code %d, got %d. Error: %v. Output:\n%s", tt.exitCode, exitErr.ExitCode(), runErr, output)
					}
				}
			}

			// Check output contains expected strings
			for _, s := range tt.contains {
				assert.Contains(t, output, s, "Output does not contain expected string %q", s)
			}
		})
	}
}

// Helper function to create a temporary directory and change to it
func setupTempDir(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir := t.TempDir()

	currentDir, err := os.Getwd()
	assert.NoError(t, err)

	err = os.Chdir(tmpDir)
	assert.NoError(t, err)

	cleanup := func() {
		os.Chdir(currentDir)
	}

	return tmpDir, cleanup
}

// Helper function to mock stdin
func mockStdin(input string) func() {
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	w.WriteString(input)
	w.Close()
	return func() {
		os.Stdin = oldStdin
	}
}

func TestIsGitRepo_FalseWhenNotGitRepo(t *testing.T) {
	tmpDir, cleanup := setupTempDir(t)
	defer cleanup()

	isRepo, err := isGitRepo(tmpDir)
	assert.NoError(t, err)
	assert.False(t, isRepo, "Expected to be false when not a git repository")
}

func TestIsGitRepo_TrueWhenGitRepo(t *testing.T) {
	tmpDir, cleanup := setupTempDir(t)
	defer cleanup()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	err := cmd.Run()
	assert.NoError(t, err)

	isRepo, err := isGitRepo(tmpDir)
	assert.NoError(t, err)
	assert.True(t, isRepo, "Expected to be true when inside a git repository")
}

func TestIsGitRepo_ErrorWhenGitCommandNotExist(t *testing.T) {
	tmpDir, cleanup := setupTempDir(t)
	defer cleanup()

	originalPath := os.Getenv("PATH")
	defer os.Setenv("PATH", originalPath)

	// Temporarily remove the directory containing the git command from PATH
	os.Setenv("PATH", "")

	_, err := isGitRepo(tmpDir)
	assert.Error(t, err, "Expected an error when git command does not exist")
	assert.Contains(t, err.Error(), "executable file not found", "Expected error to indicate missing git command")
}

func TestGetGitBaseDirectory(t *testing.T) {
	tmpDir, cleanup := setupTempDir(t)
	defer cleanup()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	err := cmd.Run()
	assert.NoError(t, err)

	baseDir, err := getGitBaseDirectory()
	assert.NoError(t, err)

	// Normalize both paths to resolve any symbolic links
	normalizedTmpDir, err := filepath.EvalSymlinks(tmpDir)
	assert.NoError(t, err)
	normalizedBaseDir, err := filepath.EvalSymlinks(baseDir)
	assert.NoError(t, err)

	assert.Equal(t, normalizedTmpDir, normalizedBaseDir, "Expected the base directory to be the initialized Git repo")
}

func TestCheckConfig_NoExistingFile(t *testing.T) {
	tmpDir, cleanup := setupTempDir(t)
	defer cleanup()

	_, checkResult, err := checkConfig(tmpDir)
	assert.NoError(t, err, "Expected no error when checking side.toml")
	assert.False(t, checkResult.hasTestCommands, "Expected hasTestCommands to be false when no file exists")
	assert.Equal(t, filepath.Join(tmpDir, "side.toml"), checkResult.filePath)
}

func TestCheckConfig_ValidFile(t *testing.T) {
	tmpDir, cleanup := setupTempDir(t)
	defer cleanup()

	configFilePath := filepath.Join(tmpDir, "side.toml")
	err := os.WriteFile(configFilePath, []byte("[[test_commands]]\ncommand = \"jest\"\n\n[ai]\ndefault=[{provider = \"openai\"}]"), 0644)
	assert.NoError(t, err)

	_, checkResult, err := checkConfig(tmpDir)
	assert.NoError(t, err, "Expected no error with valid side.toml")
	assert.True(t, checkResult.hasTestCommands, "Expected hasTestCommands to be true with valid side.toml")
}

func TestSaveConfig_CreatesFileWithCorrectContent(t *testing.T) {
	tmpDir, cleanup := setupTempDir(t)
	defer cleanup()

	config := common.RepoConfig{
		TestCommands: []common.CommandConfig{
			{Command: "test-command"},
		},
	}

	err := saveConfig(filepath.Join(tmpDir, "side.toml"), config)
	assert.NoError(t, err)

	configFilePath := filepath.Join(tmpDir, "side.toml")
	data, err := os.ReadFile(configFilePath)
	assert.NoError(t, err)
	assert.Contains(t, string(data), "command = \"test-command\"")
}

func TestEnsureTestCommands_UserEntersCommand(t *testing.T) {
	tmpDir, cleanup := setupTempDir(t)
	defer cleanup()

	configPath := filepath.Join(tmpDir, "side.toml")

	// Mock stdin: enter test command
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString("pytest\n")
		w.Close()
	}()
	defer func() {
		os.Stdin = oldStdin
	}()

	config := &common.RepoConfig{}
	err := ensureTestCommands(config, configPath)
	assert.NoError(t, err)
	assert.Len(t, config.TestCommands, 1)
	assert.Equal(t, "pytest", config.TestCommands[0].Command)

	data, err := os.ReadFile(configPath)
	assert.NoError(t, err)
	assert.Contains(t, string(data), "command = \"pytest\"")
}

func TestEnsureTestCommands_UserSkips(t *testing.T) {
	tmpDir, cleanup := setupTempDir(t)
	defer cleanup()

	configPath := filepath.Join(tmpDir, "side.toml")

	// Mock stdin: type "skip"
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString("skip\n")
		w.Close()
	}()
	defer func() {
		os.Stdin = oldStdin
	}()

	config := &common.RepoConfig{}
	err := ensureTestCommands(config, configPath)
	assert.NoError(t, err)
	assert.Empty(t, config.TestCommands)

	data, err := os.ReadFile(configPath)
	assert.NoError(t, err)
	assert.Contains(t, string(data), "# Uncomment and configure test commands for best results:")
	assert.Contains(t, string(data), "# [[test_commands]]")
	assert.Contains(t, string(data), "# command = \"pytest\"")
}

func TestEnsureTestCommands_UserSkipsCaseInsensitive(t *testing.T) {
	tmpDir, cleanup := setupTempDir(t)
	defer cleanup()

	configPath := filepath.Join(tmpDir, "side.toml")

	// Mock stdin: type "SKIP" (uppercase)
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString("SKIP\n")
		w.Close()
	}()
	defer func() {
		os.Stdin = oldStdin
	}()

	config := &common.RepoConfig{}
	err := ensureTestCommands(config, configPath)
	assert.NoError(t, err)
	assert.Empty(t, config.TestCommands)

	data, err := os.ReadFile(configPath)
	assert.NoError(t, err)
	assert.Contains(t, string(data), "# Uncomment and configure test commands for best results:")
}

func TestEnsureWorkspaceConfig(t *testing.T) {
	ctx := context.Background()

	testDB := sqlite.NewTestSqliteStorage(t, "cli_test")

	handler := NewInitCommandHandler(testDB)

	workspace, err := handler.findOrCreateWorkspace(ctx, "test", "/tmp/test")
	require.NoError(t, err)
	workspaceID := workspace.Id

	t.Run("New configuration with single providers", func(t *testing.T) {
		err := handler.ensureWorkspaceConfig(ctx, workspaceID, nil, "openai", "openai")
		require.NoError(t, err)

		config, err := testDB.GetWorkspaceConfig(ctx, workspaceID)
		require.NoError(t, err)

		assert.Len(t, config.LLM.Defaults, 1)
		assert.Equal(t, "openai", config.LLM.Defaults[0].Provider)

		assert.Len(t, config.Embedding.Defaults, 1)
		assert.Equal(t, "openai", config.Embedding.Defaults[0].Provider)
	})

	t.Run("Update existing configuration", func(t *testing.T) {
		existingConfig := &domain.WorkspaceConfig{
			LLM: common.LLMConfig{
				Defaults: []common.ModelConfig{{Provider: "old-provider"}},
			},
			Embedding: common.EmbeddingConfig{
				Defaults: []common.ModelConfig{{Provider: "old-provider"}},
			},
		}

		err := handler.ensureWorkspaceConfig(ctx, workspaceID, existingConfig, "anthropic", "google")
		require.NoError(t, err)

		config, err := testDB.GetWorkspaceConfig(ctx, workspaceID)
		require.NoError(t, err)

		assert.Len(t, config.LLM.Defaults, 1)
		assert.Equal(t, "anthropic", config.LLM.Defaults[0].Provider)

		assert.Len(t, config.Embedding.Defaults, 1)
		assert.Equal(t, "google", config.Embedding.Defaults[0].Provider)
	})

	t.Run("Empty providers result in empty defaults", func(t *testing.T) {
		err := handler.ensureWorkspaceConfig(ctx, workspaceID, nil, "", "")
		require.NoError(t, err)

		config, err := testDB.GetWorkspaceConfig(ctx, workspaceID)
		require.NoError(t, err)

		assert.Empty(t, config.LLM.Defaults)
		assert.Empty(t, config.Embedding.Defaults)
	})
}
