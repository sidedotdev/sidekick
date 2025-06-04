package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/llm"
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
	"github.com/zalando/go-keyring"
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

func TestCheckConfig_CreatesPlaceholderFile(t *testing.T) {
	tmpDir, cleanup := setupTempDir(t)
	defer cleanup()

	// Mock stdin to provide input for promptAndSaveTestCommand
	restoreStdin := mockStdin("pytest\n")
	defer restoreStdin()

	_, checkResult, err := checkConfig(tmpDir)
	assert.NoError(t, err, "Expected no error when checking side.toml")
	assert.False(t, checkResult.hasTestCommands, "Expected hasTestCommands to be false when creating side.toml")

	err = ensureTestCommands(&common.RepoConfig{}, filepath.Join(tmpDir, "side.toml"))
	assert.NoError(t, err, "Expected no error when prompting for test command")

	configFilePath := filepath.Join(tmpDir, "side.toml")
	data, err := os.ReadFile(configFilePath)
	assert.NoError(t, err)
	assert.Contains(t, string(data), "command = \"pytest\"")
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

func TestEnsureAISecrets_AnthropicProviderSelected(t *testing.T) {
	keyring.MockInit()

	// Mock stdin to provide input for ensureAISecrets
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString("Anthropic\r\n")
		time.Sleep(100 * time.Millisecond) // wait a bit till next tui prompt
		w.WriteString("dummy-api-key-anthropic\r\n")
	}()
	defer func() {
		w.Close()
		os.Stdin = oldStdin
	}()

	providers, err := ensureAISecrets()
	assert.NoError(t, err)
	assert.Equal(t, []string{"Anthropic"}, providers)

	apiKey, err := keyring.Get("sidekick", llm.AnthropicApiKeySecretName)
	assert.NoError(t, err)
	assert.Equal(t, "dummy-api-key-anthropic", apiKey)

	// Ensure OpenAI key is not present
	_, err = keyring.Get("sidekick", llm.OpenaiApiKeySecretName)
	assert.Error(t, err)
}

func TestEnsureAISecrets_OpenAIProviderSelected(t *testing.T) {
	keyring.MockInit()

	// Mock stdin to provide input for ensureAISecrets
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString("OpenAI\r\n")
		time.Sleep(100 * time.Millisecond) // wait a bit till next tui prompt
		w.WriteString("dummy-api-key\r\n")
	}()
	defer func() {
		w.Close()
		os.Stdin = oldStdin
	}()

	providers, err := ensureAISecrets()
	assert.NoError(t, err)
	assert.Equal(t, []string{"OpenAI"}, providers)

	apiKey, err := keyring.Get("sidekick", llm.OpenaiApiKeySecretName)
	assert.NoError(t, err)
	assert.Equal(t, "dummy-api-key", apiKey)

	// Ensure Anthropic key is not present
	_, err = keyring.Get("sidekick", llm.AnthropicApiKeySecretName)
	assert.Error(t, err)
}

func TestEnsureAISecrets_UsesExistingKeyringValue(t *testing.T) {
	keyring.MockInit()

	service := "sidekick"
	expectedOpenAIKey := "existing-openai-key"
	expectedAnthropicKey := "existing-anthropic-key"

	err := keyring.Set(service, llm.OpenaiApiKeySecretName, expectedOpenAIKey)
	assert.NoError(t, err)
	err = keyring.Set(service, llm.AnthropicApiKeySecretName, expectedAnthropicKey)
	assert.NoError(t, err)

	// Mock stdin to provide input for ensureAISecrets
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString("OpenAI\r\n")
	}()
	defer func() {
		w.Close()
		os.Stdin = oldStdin
	}()

	providers, err := ensureAISecrets()
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"OpenAI", "Anthropic"}, providers)

	retrievedOpenAIKey, err := keyring.Get(service, llm.OpenaiApiKeySecretName)
	assert.NoError(t, err)
	assert.Equal(t, expectedOpenAIKey, retrievedOpenAIKey)

	retrievedAnthropicKey, err := keyring.Get(service, llm.AnthropicApiKeySecretName)
	assert.NoError(t, err)
	assert.Equal(t, expectedAnthropicKey, retrievedAnthropicKey)
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

