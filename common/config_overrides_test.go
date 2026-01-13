package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigOverrides_ApplyToRepoConfig(t *testing.T) {
	t.Parallel()

	t.Run("nil overrides do not modify config", func(t *testing.T) {
		t.Parallel()
		original := RepoConfig{
			Mission:       "original mission",
			MaxIterations: 10,
		}
		overrides := ConfigOverrides{}

		overrides.ApplyToRepoConfig(&original)

		assert.Equal(t, "original mission", original.Mission)
		assert.Equal(t, 10, original.MaxIterations)
	})

	t.Run("mission override", func(t *testing.T) {
		t.Parallel()
		config := RepoConfig{Mission: "original"}
		newMission := "new mission"
		overrides := ConfigOverrides{Mission: &newMission}

		overrides.ApplyToRepoConfig(&config)

		assert.Equal(t, "new mission", config.Mission)
	})

	t.Run("disable human in the loop override", func(t *testing.T) {
		t.Parallel()
		config := RepoConfig{DisableHumanInTheLoop: false}
		disabled := true
		overrides := ConfigOverrides{DisableHumanInTheLoop: &disabled}

		overrides.ApplyToRepoConfig(&config)

		assert.True(t, config.DisableHumanInTheLoop)
	})

	t.Run("max iterations override", func(t *testing.T) {
		t.Parallel()
		config := RepoConfig{MaxIterations: 10}
		newMax := 25
		overrides := ConfigOverrides{MaxIterations: &newMax}

		overrides.ApplyToRepoConfig(&config)

		assert.Equal(t, 25, config.MaxIterations)
	})

	t.Run("check commands override", func(t *testing.T) {
		t.Parallel()
		config := RepoConfig{
			CheckCommands: []CommandConfig{{Command: "original"}},
		}
		newCommands := []CommandConfig{{Command: "new", WorkingDir: "/tmp"}}
		overrides := ConfigOverrides{CheckCommands: &newCommands}

		overrides.ApplyToRepoConfig(&config)

		assert.Equal(t, newCommands, config.CheckCommands)
	})

	t.Run("agent config override merges with existing", func(t *testing.T) {
		t.Parallel()
		config := RepoConfig{
			AgentConfig: map[string]AgentUseCaseConfig{
				"planning": {AutoIterations: 5},
				"coding":   {AutoIterations: 10},
			},
		}
		newCoding := AgentUseCaseConfig{AutoIterations: 20}
		overrides := ConfigOverrides{
			AgentConfig: map[string]*AgentUseCaseConfig{
				"coding": &newCoding,
			},
		}

		overrides.ApplyToRepoConfig(&config)

		assert.Equal(t, 5, config.AgentConfig["planning"].AutoIterations)
		assert.Equal(t, 20, config.AgentConfig["coding"].AutoIterations)
	})

	t.Run("agent config override creates map if nil", func(t *testing.T) {
		t.Parallel()
		config := RepoConfig{}
		newPlanning := AgentUseCaseConfig{AutoIterations: 15}
		overrides := ConfigOverrides{
			AgentConfig: map[string]*AgentUseCaseConfig{
				"planning": &newPlanning,
			},
		}

		overrides.ApplyToRepoConfig(&config)

		assert.Equal(t, 15, config.AgentConfig["planning"].AutoIterations)
	})

	t.Run("dev run override", func(t *testing.T) {
		t.Parallel()
		config := RepoConfig{}
		stopCmd := CommandConfig{Command: "pkill -f 'npm run dev'"}
		devRun := DevRunConfig{
			Commands: map[string]DevRunCommandConfig{
				"frontend": {
					Start: CommandConfig{Command: "npm run dev", WorkingDir: "frontend"},
					Stop:  &stopCmd,
				},
			},
			StopTimeoutSeconds: 15,
		}
		overrides := ConfigOverrides{DevRun: &devRun}

		overrides.ApplyToRepoConfig(&config)

		assert.Equal(t, devRun, config.DevRun)
		assert.Len(t, config.DevRun.Commands, 1)
		assert.Equal(t, "npm run dev", config.DevRun.Commands["frontend"].Start.Command)
		assert.Equal(t, "frontend", config.DevRun.Commands["frontend"].Start.WorkingDir)
		assert.NotNil(t, config.DevRun.Commands["frontend"].Stop)
		assert.Equal(t, 15, config.DevRun.StopTimeoutSeconds)
	})

	t.Run("dev run override replaces existing", func(t *testing.T) {
		t.Parallel()
		config := RepoConfig{
			DevRun: DevRunConfig{
				Commands: map[string]DevRunCommandConfig{
					"old": {Start: CommandConfig{Command: "old command"}},
				},
				StopTimeoutSeconds: 5,
			},
		}
		newDevRun := DevRunConfig{
			Commands: map[string]DevRunCommandConfig{
				"new": {Start: CommandConfig{Command: "new command"}},
			},
			StopTimeoutSeconds: 30,
		}
		overrides := ConfigOverrides{DevRun: &newDevRun}

		overrides.ApplyToRepoConfig(&config)

		assert.Equal(t, "new command", config.DevRun.Commands["new"].Start.Command)
		assert.Equal(t, 30, config.DevRun.StopTimeoutSeconds)
		assert.NotContains(t, config.DevRun.Commands, "old")
	})

	t.Run("multiple overrides applied together", func(t *testing.T) {
		t.Parallel()
		config := RepoConfig{
			Mission:       "original",
			MaxIterations: 10,
		}
		newMission := "updated mission"
		newMax := 50
		devRun := DevRunConfig{
			Commands: map[string]DevRunCommandConfig{
				"dev": {Start: CommandConfig{Command: "make dev"}},
			},
		}
		overrides := ConfigOverrides{
			Mission:       &newMission,
			MaxIterations: &newMax,
			DevRun:        &devRun,
		}

		overrides.ApplyToRepoConfig(&config)

		assert.Equal(t, "updated mission", config.Mission)
		assert.Equal(t, 50, config.MaxIterations)
		assert.Equal(t, "make dev", config.DevRun.Commands["dev"].Start.Command)
	})
}
