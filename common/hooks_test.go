package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHooksConfigValidate(t *testing.T) {
	t.Parallel()

	warn := HookOnErrorWarn
	ignore := "ignore"
	modalOnly := &HookMatchConfig{EnvTypes: []string{"modal"}}

	tests := []struct {
		name     string
		config   HooksConfig
		wantErrs []string
	}{
		{
			name:   "empty",
			config: HooksConfig{},
		},
		{
			name: "valid run_command script rule",
			config: HooksConfig{RunCommand: []HookRuleConfig{{
				Match:   modalOnly,
				Actions: []HookActionConfig{{Type: HookActionScript, Source: "emitCommandEnvironment({envVars: {}})", OnError: &warn}},
			}}},
		},
		{
			name: "both events accept both action types",
			config: HooksConfig{
				RunTests: []HookRuleConfig{{Actions: []HookActionConfig{
					{Type: HookActionModalRefreshVolumes},
					{Type: HookActionScript, Source: "1"},
				}}},
				RunCommand: []HookRuleConfig{{Actions: []HookActionConfig{
					{Type: HookActionScript, Source: "1"},
					{Type: HookActionModalRefreshVolumes},
				}}},
			},
		},
		{
			name: "script without source",
			config: HooksConfig{RunCommand: []HookRuleConfig{{
				Actions: []HookActionConfig{{Type: HookActionScript}},
			}}},
			wantErrs: []string{"hooks.run_command[0].actions[0] (script)", "source"},
		},
		{
			name: "refresh volumes with source",
			config: HooksConfig{RunCommand: []HookRuleConfig{{
				Actions: []HookActionConfig{{Type: HookActionModalRefreshVolumes, Source: "1"}},
			}}},
			wantErrs: []string{"hooks.run_command[0].actions[0] (modal.refresh_volumes)", "source"},
		},
		{
			name: "unknown type in run_command",
			config: HooksConfig{RunCommand: []HookRuleConfig{{
				Actions: []HookActionConfig{{Type: "shell"}},
			}}},
			wantErrs: []string{"hooks.run_command[0].actions[0] (shell)", "unsupported action type"},
		},
		{
			name: "unknown type in run_tests",
			config: HooksConfig{RunTests: []HookRuleConfig{
				{Actions: []HookActionConfig{{Type: HookActionModalRefreshVolumes}}},
				{Actions: []HookActionConfig{{Type: HookActionModalRefreshVolumes}, {Type: "shell"}}},
			}},
			wantErrs: []string{"hooks.run_tests[1].actions[1] (shell)", "unsupported action type"},
		},
		{
			name: "run_command errors reported after valid run_tests",
			config: HooksConfig{
				RunTests:   []HookRuleConfig{{Actions: []HookActionConfig{{Type: HookActionModalRefreshVolumes}}}},
				RunCommand: []HookRuleConfig{{Actions: []HookActionConfig{{Type: HookActionScript, Source: "1", OnError: &ignore}}}},
			},
			wantErrs: []string{"hooks.run_command[0].actions[0] (script)", "on_error"},
		},
		{
			name:     "missing actions in run_command rule",
			config:   HooksConfig{RunCommand: []HookRuleConfig{{Match: modalOnly}}},
			wantErrs: []string{"hooks.run_command[0].actions"},
		},
		{
			name: "unsupported env type in run_command match",
			config: HooksConfig{RunCommand: []HookRuleConfig{{
				Match:   &HookMatchConfig{EnvTypes: []string{"docker"}},
				Actions: []HookActionConfig{{Type: HookActionScript, Source: "1"}},
			}}},
			wantErrs: []string{"hooks.run_command[0].match.env_types[0]", "docker"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.config.Validate()
			if len(tt.wantErrs) == 0 {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, want := range tt.wantErrs {
				require.Contains(t, err.Error(), want)
			}
		})
	}
}
