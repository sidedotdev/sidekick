package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateCommandPermissionDetailed(t *testing.T) {
	t.Parallel()

	config := CommandPermissionConfig{
		Deny: []CommandPattern{
			{Pattern: "git push --force", Message: "no force pushes"},
			{Pattern: `git push (--force|-f)`, Message: "no force pushes: $1", Source: CommandPatternSourceRepoConfig},
		},
		RequireApproval: []CommandPattern{
			{Pattern: "git push", Source: CommandPatternSourceBase},
		},
		AutoApprove: []CommandPattern{
			{Pattern: "git", Source: CommandPatternSourceWorkspaceConfig},
			{Pattern: "ls", Message: "listing is fine", Source: CommandPatternSourceLocalConfig},
		},
	}

	tests := []struct {
		name              string
		command           string
		opts              EvaluatePermissionOptions
		wantOutcome       PermissionResult
		wantDecidedBy     PermissionDecider
		wantDecidingRule  *MatchedRule
		wantDecidingKind  PermissionFactorKind
		wantMatchedRules  []MatchedRule
		wantFactorKinds   []PermissionFactorKind
		wantFactorPaths   []string
		wantWrapperResult PermissionResult
		wantWrapperMsg    string
	}{
		{
			name:          "deny wins over require and auto, all matches recorded with sources",
			command:       "git push --force origin main",
			wantOutcome:   PermissionDeny,
			wantDecidedBy: DecidedByRule,
			wantDecidingRule: &MatchedRule{
				Action: PermissionDeny, Pattern: "git push --force", Message: "no force pushes",
			},
			wantMatchedRules: []MatchedRule{
				{Action: PermissionDeny, Pattern: "git push --force", Message: "no force pushes"},
				{Action: PermissionDeny, Pattern: `git push (--force|-f)`, Message: "no force pushes: --force", Source: CommandPatternSourceRepoConfig},
				{Action: PermissionRequireApproval, Pattern: "git push", Source: CommandPatternSourceBase},
				{Action: PermissionAutoApprove, Pattern: "git", Source: CommandPatternSourceWorkspaceConfig},
			},
			wantWrapperResult: PermissionDeny,
			wantWrapperMsg:    "no force pushes",
		},
		{
			name:          "require approval wins over auto approve",
			command:       "git push origin main",
			wantOutcome:   PermissionRequireApproval,
			wantDecidedBy: DecidedByRule,
			wantDecidingRule: &MatchedRule{
				Action: PermissionRequireApproval, Pattern: "git push", Source: CommandPatternSourceBase,
			},
			wantMatchedRules: []MatchedRule{
				{Action: PermissionRequireApproval, Pattern: "git push", Source: CommandPatternSourceBase},
				{Action: PermissionAutoApprove, Pattern: "git", Source: CommandPatternSourceWorkspaceConfig},
			},
			wantWrapperResult: PermissionRequireApproval,
			wantWrapperMsg:    "",
		},
		{
			name:          "auto approve with message",
			command:       "ls -la",
			wantOutcome:   PermissionAutoApprove,
			wantDecidedBy: DecidedByRule,
			wantDecidingRule: &MatchedRule{
				Action: PermissionAutoApprove, Pattern: "ls", Message: "listing is fine", Source: CommandPatternSourceLocalConfig,
			},
			wantMatchedRules: []MatchedRule{
				{Action: PermissionAutoApprove, Pattern: "ls", Message: "listing is fine", Source: CommandPatternSourceLocalConfig},
			},
			wantWrapperResult: PermissionAutoApprove,
			wantWrapperMsg:    "listing is fine",
		},
		{
			name:             "absolute path escalation factor records detected paths",
			command:          "ls /etc/passwd",
			wantOutcome:      PermissionRequireApproval,
			wantDecidedBy:    DecidedByFactor,
			wantDecidingKind: PermissionFactorAbsolutePathEscalation,
			wantMatchedRules: []MatchedRule{
				{Action: PermissionAutoApprove, Pattern: "ls", Message: "listing is fine", Source: CommandPatternSourceLocalConfig},
			},
			wantFactorKinds:   []PermissionFactorKind{PermissionFactorAbsolutePathEscalation},
			wantFactorPaths:   []string{"/etc/passwd"},
			wantWrapperResult: PermissionRequireApproval,
			wantWrapperMsg:    "",
		},
		{
			name:          "skip absolute path escalation",
			command:       "ls /etc/passwd",
			opts:          EvaluatePermissionOptions{SkipAbsolutePathEscalation: true},
			wantOutcome:   PermissionAutoApprove,
			wantDecidedBy: DecidedByRule,
			wantDecidingRule: &MatchedRule{
				Action: PermissionAutoApprove, Pattern: "ls", Message: "listing is fine", Source: CommandPatternSourceLocalConfig,
			},
			wantMatchedRules: []MatchedRule{
				{Action: PermissionAutoApprove, Pattern: "ls", Message: "listing is fine", Source: CommandPatternSourceLocalConfig},
			},
			wantWrapperResult: PermissionAutoApprove,
			wantWrapperMsg:    "listing is fine",
		},
		{
			name:              "no rule matched defaults to require approval",
			command:           "unknown-cmd --flag",
			wantOutcome:       PermissionRequireApproval,
			wantDecidedBy:     DecidedByFactor,
			wantDecidingKind:  PermissionFactorNoRuleMatched,
			wantFactorKinds:   []PermissionFactorKind{PermissionFactorNoRuleMatched},
			wantWrapperResult: PermissionRequireApproval,
			wantWrapperMsg:    "",
		},
		{
			name:              "sandbox default auto approve when no rule matched",
			command:           "unknown-cmd --flag",
			opts:              EvaluatePermissionOptions{DefaultAutoApprove: true},
			wantOutcome:       PermissionAutoApprove,
			wantDecidedBy:     DecidedByFactor,
			wantDecidingKind:  PermissionFactorSandboxDefaultAutoApprove,
			wantFactorKinds:   []PermissionFactorKind{PermissionFactorSandboxDefaultAutoApprove},
			wantWrapperResult: PermissionAutoApprove,
			wantWrapperMsg:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eval := EvaluateCommandPermissionDetailed(config, tt.command, tt.opts)

			assert.Equal(t, tt.command, eval.Command)
			assert.Equal(t, tt.wantOutcome, eval.Outcome)
			assert.Equal(t, tt.wantDecidedBy, eval.DecidedBy)
			assert.Equal(t, tt.wantMatchedRules, eval.MatchedRules)

			switch tt.wantDecidedBy {
			case DecidedByRule:
				require.NotNil(t, tt.wantDecidingRule)
				require.Less(t, eval.DecidedByIndex, len(eval.MatchedRules))
				assert.Equal(t, *tt.wantDecidingRule, eval.MatchedRules[eval.DecidedByIndex])
			case DecidedByFactor:
				require.Less(t, eval.DecidedByIndex, len(eval.Factors))
				decidingFactor := eval.Factors[eval.DecidedByIndex]
				assert.Equal(t, tt.wantDecidingKind, decidingFactor.Kind)
				assert.Equal(t, tt.wantOutcome, decidingFactor.Outcome)
				assert.NotEmpty(t, decidingFactor.Message)
			}

			gotFactorKinds := make([]PermissionFactorKind, 0, len(eval.Factors))
			for _, f := range eval.Factors {
				gotFactorKinds = append(gotFactorKinds, f.Kind)
			}
			assert.ElementsMatch(t, tt.wantFactorKinds, gotFactorKinds)

			if tt.wantFactorPaths != nil {
				var gotPaths []string
				for _, f := range eval.Factors {
					gotPaths = append(gotPaths, f.Paths...)
				}
				assert.Equal(t, tt.wantFactorPaths, gotPaths)
			}

			// The compatibility wrapper must derive the exact same result and
			// message as the pre-refactor evaluator.
			result, msg := EvaluateCommandPermissionWithOptions(config, tt.command, tt.opts)
			assert.Equal(t, tt.wantWrapperResult, result)
			assert.Equal(t, tt.wantWrapperMsg, msg)
		})
	}
}

