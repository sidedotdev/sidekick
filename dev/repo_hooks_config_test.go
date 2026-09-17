package dev

import (
	"encoding/json"
	"math"
	"sidekick/common"
	"sidekick/env"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func repoHookConfigLoaders() map[string]func(env.EnvContainer) (common.RepoConfig, error) {
	return map[string]func(env.EnvContainer) (common.RepoConfig, error){
		"v1": GetRepoConfigActivity,
		"v2": func(ec env.EnvContainer) (common.RepoConfig, error) {
			result, err := GetRepoConfigActivityV2(ec)
			return result.Config, err
		},
	}
}

func TestRepoHooksConfigRejectsInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		hooks string
		path  string
	}{
		{"old event", `{"before_tests":[]}`, "hooks.before_tests"},
		{"future event", `{"test_results":[]}`, "hooks.test_results"},
		{"unknown event", `{"other":[]}`, "hooks.other"},
		{"null hooks", `null`, "hooks"},
		{"scalar hooks", `"run_tests"`, "hooks"},
		{"null rules", `{"run_tests":null}`, "hooks.run_tests"},
		{"object rules", `{"run_tests":{}}`, "hooks.run_tests"},
		{"null rule", `{"run_tests":[null]}`, "hooks.run_tests[0]"},
		{"unknown rule field", `{"run_tests":[{"extra":true,"actions":[]}]}`, "hooks.run_tests[0].extra"},
		{"missing actions", `{"run_tests":[{}]}`, "hooks.run_tests[0].actions"},
		{"null actions", `{"run_tests":[{"actions":null}]}`, "hooks.run_tests[0].actions"},
		{"object actions", `{"run_tests":[{"actions":{}}]}`, "hooks.run_tests[0].actions"},
		{"null match", `{"run_tests":[{"match":null,"actions":[]}]}`, "hooks.run_tests[0].match"},
		{"unknown filter", `{"run_tests":[{"match":{"branch":["main"]},"actions":[]}]}`, "hooks.run_tests[0].match.branch"},
		{"empty environments", `{"run_tests":[{"match":{"env_types":[]},"actions":[]}]}`, "hooks.run_tests[0].match.env_types"},
		{"null environments", `{"run_tests":[{"match":{"env_types":null},"actions":[]}]}`, "hooks.run_tests[0].match.env_types"},
		{"scalar environment", `{"run_tests":[{"match":{"env_types":"modal"},"actions":[]}]}`, "hooks.run_tests[0].match.env_types"},
		{"unknown environment", `{"run_tests":[{"match":{"env_types":["docker"]},"actions":[]}]}`, "hooks.run_tests[0].match.env_types"},
		{"nonstring environment", `{"run_tests":[{"match":{"env_types":[1]},"actions":[]}]}`, "hooks.run_tests[0].match.env_types"},
		{"null action", `{"run_tests":[{"actions":[null]}]}`, "hooks.run_tests[0].actions[0]"},
		{"missing type", `{"run_tests":[{"actions":[{}]}]}`, "hooks.run_tests[0].actions[0]"},
		{"shell", `{"run_tests":[{"actions":[{"type":"shell"}]}]}`, "shell"},
		{"unknown type", `{"run_tests":[{"actions":[{"type":"other"}]}]}`, "other"},
		{"refresh arguments", `{"run_tests":[{"actions":[{"type":"modal.refresh_volumes","volumes":["cache"]}]}]}`, "modal.refresh_volumes"},
		{"shell command argument", `{"run_tests":[{"actions":[{"type":"modal.refresh_volumes","command":"true"}]}]}`, "modal.refresh_volumes"},
		{"zero timeout", `{"run_tests":[{"actions":[{"type":"modal.refresh_volumes","timeout_seconds":0}]}]}`, "timeout_seconds"},
		{"negative timeout", `{"run_tests":[{"actions":[{"type":"modal.refresh_volumes","timeout_seconds":-1}]}]}`, "timeout_seconds"},
		{"string timeout", `{"run_tests":[{"actions":[{"type":"modal.refresh_volumes","timeout_seconds":"30"}]}]}`, "timeout_seconds"},
		{"null timeout", `{"run_tests":[{"actions":[{"type":"modal.refresh_volumes","timeout_seconds":null}]}]}`, "timeout_seconds"},
		{"subnanosecond timeout", `{"run_tests":[{"actions":[{"type":"modal.refresh_volumes","timeout_seconds":1e-12}]}]}`, "timeout_seconds"},
		{"overflow timeout", `{"run_tests":[{"actions":[{"type":"modal.refresh_volumes","timeout_seconds":1e20}]}]}`, "timeout_seconds"},
		{"empty policy", `{"run_tests":[{"actions":[{"type":"modal.refresh_volumes","on_error":""}]}]}`, "on_error"},
		{"unknown policy", `{"run_tests":[{"actions":[{"type":"modal.refresh_volumes","on_error":"ignore"}]}]}`, "on_error"},
		{"null policy", `{"run_tests":[{"actions":[{"type":"modal.refresh_volumes","on_error":null}]}]}`, "on_error"},
		{"nonstring policy", `{"run_tests":[{"actions":[{"type":"modal.refresh_volumes","on_error":true}]}]}`, "on_error"},
		{"wrong field case", `{"Run_tests":[]}`, "hooks.Run_tests"},
	}

	for name, load := range repoHookConfigLoaders() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					ec := setupTestEnvWithFilename(t, "side.json", `{"hooks":`+tt.hooks+`}`, "", "")
					_, err := load(ec)
					require.Error(t, err)
					require.Contains(t, err.Error(), tt.path)
				})
			}
		})
	}
}

