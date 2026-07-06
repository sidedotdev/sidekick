package dev

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sidekick/common"
	"sidekick/env"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// remoteLikeEnv simulates an environment (e.g. DevPod) whose working directory
// does not exist on the local filesystem, serving files from a separate backing
// directory via the env's own Stat/ReadFile. It exists to verify that repo
// config discovery resolves files through the env filesystem rather than the
// local OS.
type remoteLikeEnv struct {
	*mockEnv
	remoteWorkingDir string
	backingDir       string
}

func (e *remoteLikeEnv) GetWorkingDirectory() string { return e.remoteWorkingDir }

func (e *remoteLikeEnv) resolve(p string) string {
	// Mirror real Env implementations, which resolve relative paths against
	// the working directory before hitting the (remote) filesystem.
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.remoteWorkingDir, p)
	}
	if rel, err := filepath.Rel(e.remoteWorkingDir, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join(e.backingDir, rel)
	}
	return p
}

func (e *remoteLikeEnv) Stat(ctx context.Context, p string) (fs.FileInfo, error) {
	return os.Stat(e.resolve(p))
}

func (e *remoteLikeEnv) ReadFile(ctx context.Context, p string) ([]byte, error) {
	return os.ReadFile(e.resolve(p))
}

// mockEnv is a simple implementation of env.Env for testing purposes.
type mockEnv struct {
	workingDir string
}

// defaultConfigFilename is the default repo config filename used in tests
const defaultConfigFilename = "side.yml"

func (m *mockEnv) GetType() env.EnvType {
	// Using EnvTypeLocal as a placeholder, the specific type doesn't matter for this test
	return env.EnvTypeLocal
}

func (m *mockEnv) GetWorkingDirectory() string {
	return m.workingDir
}

// RunCommand is not needed for GetRepoConfigActivity tests.
func (m *mockEnv) RunCommand(ctx context.Context, input env.EnvRunCommandInput) (env.EnvRunCommandOutput, error) {
	panic("RunCommand should not be called in this test")
}

func (m *mockEnv) Walk(ctx context.Context, ignoreFileNames []string, handleEntry func(path string, isDir bool) error) error {
	return (&env.LocalEnv{WorkingDirectory: m.workingDir}).Walk(ctx, ignoreFileNames, handleEntry)
}

func (m *mockEnv) ReadFile(ctx context.Context, p string) ([]byte, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(m.workingDir, p)
	}
	return os.ReadFile(p)
}

func (m *mockEnv) ReadDir(ctx context.Context, path string) ([]fs.DirEntry, error) {
	return os.ReadDir(path)
}

func (m *mockEnv) WriteFile(ctx context.Context, p string, data []byte, perm fs.FileMode) error {
	if !filepath.IsAbs(p) {
		p = filepath.Join(m.workingDir, p)
	}
	return os.WriteFile(p, data, perm)
}

func (m *mockEnv) MkdirAll(ctx context.Context, p string, perm fs.FileMode) error {
	if !filepath.IsAbs(p) {
		p = filepath.Join(m.workingDir, p)
	}
	return os.MkdirAll(p, perm)
}

func (m *mockEnv) Stat(ctx context.Context, p string) (fs.FileInfo, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(m.workingDir, p)
	}
	return os.Stat(p)
}

func (m *mockEnv) Remove(ctx context.Context, p string) error {
	if !filepath.IsAbs(p) {
		p = filepath.Join(m.workingDir, p)
	}
	return os.Remove(p)
}

func (m *mockEnv) CreateTemp(ctx context.Context, dir, pattern string) (string, error) {
	if dir != "" && !filepath.IsAbs(dir) {
		dir = filepath.Join(m.workingDir, dir)
	}
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	if cerr := f.Close(); cerr != nil {
		return name, cerr
	}
	return name, nil
}
func (m *mockEnv) Hibernate(ctx context.Context, branchName string) (env.HibernationMetadata, error) {
	return env.HibernationMetadata{}, fmt.Errorf("hibernate not supported on mock env")
}

func (m *mockEnv) WakeIfHibernated(ctx context.Context) error { return nil }

// setupTestEnv creates a test environment with a repo config file and optional hints file.
// configFilename defaults to "side.yml" if empty.
func setupTestEnv(t *testing.T, configContent string, hintsFilename string, hintsContent string) env.EnvContainer {
	return setupTestEnvWithFilename(t, defaultConfigFilename, configContent, hintsFilename, hintsContent)
}