func TestEvaluateScriptPermissionDetailed(t *testing.T) {
	t.Parallel()

	config := CommandPermissionConfig{
		AutoApprove: []CommandPattern{
			{Pattern: "ls", Message: "listing is fine", Source: CommandPatternSourceLocalConfig},
			{Pattern: "echo", Source: CommandPatternSourceBase},
			{Pattern: "cat", Source: CommandPatternSourceBase},
			{Pattern: "tee"},
		},
		RequireApproval: []CommandPattern{
			{Pattern: "git push", Source: CommandPatternSourceRepoConfig},
		},
		Deny: []CommandPattern{
			{Pattern: "sudo", Message: "no sudo allowed", Source: CommandPatternSourceWorkspaceConfig},
		},
	}

	factorKinds := func(factors []PermissionFactor) []PermissionFactorKind {
		kinds := make([]PermissionFactorKind, 0, len(factors))
		for _, f := range factors {
			kinds = append(kinds, f.Kind)
		}
		return kinds
	}

	t.Run("mixed script explains auto-approved and approval-requiring commands", func(t *testing.T) {
		t.Parallel()
		script := "ls -la && git push origin main && unknown-cmd"
		eval := EvaluateScriptPermissionDetailed(config, script, EvaluatePermissionOptions{})

		assert.Equal(t, PermissionRequireApproval, eval.Outcome)
		assert.Empty(t, eval.Factors)
		require.Len(t, eval.Commands, 3)

		lsEval := eval.Commands[0]
		assert.Equal(t, "ls -la", lsEval.Command)
		assert.Equal(t, PermissionAutoApprove, lsEval.Outcome)
		assert.Equal(t, DecidedByRule, lsEval.DecidedBy)
		require.Less(t, lsEval.DecidedByIndex, len(lsEval.MatchedRules))
		assert.Equal(t, MatchedRule{
			Action: PermissionAutoApprove, Pattern: "ls",
			Message: "listing is fine", Source: CommandPatternSourceLocalConfig,
		}, lsEval.MatchedRules[lsEval.DecidedByIndex])

		pushEval := eval.Commands[1]
		assert.Equal(t, PermissionRequireApproval, pushEval.Outcome)
		assert.Equal(t, DecidedByRule, pushEval.DecidedBy)
		require.Less(t, pushEval.DecidedByIndex, len(pushEval.MatchedRules))
		assert.Equal(t, MatchedRule{
			Action: PermissionRequireApproval, Pattern: "git push", Source: CommandPatternSourceRepoConfig,
		}, pushEval.MatchedRules[pushEval.DecidedByIndex])

		unknownEval := eval.Commands[2]
		assert.Equal(t, PermissionRequireApproval, unknownEval.Outcome)
		assert.Equal(t, DecidedByFactor, unknownEval.DecidedBy)
		require.Less(t, unknownEval.DecidedByIndex, len(unknownEval.Factors))
		assert.Equal(t, PermissionFactorNoRuleMatched, unknownEval.Factors[unknownEval.DecidedByIndex].Kind)

		result, msg := EvaluateScriptPermissionWithOptions(config, script, EvaluatePermissionOptions{})
		assert.Equal(t, PermissionRequireApproval, result)
		assert.Empty(t, msg)
	})

	t.Run("all commands evaluated even after a deny", func(t *testing.T) {
		t.Parallel()
		script := "sudo rm -rf / && ls"
		eval := EvaluateScriptPermissionDetailed(config, script, EvaluatePermissionOptions{})

		// Extraction yields sudo's wrapped command too: [sudo rm -rf /, rm -rf /, ls]
		assert.Equal(t, PermissionDeny, eval.Outcome)
		require.Len(t, eval.Commands, 3)
		assert.Equal(t, PermissionDeny, eval.Commands[0].Outcome)
		assert.Equal(t, PermissionAutoApprove, eval.Commands[2].Outcome)

		result, msg := EvaluateScriptPermissionWithOptions(config, script, EvaluatePermissionOptions{})
		assert.Equal(t, PermissionDeny, result)
		assert.Equal(t, "no sudo allowed", msg)
	})

	t.Run("all auto-approved script aggregates advisories in wrapper", func(t *testing.T) {
		t.Parallel()
		script := "ls -la && echo hi"
		eval := EvaluateScriptPermissionDetailed(config, script, EvaluatePermissionOptions{})

		assert.Equal(t, PermissionAutoApprove, eval.Outcome)
		require.Len(t, eval.Commands, 2)

		result, msg := EvaluateScriptPermissionWithOptions(config, script, EvaluatePermissionOptions{})
		assert.Equal(t, PermissionAutoApprove, result)
		assert.Equal(t, "listing is fine", msg)
	})

	t.Run("heredoc file write deny factor", func(t *testing.T) {
		t.Parallel()
		script := "cat > /tmp/scratch.txt << 'EOF'\nhi\nEOF"
		eval := EvaluateScriptPermissionDetailed(config, script, EvaluatePermissionOptions{})

		assert.Equal(t, PermissionDeny, eval.Outcome)
		assert.Empty(t, eval.Commands)
		assert.Equal(t, []PermissionFactorKind{
			PermissionFactorTempPathAdvisory,
			PermissionFactorHeredocFileWriteDeny,
		}, factorKinds(eval.Factors))
		assert.Equal(t, PermissionDeny, eval.Factors[1].Outcome)

		result, msg := EvaluateScriptPermissionWithOptions(config, script, EvaluatePermissionOptions{})
		assert.Equal(t, PermissionDeny, result)
		assert.Contains(t, msg, "edit blocks")
		assert.Contains(t, msg, ".side/tmp")
	})

	t.Run("heredoc deny without temp path omits temp guidance", func(t *testing.T) {
		t.Parallel()
		script := "cat > out.txt << EOF\nhi\nEOF"
		eval := EvaluateScriptPermissionDetailed(config, script, EvaluatePermissionOptions{})

		assert.Equal(t, PermissionDeny, eval.Outcome)
		assert.Equal(t, []PermissionFactorKind{PermissionFactorHeredocFileWriteDeny}, factorKinds(eval.Factors))

		result, msg := EvaluateScriptPermissionWithOptions(config, script, EvaluatePermissionOptions{})
		assert.Equal(t, PermissionDeny, result)
		assert.Contains(t, msg, "edit blocks")
		assert.NotContains(t, msg, ".side/tmp")
	})

	t.Run("sandbox heredoc advisory factor", func(t *testing.T) {
		t.Parallel()
		opts := EvaluatePermissionOptions{HeredocFileWriteWarnInsteadOfDeny: true, DefaultAutoApprove: true}
		script := "cat > out.txt << EOF\nhi\nEOF"
		eval := EvaluateScriptPermissionDetailed(config, script, opts)

		assert.Equal(t, PermissionAutoApprove, eval.Outcome)
		assert.Equal(t, []PermissionFactorKind{PermissionFactorSandboxHeredocAdvisory}, factorKinds(eval.Factors))
		assert.Empty(t, eval.Factors[0].Outcome, "advisory factor should not carry an outcome")

		result, msg := EvaluateScriptPermissionWithOptions(config, script, opts)
		assert.Equal(t, PermissionAutoApprove, result)
		assert.Contains(t, msg, "edit blocks")
	})

	t.Run("heredoc escape hatch forces approval via per-command factor", func(t *testing.T) {
		t.Parallel()
		script := "cat > some/path << ESCAPE_HATCH_EOF\nhello\nESCAPE_HATCH_EOF"
		eval := EvaluateScriptPermissionDetailed(config, script, EvaluatePermissionOptions{})

		assert.Equal(t, PermissionRequireApproval, eval.Outcome)
		require.Len(t, eval.Commands, 1)
		cmdEval := eval.Commands[0]
		assert.Equal(t, PermissionRequireApproval, cmdEval.Outcome)
		assert.Equal(t, DecidedByFactor, cmdEval.DecidedBy)
		require.Less(t, cmdEval.DecidedByIndex, len(cmdEval.Factors))
		assert.Equal(t, PermissionFactorHeredocEscapeHatch, cmdEval.Factors[cmdEval.DecidedByIndex].Kind)

		result, msg := EvaluateScriptPermissionWithOptions(config, script, EvaluatePermissionOptions{})
		assert.Equal(t, PermissionRequireApproval, result)
		assert.Empty(t, msg)
	})

	t.Run("escape hatch retains matched rules from all lists while factor decides", func(t *testing.T) {
		t.Parallel()
		multiListConfig := CommandPermissionConfig{
			Deny:            []CommandPattern{{Pattern: "cat > /etc", Message: "no etc writes", Source: CommandPatternSourceWorkspaceConfig}},
			RequireApproval: []CommandPattern{{Pattern: "cat >", Source: CommandPatternSourceRepoConfig}},
			AutoApprove:     []CommandPattern{{Pattern: "cat", Source: CommandPatternSourceBase}},
		}
		script := "cat > /etc/motd << ESCAPE_HATCH_EOF\nhello\nESCAPE_HATCH_EOF"
		eval := EvaluateScriptPermissionDetailed(multiListConfig, script, EvaluatePermissionOptions{})

		assert.Equal(t, PermissionRequireApproval, eval.Outcome)
		require.Len(t, eval.Commands, 1)
		cmdEval := eval.Commands[0]
		assert.Equal(t, []MatchedRule{
			{Action: PermissionDeny, Pattern: "cat > /etc", Message: "no etc writes", Source: CommandPatternSourceWorkspaceConfig},
			{Action: PermissionRequireApproval, Pattern: "cat >", Source: CommandPatternSourceRepoConfig},
			{Action: PermissionAutoApprove, Pattern: "cat", Source: CommandPatternSourceBase},
		}, cmdEval.MatchedRules)
		assert.Equal(t, PermissionRequireApproval, cmdEval.Outcome)
		assert.Equal(t, DecidedByFactor, cmdEval.DecidedBy)
		require.Less(t, cmdEval.DecidedByIndex, len(cmdEval.Factors))
		assert.Equal(t, PermissionFactorHeredocEscapeHatch, cmdEval.Factors[cmdEval.DecidedByIndex].Kind)

		result, msg := EvaluateScriptPermissionWithOptions(multiListConfig, script, EvaluatePermissionOptions{})
		assert.Equal(t, PermissionRequireApproval, result)
		assert.Empty(t, msg)
	})

	t.Run("temp path advisory factor on auto-approved script", func(t *testing.T) {
		t.Parallel()
		opts := EvaluatePermissionOptions{SkipAbsolutePathEscalation: true}
		script := "echo hi | tee /tmp/out.log"
		eval := EvaluateScriptPermissionDetailed(config, script, opts)

		assert.Equal(t, PermissionAutoApprove, eval.Outcome)
		assert.Equal(t, []PermissionFactorKind{PermissionFactorTempPathAdvisory}, factorKinds(eval.Factors))
		assert.Empty(t, eval.Factors[0].Outcome, "advisory factor should not carry an outcome")

		result, msg := EvaluateScriptPermissionWithOptions(config, script, opts)
		assert.Equal(t, PermissionAutoApprove, result)
		assert.Contains(t, msg, ".side/tmp")
	})

	t.Run("empty extraction requires approval by default", func(t *testing.T) {
		t.Parallel()
		eval := EvaluateScriptPermissionDetailed(config, "", EvaluatePermissionOptions{})

		assert.Equal(t, PermissionRequireApproval, eval.Outcome)
		assert.Empty(t, eval.Commands)
		require.Equal(t, []PermissionFactorKind{PermissionFactorEmptyCommandExtraction}, factorKinds(eval.Factors))
		assert.Equal(t, PermissionRequireApproval, eval.Factors[0].Outcome)

		result, msg := EvaluateScriptPermissionWithOptions(config, "", EvaluatePermissionOptions{})
		assert.Equal(t, PermissionRequireApproval, result)
		assert.Empty(t, msg)
	})

	t.Run("empty extraction auto-approves in sandbox", func(t *testing.T) {
		t.Parallel()
		opts := EvaluatePermissionOptions{DefaultAutoApprove: true}
		eval := EvaluateScriptPermissionDetailed(config, "", opts)

		assert.Equal(t, PermissionAutoApprove, eval.Outcome)
		require.Equal(t, []PermissionFactorKind{PermissionFactorEmptyCommandExtraction}, factorKinds(eval.Factors))
		assert.Equal(t, PermissionAutoApprove, eval.Factors[0].Outcome)

		result, msg := EvaluateScriptPermissionWithOptions(config, "", opts)
		assert.Equal(t, PermissionAutoApprove, result)
		assert.Empty(t, msg)
	})
}