func TestRepoHooksConfigFormats(t *testing.T) {
	t.Parallel()

	configs := map[string]string{
		"side.yml": `
mission: retained
unrelated_future_field: ignored
hooks:
  run_tests:
    - match:
        env_types: [modal, local]
      actions:
        - type: modal.refresh_volumes
          timeout_seconds: 0.25
          on_error: warn
        - type: modal.refresh_volumes
    - actions: []
`,
		"side.yaml": `
mission: retained
hooks:
  run_tests:
    - match:
        env_types: [modal, local]
      actions:
        - type: modal.refresh_volumes
          timeout_seconds: 0.25
          on_error: warn
        - type: modal.refresh_volumes
    - actions: []
`,
		"side.json": `{
			"mission":"retained",
			"unrelated_future_field":"ignored",
			"hooks":{"run_tests":[
				{"match":{"env_types":["modal","local"]},"actions":[
					{"type":"modal.refresh_volumes","timeout_seconds":0.25,"on_error":"warn"},
					{"type":"modal.refresh_volumes"}
				]},
				{"actions":[]}
			]}
		}`,
		"side.toml": `
mission = "retained"
unrelated_future_field = "ignored"
[[hooks.run_tests]]
[hooks.run_tests.match]
env_types = ["modal", "local"]
[[hooks.run_tests.actions]]
type = "modal.refresh_volumes"
timeout_seconds = 0.25
on_error = "warn"
[[hooks.run_tests.actions]]
type = "modal.refresh_volumes"
[[hooks.run_tests]]
actions = []
`,
	}
	for name, load := range repoHookConfigLoaders() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for filename, content := range configs {
				t.Run(filename, func(t *testing.T) {
					t.Parallel()
					ec := setupTestEnvWithFilename(t, filename, content, "", "")
					config, err := load(ec)
					require.NoError(t, err)
					require.Equal(t, "retained", config.Mission)
					require.NoError(t, config.Hooks.Validate())
					require.Len(t, config.Hooks.RunTests, 2)
					first, second := config.Hooks.RunTests[0], config.Hooks.RunTests[1]
					require.NotNil(t, first.Match)
					require.Equal(t, []string{"modal", "local"}, first.Match.EnvTypes)
					require.Len(t, first.Actions, 2)
					require.Equal(t, common.HookActionModalRefreshVolumes, first.Actions[0].Type)
					require.Equal(t, 250*time.Millisecond, first.Actions[0].Timeout())
					require.Equal(t, common.HookOnErrorWarn, first.Actions[0].ErrorPolicy())
					require.Nil(t, first.Actions[1].TimeoutSeconds)
					require.Nil(t, first.Actions[1].OnError)
					require.Equal(t, 30*time.Second, first.Actions[1].Timeout())
					require.Equal(t, common.HookOnErrorFail, first.Actions[1].ErrorPolicy())
					require.Nil(t, second.Match)
					require.NotNil(t, second.Actions)
					require.Empty(t, second.Actions)

					data, err := json.Marshal(config.Hooks)
					require.NoError(t, err)
					require.Contains(t, string(data), `"runTests"`)
					require.Contains(t, string(data), `"envTypes"`)
					require.Contains(t, string(data), `"timeoutSeconds"`)
					require.Contains(t, string(data), `"onError"`)
					var restored common.HooksConfig
					require.NoError(t, json.Unmarshal(data, &restored))
					require.Equal(t, config.Hooks, restored)
				})
			}
		})
	}
}

func TestRepoHooksConfigNoOpsAndDefaults(t *testing.T) {
	t.Parallel()

	for name, load := range repoHookConfigLoaders() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, content := range []string{
				`{}`,
				`{"hooks":{}}`,
				`{"hooks":{"run_tests":[]}}`,
				`{"hooks":{"run_tests":[{"actions":[]}]}}`,
				`{"hooks":{"run_tests":[{"match":{},"actions":[]}]}}`,
				`{"hooks":{"run_tests":[{"actions":[{"type":"modal.refresh_volumes","timeout_seconds":1,"on_error":"fail"}]}]}}`,
			} {
				t.Run(content, func(t *testing.T) {
					t.Parallel()
					ec := setupTestEnvWithFilename(t, "side.json", content, "", "")
					config, err := load(ec)
					require.NoError(t, err)
					require.NoError(t, config.Hooks.Validate())
				})
			}
		})
	}
}