// setupTestEnvWithFilename creates a test environment with a specified config filename.
func setupTestEnvWithFilename(t *testing.T, configFilename string, configContent string, hintsFilename string, hintsContent string) env.EnvContainer {
	t.Helper()
	tempDir := t.TempDir()

	// Create config file
	configPath := filepath.Join(tempDir, configFilename)
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	require.NoError(t, err, "Failed to write config file")

	// Create hints file if needed
	if hintsFilename != "" {
		hintsFilePath := filepath.Join(tempDir, hintsFilename)
		err = os.WriteFile(hintsFilePath, []byte(hintsContent), 0644)
		require.NoError(t, err, "Failed to write hints file")
	}

	mock := &mockEnv{workingDir: tempDir}
	return env.EnvContainer{Env: mock}
}

func writeFallbackFile(t *testing.T, envContainer env.EnvContainer, relativePath string, content string) {
	t.Helper()
	workingDir := envContainer.Env.GetWorkingDirectory()
	absPath := filepath.Join(workingDir, relativePath)

	dir := filepath.Dir(absPath)
	if dir != "" && dir != "." {
		err := os.MkdirAll(dir, 0755)
		require.NoError(t, err)
	}

	err := os.WriteFile(absPath, []byte(content), 0644)
	require.NoError(t, err)
}