func TestEnsureTestCommands(t *testing.T) {
	tmpDir, cleanup := setupTempDir(t)
	defer cleanup()

	// Mock stdin to provide input for promptAndSaveTestCommand
	restoreStdin := mockStdin("pytest\n")
	defer restoreStdin()

	err := ensureTestCommands(&common.RepoConfig{}, filepath.Join(tmpDir, "side.toml"))
	assert.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "side.toml"))
	assert.NoError(t, err)
	assert.Contains(t, string(data), "command = \"pytest\"")
}

func TestEnsureWorkspaceConfig(t *testing.T) {
	ctx := context.Background()

	//testDB := redis.NewTestRedisStorage()
	testDB := sqlite.NewTestStorage(t, "cli_test")

	// Create a new InitCommandHandler with the test database
	handler := NewInitCommandHandler(testDB)

	workspace, err := handler.findOrCreateWorkspace(ctx, "test", "/tmp/test")
	require.NoError(t, err)
	workspaceID := workspace.Id

	// Test case 1: New configuration
	t.Run("New configuration", func(t *testing.T) {
		llmProviders := []string{"openai", "anthropic"}
		embeddingProviders := []string{"openai"}

		err := handler.ensureWorkspaceConfig(ctx, workspaceID, nil, llmProviders, embeddingProviders)
		if err != nil {
			t.Fatalf("ensureWorkspaceConfig failed: %v", err)
		}

		// Retrieve the persisted configuration
		config, err := testDB.GetWorkspaceConfig(ctx, workspaceID)
		if err != nil {
			t.Fatalf("Failed to retrieve workspace config: %v", err)
		}

		// Check LLM configuration
		if len(config.LLM.Defaults) != 2 {
			t.Errorf("Expected 2 LLM providers, got %d", len(config.LLM.Defaults))
		}
		if config.LLM.Defaults[0].Provider != "openai" || config.LLM.Defaults[1].Provider != "anthropic" {
			t.Errorf("Unexpected LLM providers: %v", config.LLM.Defaults)
		}

		// Check Embedding configuration
		if len(config.Embedding.Defaults) != 1 {
			t.Errorf("Expected 1 Embedding provider, got %d", len(config.Embedding.Defaults))
		}
		if config.Embedding.Defaults[0].Provider != "openai" {
			t.Errorf("Unexpected Embedding provider: %v", config.Embedding.Defaults)
		}
	})

	// Test case 2: Update existing configuration
	t.Run("Update existing configuration", func(t *testing.T) {
		existingConfig := &domain.WorkspaceConfig{
			LLM: common.LLMConfig{
				Defaults: []common.ModelConfig{{Provider: "old-provider"}},
			},
			Embedding: common.EmbeddingConfig{
				Defaults: []common.ModelConfig{{Provider: "old-provider"}},
			},
		}

		llmProviders := []string{"new-provider"}
		embeddingProviders := []string{"new-provider"}

		err := handler.ensureWorkspaceConfig(ctx, workspaceID, existingConfig, llmProviders, embeddingProviders)
		if err != nil {
			t.Fatalf("ensureWorkspaceConfig failed: %v", err)
		}

		// Retrieve the persisted configuration
		config, err := testDB.GetWorkspaceConfig(ctx, workspaceID)
		if err != nil {
			t.Fatalf("Failed to retrieve workspace config: %v", err)
		}

		// Check LLM configuration
		if len(config.LLM.Defaults) != 1 {
			t.Errorf("Expected 1 LLM provider, got %d", len(config.LLM.Defaults))
		}
		if config.LLM.Defaults[0].Provider != "new-provider" {
			t.Errorf("Unexpected LLM provider: %v", config.LLM.Defaults)
		}

		// Check Embedding configuration
		if len(config.Embedding.Defaults) != 1 {
			t.Errorf("Expected 1 Embedding provider, got %d", len(config.Embedding.Defaults))
		}
		if config.Embedding.Defaults[0].Provider != "new-provider" {
			t.Errorf("Unexpected Embedding provider: %v", config.Embedding.Defaults)
		}
	})
}