func TestRepoHooksConfigEnvironmentNames(t *testing.T) {
	t.Parallel()

	for _, name := range []env.EnvType{
		env.EnvTypeLocal,
		env.EnvTypeLocalGitWorktree,
		env.EnvTypeDevPod,
		env.EnvTypeOpenShell,
		env.EnvTypeModal,
		"docker",
		"",
		"MODAL",
	} {
		t.Run(string(name), func(t *testing.T) {
			t.Parallel()
			config := common.HooksConfig{RunTests: []common.HookRuleConfig{{
				Match:   &common.HookMatchConfig{EnvTypes: []string{string(name)}},
				Actions: []common.HookActionConfig{},
			}}}
			require.Equal(t, name.IsValid(), config.Validate() == nil)
		})
	}
}

func TestRepoHooksActionValidation(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		seconds float64
		valid   bool
	}{
		{"fraction", 0.125, true},
		{"integer", 30, true},
		{"nanosecond", 1e-9, true},
		{"zero", 0, false},
		{"negative", -1, false},
		{"too small", 1e-12, false},
		{"overflow", 1e20, false},
		{"boundary overflow", float64(math.MaxInt64) / float64(time.Second), false},
		{"nan", math.NaN(), false},
		{"positive infinity", math.Inf(1), false},
		{"negative infinity", math.Inf(-1), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			action := common.HookActionConfig{
				Type:           common.HookActionModalRefreshVolumes,
				TimeoutSeconds: &tt.seconds,
			}
			err := action.Validate("hooks.run_tests[2].actions[1]")
			if tt.valid {
				require.NoError(t, err)
				require.Positive(t, action.Timeout())
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), "hooks.run_tests[2].actions[1]")
				require.Contains(t, err.Error(), common.HookActionModalRefreshVolumes)
				require.Contains(t, err.Error(), "timeout_seconds")
			}
		})
	}
}

func TestRepoHooksConfigAllEnvironmentsAcrossFormats(t *testing.T) {
	t.Parallel()

	configs := map[string]string{
		"side.yml": `
hooks:
  run_tests:
    - match:
        env_types: [local, local_git_worktree, devpod, openshell, modal]
      actions:
        - type: modal.refresh_volumes
`,
		"side.toml": `
[[hooks.run_tests]]
[hooks.run_tests.match]
env_types = ["local", "local_git_worktree", "devpod", "openshell", "modal"]
[[hooks.run_tests.actions]]
type = "modal.refresh_volumes"
`,
		"side.json": `{
			"hooks":{"run_tests":[{
				"match":{"env_types":["local","local_git_worktree","devpod","openshell","modal"]},
				"actions":[{"type":"modal.refresh_volumes"}]
			}]}
		}`,
	}

	for name, load := range repoHookConfigLoaders() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for filename, content := range configs {
				t.Run(filename, func(t *testing.T) {
					t.Parallel()
					ec := setupTestEnvWithFilename(t, filename, content, "", "")
					config, err := load(ec)
					require.NoError(t, err)
					require.Len(t, config.Hooks.RunTests, 1)
					rule := config.Hooks.RunTests[0]
					require.NotNil(t, rule.Match)
					require.Equal(t, []string{
						string(env.EnvTypeLocal),
						string(env.EnvTypeLocalGitWorktree),
						string(env.EnvTypeDevPod),
						string(env.EnvTypeOpenShell),
						string(env.EnvTypeModal),
					}, rule.Match.EnvTypes)
					require.Len(t, rule.Actions, 1)
					require.Equal(t, common.HookActionModalRefreshVolumes, rule.Actions[0].Type)
				})
			}
		})
	}
}

func TestRepoHooksConfigRejectsInvalidPolicyInUnmatchedRule(t *testing.T) {
	t.Parallel()

	configs := map[string]string{
		"side.yml": `
hooks:
  run_tests:
    - match:
        env_types: [modal]
      actions:
        - type: modal.refresh_volumes
          on_error: ignore
`,
		"side.toml": `
[[hooks.run_tests]]
[hooks.run_tests.match]
env_types = ["modal"]
[[hooks.run_tests.actions]]
type = "modal.refresh_volumes"
on_error = "ignore"
`,
		"side.json": `{
			"hooks":{"run_tests":[{
				"match":{"env_types":["modal"]},
				"actions":[{"type":"modal.refresh_volumes","on_error":"ignore"}]
			}]}
		}`,
	}

	for name, load := range repoHookConfigLoaders() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for filename, content := range configs {
				t.Run(filename, func(t *testing.T) {
					t.Parallel()
					ec := setupTestEnvWithFilename(t, filename, content, "", "")
					require.Equal(t, env.EnvTypeLocal, ec.Env.GetType())
					require.NotEqual(t, env.EnvTypeModal, ec.Env.GetType())

					_, err := load(ec)
					require.Error(t, err)
					require.Contains(t, err.Error(), "hooks.run_tests[0].actions[0]")
					require.Contains(t, err.Error(), common.HookActionModalRefreshVolumes)
					require.Contains(t, err.Error(), "on_error")
				})
			}
		})
	}
}