func TestGetRepoConfigActivity(t *testing.T) {
	t.Run("Scenario 1: Inline hints set, hints_path unset", func(t *testing.T) {
		yamlContent := `
edit_code:
  hints: "This is an inline hint."
`
		envContainer := setupTestEnv(t, yamlContent, "", "")

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "This is an inline hint.", config.EditCode.Hints)
		assert.Empty(t, config.EditCode.HintsPath)
	})

	t.Run("Scenario 2: Inline hints empty, hints_path set, file exists", func(t *testing.T) {
		hintsFilename := "actual_hints.txt"
		hintsContent := "This hint comes from the file."
		yamlContent := `
edit_code:
  hints_path: "` + hintsFilename + `"
`
		envContainer := setupTestEnv(t, yamlContent, hintsFilename, hintsContent)

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, hintsContent, config.EditCode.Hints)
		assert.Equal(t, hintsFilename, config.EditCode.HintsPath)
	})

	t.Run("Scenario 3: Inline hints set, hints_path set", func(t *testing.T) {
		hintsFilename := "other_hints.txt"
		hintsContent := "This hint should be ignored."
		yamlContent := `
edit_code:
  hints: "Inline hint takes precedence."
  hints_path: "` + hintsFilename + `"
`
		envContainer := setupTestEnv(t, yamlContent, hintsFilename, hintsContent)

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "Inline hint takes precedence.", config.EditCode.Hints)
		assert.Equal(t, hintsFilename, config.EditCode.HintsPath)
	})

	t.Run("Scenario 4: Inline hints empty, hints_path set, file missing", func(t *testing.T) {
		missingHintsFilename := "non_existent_hints.txt"
		yamlContent := `
edit_code:
  hints_path: "` + missingHintsFilename + `"
`
		envContainer := setupTestEnv(t, yamlContent, "", "")

		_, err := GetRepoConfigActivity(envContainer)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read hints file")
		assert.Contains(t, err.Error(), missingHintsFilename)
	})

	t.Run("Scenario 5: Both hints and hints_path empty", func(t *testing.T) {
		// Scenario 5a: Empty edit_code section
		yamlContentEmptySection := `
edit_code: {}
`
		envContainerEmptySection := setupTestEnv(t, yamlContentEmptySection, "", "")
		configEmptySection, errEmptySection := GetRepoConfigActivity(envContainerEmptySection)
		require.NoError(t, errEmptySection)
		assert.Empty(t, configEmptySection.EditCode.Hints)
		assert.Empty(t, configEmptySection.EditCode.HintsPath)

		// Scenario 5b: No edit_code section
		yamlContentNoSection := `
mission: "Test mission"
`
		envContainerNoSection := setupTestEnv(t, yamlContentNoSection, "", "")
		configNoSection, errNoSection := GetRepoConfigActivity(envContainerNoSection)
		require.NoError(t, errNoSection)
		assert.Empty(t, configNoSection.EditCode.Hints)
		assert.Empty(t, configNoSection.EditCode.HintsPath)

		// Scenario 5c: Empty file
		yamlContentEmptyFile := ``
		envContainerEmptyFile := setupTestEnv(t, yamlContentEmptyFile, "", "")
		configEmptyFile, errEmptyFile := GetRepoConfigActivity(envContainerEmptyFile)
		require.NoError(t, errEmptyFile)
		assert.Empty(t, configEmptyFile.EditCode.Hints)
		assert.Empty(t, configEmptyFile.EditCode.HintsPath)
	})

	t.Run("Scenario 6: Config parsing error", func(t *testing.T) {
		invalidYamlContent := `this: is: not: valid: yaml`
		envContainer := setupTestEnv(t, invalidYamlContent, "", "")

		_, err := GetRepoConfigActivity(envContainer)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse repo config")
	})

	t.Run("AgentConfig: deserializes auto_iterations for a use case", func(t *testing.T) {
		yamlContent := `
agent_config:
  coding:
    auto_iterations: 15
`
		envContainer := setupTestEnv(t, yamlContent, "", "")

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		require.NotNil(t, config.AgentConfig)
		require.Contains(t, config.AgentConfig, "coding")
		assert.Equal(t, 15, config.AgentConfig["coding"].AutoIterations)
	})

	t.Run("Handles missing config file", func(t *testing.T) {
		tempDir := t.TempDir()
		mock := &mockEnv{workingDir: tempDir}
		envContainer := env.EnvContainer{Env: mock}

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Empty(t, config.EditCode.Hints)
		assert.Empty(t, config.EditCode.HintsPath)
		assert.Empty(t, config.CheckCommands)
		assert.Empty(t, config.AutofixCommands)
		assert.Empty(t, config.TestCommands)
		assert.Empty(t, config.IntegrationTestCommands)
		assert.Empty(t, config.Mission)
		assert.False(t, config.DisableHumanInTheLoop)
		assert.Zero(t, config.MaxIterations)
		assert.Zero(t, config.MaxPlanningIterations)
		assert.Empty(t, config.WorktreeSetup)
	})
}
func TestGetRepoConfigActivity_FallbackPrecedence(t *testing.T) {
	candidates := []string{
		"AGENTS.md",
		"CLAUDE.md",
		"GEMINI.md",
		".github/copilot-instructions.md",
		".clinerules",
		".cursorrules",
		".windsurfrules",
		"CONVENTIONS.md",
	}

	for i, candidate := range candidates {
		candidate := candidate
		t.Run(candidate, func(t *testing.T) {
			envContainer := setupTestEnv(t, `
edit_code: {}
`, "", "")
			for j := i; j < len(candidates); j++ {
				name := candidates[j]
				writeFallbackFile(t, envContainer, name, "fallback for "+name)
			}

			config, err := GetRepoConfigActivity(envContainer)
			require.NoError(t, err)
			assert.Equal(t, "fallback for "+candidate, config.EditCode.Hints)
			assert.Equal(t, candidate, config.EditCode.HintsPath)
		})
	}
}

func TestGetRepoConfigActivity_FallbackSuppressedWhenHintsConfigured(t *testing.T) {
	t.Run("Inline hints override fallback", func(t *testing.T) {
		yamlContent := `
edit_code:
  hints: "Inline value"
`
		envContainer := setupTestEnv(t, yamlContent, "", "")
		writeFallbackFile(t, envContainer, "AGENTS.md", "fallback content")

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "Inline value", config.EditCode.Hints)
		assert.Empty(t, config.EditCode.HintsPath)
	})

	t.Run("Configured hints_path overrides fallback", func(t *testing.T) {
		hintsFilename := "repo_hints.txt"
		yamlContent := `
edit_code:
  hints_path: "` + hintsFilename + `"
`
		envContainer := setupTestEnv(t, yamlContent, hintsFilename, "configured file content")
		writeFallbackFile(t, envContainer, "AGENTS.md", "fallback content")

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "configured file content", config.EditCode.Hints)
		assert.Equal(t, hintsFilename, config.EditCode.HintsPath)
	})
}

