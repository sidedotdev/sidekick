package dev

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/env"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEnv is a simple implementation of env.Env for testing purposes.
type mockEnv struct {
	workingDir string
}

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

// Helper function to create side.toml and optional hints file
func setupTestEnv(t *testing.T, sideTomlContent string, hintsFilename string, hintsContent string) env.EnvContainer {
	t.Helper()
	tempDir := t.TempDir()

	// Create side.toml
	sideTomlPath := filepath.Join(tempDir, "side.toml")
	err := os.WriteFile(sideTomlPath, []byte(sideTomlContent), 0644)
	require.NoError(t, err, "Failed to write side.toml")

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
		tomlContent := `
[edit_code]
hints = "This is an inline hint."
`
		envContainer := setupTestEnv(t, tomlContent, "", "")

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "This is an inline hint.", config.EditCode.Hints)
		assert.Empty(t, config.EditCode.HintsPath)
	})

	t.Run("Scenario 2: Inline hints empty, hints_path set, file exists", func(t *testing.T) {
		hintsFilename := "actual_hints.txt"
		hintsContent := "This hint comes from the file."
		tomlContent := `
[edit_code]
hints_path = "` + hintsFilename + `"
`
		envContainer := setupTestEnv(t, tomlContent, hintsFilename, hintsContent)

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, hintsContent, config.EditCode.Hints)
		assert.Equal(t, hintsFilename, config.EditCode.HintsPath)
	})

	t.Run("Scenario 3: Inline hints set, hints_path set", func(t *testing.T) {
		hintsFilename := "other_hints.txt"
		hintsContent := "This hint should be ignored."
		tomlContent := `
[edit_code]
hints = "Inline hint takes precedence."
hints_path = "` + hintsFilename + `"
`
		envContainer := setupTestEnv(t, tomlContent, hintsFilename, hintsContent)

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "Inline hint takes precedence.", config.EditCode.Hints)
		assert.Equal(t, hintsFilename, config.EditCode.HintsPath) // HintsPath is still populated, just not used for Hints content
	})

	t.Run("Scenario 4: Inline hints empty, hints_path set, file missing", func(t *testing.T) {
		missingHintsFilename := "non_existent_hints.txt"
		tomlContent := `
[edit_code]
hints_path = "` + missingHintsFilename + `"
`
		envContainer := setupTestEnv(t, tomlContent, "", "") // Don't create the hints file

		_, err := GetRepoConfigActivity(envContainer)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read hints file specified in side.toml")
		assert.Contains(t, err.Error(), missingHintsFilename)
		// Check for underlying os.ErrNotExist if possible/desired, though message check is often sufficient
		// require.ErrorIs(t, err, os.ErrNotExist) // This might require unwrapping the error
	})

	t.Run("Scenario 5: Both hints and hints_path empty", func(t *testing.T) {
		// Scenario 5a: Empty edit_code section
		tomlContentEmptySection := `
[edit_code]
`
		envContainerEmptySection := setupTestEnv(t, tomlContentEmptySection, "", "")
		configEmptySection, errEmptySection := GetRepoConfigActivity(envContainerEmptySection)
		require.NoError(t, errEmptySection)
		assert.Empty(t, configEmptySection.EditCode.Hints)
		assert.Empty(t, configEmptySection.EditCode.HintsPath)

		// Scenario 5b: No edit_code section
		tomlContentNoSection := `
mission = "Test mission"
`
		envContainerNoSection := setupTestEnv(t, tomlContentNoSection, "", "")
		configNoSection, errNoSection := GetRepoConfigActivity(envContainerNoSection)
		require.NoError(t, errNoSection)
		assert.Empty(t, configNoSection.EditCode.Hints)
		assert.Empty(t, configNoSection.EditCode.HintsPath)

		// Scenario 5c: Empty file
		tomlContentEmptyFile := ``
		envContainerEmptyFile := setupTestEnv(t, tomlContentEmptyFile, "", "")
		configEmptyFile, errEmptyFile := GetRepoConfigActivity(envContainerEmptyFile)
		require.NoError(t, errEmptyFile)
		assert.Empty(t, configEmptyFile.EditCode.Hints)
		assert.Empty(t, configEmptyFile.EditCode.HintsPath)
	})

	t.Run("Scenario 6: TOML unmarshalling error", func(t *testing.T) {
		invalidTomlContent := `this is not valid toml [`
		envContainer := setupTestEnv(t, invalidTomlContent, "", "")

		_, err := GetRepoConfigActivity(envContainer)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal TOML data")
	})

	t.Run("Handles missing side.toml file", func(t *testing.T) {
		tempDir := t.TempDir()
		mock := &mockEnv{workingDir: tempDir}
		envContainer := env.EnvContainer{Env: mock}
		// Do not create side.toml

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
[edit_code]
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
		tomlContent := `
[edit_code]
hints = "Inline value"
`
		envContainer := setupTestEnv(t, tomlContent, "", "")
		writeFallbackFile(t, envContainer, "AGENTS.md", "fallback content")

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "Inline value", config.EditCode.Hints)
		assert.Empty(t, config.EditCode.HintsPath)
	})

	t.Run("Configured hints_path overrides fallback", func(t *testing.T) {
		hintsFilename := "repo_hints.txt"
		tomlContent := `
[edit_code]
hints_path = "` + hintsFilename + `"
`
		envContainer := setupTestEnv(t, tomlContent, hintsFilename, "configured file content")
		writeFallbackFile(t, envContainer, "AGENTS.md", "fallback content")

		config, err := GetRepoConfigActivity(envContainer)

		require.NoError(t, err)
		assert.Equal(t, "configured file content", config.EditCode.Hints)
		assert.Equal(t, hintsFilename, config.EditCode.HintsPath)
	})
}

func TestGetRepoConfigActivity_NoFallbackCandidates(t *testing.T) {
	envContainer := setupTestEnv(t, `
[edit_code]
`, "", "")

	config, err := GetRepoConfigActivity(envContainer)

	require.NoError(t, err)
	assert.Empty(t, config.EditCode.Hints)
	assert.Empty(t, config.EditCode.HintsPath)
}