func TestGetRepoConfigActivity_NoFallbackCandidates(t *testing.T) {
	envContainer := setupTestEnv(t, `
edit_code: {}
`, "", "")

	config, err := GetRepoConfigActivity(envContainer)

	require.NoError(t, err)
	assert.Empty(t, config.EditCode.Hints)
	assert.Empty(t, config.EditCode.HintsPath)
}

func TestGetRepoConfigActivity_LoadsFallbackHintsWhenConfigMissing(t *testing.T) {
	tempDir := t.TempDir()
	mock := &mockEnv{workingDir: tempDir}
	envContainer := env.EnvContainer{Env: mock}

	writeFallbackFile(t, envContainer, "AGENTS.md", "fallback agents content")

	config, err := GetRepoConfigActivity(envContainer)

	require.NoError(t, err)
	assert.Equal(t, "fallback agents content", config.EditCode.Hints)
	assert.Equal(t, "AGENTS.md", config.EditCode.HintsPath)
}

func TestGetRepoConfigActivity_ConfigFilePrecedence(t *testing.T) {
	t.Run("side.yml takes precedence over side.yaml", func(t *testing.T) {
		tempDir := t.TempDir()

		// Create both files with different missions
		err := os.WriteFile(filepath.Join(tempDir, "side.yml"), []byte("mission: from-yml"), 0644)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(tempDir, "side.yaml"), []byte("mission: from-yaml"), 0644)
		require.NoError(t, err)

		mock := &mockEnv{workingDir: tempDir}
		envContainer := env.EnvContainer{Env: mock}

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "from-yml", config.Mission)
	})

	t.Run("side.yaml takes precedence over side.toml", func(t *testing.T) {
		tempDir := t.TempDir()

		err := os.WriteFile(filepath.Join(tempDir, "side.yaml"), []byte("mission: from-yaml"), 0644)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(tempDir, "side.toml"), []byte("mission = \"from-toml\""), 0644)
		require.NoError(t, err)

		mock := &mockEnv{workingDir: tempDir}
		envContainer := env.EnvContainer{Env: mock}

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "from-yaml", config.Mission)
	})

	t.Run("side.toml takes precedence over side.json", func(t *testing.T) {
		tempDir := t.TempDir()

		err := os.WriteFile(filepath.Join(tempDir, "side.toml"), []byte("mission = \"from-toml\""), 0644)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(tempDir, "side.json"), []byte(`{"mission": "from-json"}`), 0644)
		require.NoError(t, err)

		mock := &mockEnv{workingDir: tempDir}
		envContainer := env.EnvContainer{Env: mock}

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "from-toml", config.Mission)
	})
}

func TestGetRepoConfigActivity_DifferentFormats(t *testing.T) {
	t.Run("loads TOML config", func(t *testing.T) {
		tomlContent := `
mission = "toml mission"

[edit_code]
hints = "toml hints"
`
		envContainer := setupTestEnvWithFilename(t, "side.toml", tomlContent, "", "")

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "toml mission", config.Mission)
		assert.Equal(t, "toml hints", config.EditCode.Hints)
	})

	t.Run("loads JSON config", func(t *testing.T) {
		jsonContent := `{
  "mission": "json mission",
  "edit_code": {
    "hints": "json hints"
  }
}`
		envContainer := setupTestEnvWithFilename(t, "side.json", jsonContent, "", "")

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "json mission", config.Mission)
		assert.Equal(t, "json hints", config.EditCode.Hints)
	})

	t.Run("loads side.yaml config", func(t *testing.T) {
		yamlContent := `
mission: yaml mission
edit_code:
  hints: yaml hints
`
		envContainer := setupTestEnvWithFilename(t, "side.yaml", yamlContent, "", "")

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "yaml mission", config.Mission)
		assert.Equal(t, "yaml hints", config.EditCode.Hints)
	})
}

func TestGetRepoConfigActivityV2(t *testing.T) {
	t.Run("returns chosen path when config exists", func(t *testing.T) {
		yamlContent := `
mission: test mission
`
		envContainer := setupTestEnv(t, yamlContent, "", "")

		result, err := GetRepoConfigActivityV2(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "test mission", result.Config.Mission)
		assert.Contains(t, result.ChosenPath, "side.yml")
	})

	t.Run("returns empty chosen path when no config exists", func(t *testing.T) {
		tempDir := t.TempDir()
		mock := &mockEnv{workingDir: tempDir}
		envContainer := env.EnvContainer{Env: mock}

		result, err := GetRepoConfigActivityV2(envContainer)

		require.NoError(t, err)
		assert.Empty(t, result.ChosenPath)
		assert.Empty(t, result.Config.Mission)
	})

	t.Run("returns full path for chosen config", func(t *testing.T) {
		tempDir := t.TempDir()
		configPath := filepath.Join(tempDir, "side.toml")
		err := os.WriteFile(configPath, []byte(`mission = "toml mission"`), 0644)
		require.NoError(t, err)

		mock := &mockEnv{workingDir: tempDir}
		envContainer := env.EnvContainer{Env: mock}

		result, err := GetRepoConfigActivityV2(envContainer)

		require.NoError(t, err)
		assert.Equal(t, configPath, result.ChosenPath)
		assert.Equal(t, "toml mission", result.Config.Mission)
	})

	t.Run("config parsing matches GetRepoConfigActivity", func(t *testing.T) {
		yamlContent := `
mission: test mission
edit_code:
  hints: inline hints
agent_config:
  coding:
    auto_iterations: 10
`
		envContainer := setupTestEnv(t, yamlContent, "", "")

		resultV2, err := GetRepoConfigActivityV2(envContainer)
		require.NoError(t, err)

		resultV1, err := GetRepoConfigActivity(envContainer)
		require.NoError(t, err)

		assert.Equal(t, resultV1, resultV2.Config)
	})

	t.Run("discovers config via env filesystem when working dir is not local", func(t *testing.T) {
		backingDir := t.TempDir()
		configContent := `
mission: remote mission
test_commands:
  - command: go test ./...
edit_code:
  hints: inline hints
`
		require.NoError(t, os.WriteFile(filepath.Join(backingDir, "side.yml"), []byte(configContent), 0644))

		remoteWorkingDir := filepath.Join(t.TempDir(), "nonexistent-remote-worktree")
		mock := &remoteLikeEnv{
			mockEnv:          &mockEnv{workingDir: remoteWorkingDir},
			remoteWorkingDir: remoteWorkingDir,
			backingDir:       backingDir,
		}
		envContainer := env.EnvContainer{Env: mock}

		result, err := GetRepoConfigActivityV2(envContainer)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(remoteWorkingDir, "side.yml"), result.ChosenPath)
		assert.Equal(t, "remote mission", result.Config.Mission)
		require.Len(t, result.Config.TestCommands, 1)
		assert.Equal(t, "go test ./...", result.Config.TestCommands[0].Command)
	})

	t.Run("reads fallback hints via env filesystem when working dir is not local", func(t *testing.T) {
		backingDir := t.TempDir()
		configContent := `
mission: remote mission
test_commands:
  - command: go test ./...
`
		require.NoError(t, os.WriteFile(filepath.Join(backingDir, "side.yml"), []byte(configContent), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(backingDir, "AGENTS.md"), []byte("fallback hints body"), 0644))

		remoteWorkingDir := filepath.Join(t.TempDir(), "nonexistent-remote-worktree")
		mock := &remoteLikeEnv{
			mockEnv:          &mockEnv{workingDir: remoteWorkingDir},
			remoteWorkingDir: remoteWorkingDir,
			backingDir:       backingDir,
		}
		envContainer := env.EnvContainer{Env: mock}

		result, err := GetRepoConfigActivityV2(envContainer)
		require.NoError(t, err)
		assert.Equal(t, "AGENTS.md", result.Config.EditCode.HintsPath)
		assert.Equal(t, "fallback hints body", result.Config.EditCode.Hints)
	})
}

func TestGetRepoConfigActivity_PortForwards(t *testing.T) {
	yamlContent := `
port_forwards:
  - host_port: 18855
  - host_port: 8080
    container_port: 9090
`
	envContainer := setupTestEnv(t, yamlContent, "", "")

	config, err := GetRepoConfigActivity(envContainer)

	require.NoError(t, err)
	require.Len(t, config.PortForwards, 2)
	assert.Equal(t, common.PortForwardConfig{HostPort: 18855}, config.PortForwards[0])
	assert.Equal(t, 18855, config.PortForwards[0].ContainerPortOrDefault())
	assert.Equal(t, common.PortForwardConfig{HostPort: 8080, ContainerPort: 9090}, config.PortForwards[1])
	assert.Equal(t, 9090, config.PortForwards[1].ContainerPortOrDefault())
}
