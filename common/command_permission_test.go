package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeCommandPermissions(t *testing.T) {
	t.Run("empty configs returns empty result", func(t *testing.T) {
		result := MergeCommandPermissions()
		assert.Empty(t, result.AutoApprove)
		assert.Empty(t, result.RequireApproval)
		assert.Empty(t, result.Deny)
	})

	t.Run("single config returns same config", func(t *testing.T) {
		cfg := CommandPermissionConfig{
			AutoApprove:     []CommandPattern{{Pattern: "ls"}},
			RequireApproval: []CommandPattern{{Pattern: "git push"}},
			Deny:            []CommandPattern{{Pattern: "rm -rf /", Message: "dangerous"}},
		}

		result := MergeCommandPermissions(cfg)

		assert.Equal(t, cfg.AutoApprove, result.AutoApprove)
		assert.Equal(t, cfg.RequireApproval, result.RequireApproval)
		assert.Equal(t, cfg.Deny, result.Deny)
	})

	t.Run("basic appending behavior", func(t *testing.T) {
		base := CommandPermissionConfig{
			AutoApprove:     []CommandPattern{{Pattern: "ls"}, {Pattern: "cat"}},
			RequireApproval: []CommandPattern{{Pattern: "git push"}},
			Deny:            []CommandPattern{{Pattern: "sudo", Message: "no sudo"}},
		}
		local := CommandPermissionConfig{
			AutoApprove:     []CommandPattern{{Pattern: "echo"}},
			RequireApproval: []CommandPattern{{Pattern: "npm publish"}},
			Deny:            []CommandPattern{{Pattern: "rm -rf /", Message: "dangerous"}},
		}

		result := MergeCommandPermissions(base, local)

		assert.Len(t, result.AutoApprove, 3)
		assert.Equal(t, "ls", result.AutoApprove[0].Pattern)
		assert.Equal(t, "cat", result.AutoApprove[1].Pattern)
		assert.Equal(t, "echo", result.AutoApprove[2].Pattern)

		assert.Len(t, result.RequireApproval, 2)
		assert.Equal(t, "git push", result.RequireApproval[0].Pattern)
		assert.Equal(t, "npm publish", result.RequireApproval[1].Pattern)

		assert.Len(t, result.Deny, 2)
		assert.Equal(t, "sudo", result.Deny[0].Pattern)
		assert.Equal(t, "rm -rf /", result.Deny[1].Pattern)
	})

	t.Run("reset auto-approve clears previous entries", func(t *testing.T) {
		base := CommandPermissionConfig{
			AutoApprove: []CommandPattern{{Pattern: "ls"}, {Pattern: "cat"}},
		}
		local := CommandPermissionConfig{
			AutoApprove:      []CommandPattern{{Pattern: "echo"}},
			ResetAutoApprove: true,
		}

		result := MergeCommandPermissions(base, local)

		assert.Len(t, result.AutoApprove, 1)
		assert.Equal(t, "echo", result.AutoApprove[0].Pattern)
	})

	t.Run("reset require-approval clears previous entries", func(t *testing.T) {
		base := CommandPermissionConfig{
			RequireApproval: []CommandPattern{{Pattern: "git push"}, {Pattern: "npm publish"}},
		}
		local := CommandPermissionConfig{
			RequireApproval:      []CommandPattern{{Pattern: "docker push"}},
			ResetRequireApproval: true,
		}

		result := MergeCommandPermissions(base, local)

		assert.Len(t, result.RequireApproval, 1)
		assert.Equal(t, "docker push", result.RequireApproval[0].Pattern)
	})

	t.Run("deny always accumulates regardless of reset flags", func(t *testing.T) {
		base := CommandPermissionConfig{
			Deny: []CommandPattern{{Pattern: "sudo", Message: "no sudo"}},
		}
		local := CommandPermissionConfig{
			Deny:                 []CommandPattern{{Pattern: "rm -rf /", Message: "dangerous"}},
			ResetAutoApprove:     true,
			ResetRequireApproval: true,
		}

		result := MergeCommandPermissions(base, local)

		assert.Len(t, result.Deny, 2)
		assert.Equal(t, "sudo", result.Deny[0].Pattern)
		assert.Equal(t, "rm -rf /", result.Deny[1].Pattern)
	})

	t.Run("multiple configs merge in order", func(t *testing.T) {
		base := CommandPermissionConfig{
			AutoApprove: []CommandPattern{{Pattern: "ls"}},
			Deny:        []CommandPattern{{Pattern: "sudo"}},
		}
		local := CommandPermissionConfig{
			AutoApprove: []CommandPattern{{Pattern: "cat"}},
			Deny:        []CommandPattern{{Pattern: "rm -rf /"}},
		}
		repo := CommandPermissionConfig{
			AutoApprove: []CommandPattern{{Pattern: "go test"}},
			Deny:        []CommandPattern{{Pattern: "chmod 777"}},
		}
		workspace := CommandPermissionConfig{
			AutoApprove: []CommandPattern{{Pattern: "npm test"}},
			Deny:        []CommandPattern{{Pattern: "mkfs"}},
		}

		result := MergeCommandPermissions(base, local, repo, workspace)

		assert.Len(t, result.AutoApprove, 4)
		assert.Equal(t, "ls", result.AutoApprove[0].Pattern)
		assert.Equal(t, "cat", result.AutoApprove[1].Pattern)
		assert.Equal(t, "go test", result.AutoApprove[2].Pattern)
		assert.Equal(t, "npm test", result.AutoApprove[3].Pattern)

		assert.Len(t, result.Deny, 4)
		assert.Equal(t, "sudo", result.Deny[0].Pattern)
		assert.Equal(t, "rm -rf /", result.Deny[1].Pattern)
		assert.Equal(t, "chmod 777", result.Deny[2].Pattern)
		assert.Equal(t, "mkfs", result.Deny[3].Pattern)
	})

	t.Run("reset in middle config clears only previous entries", func(t *testing.T) {
		base := CommandPermissionConfig{
			AutoApprove: []CommandPattern{{Pattern: "ls"}},
		}
		local := CommandPermissionConfig{
			AutoApprove:      []CommandPattern{{Pattern: "cat"}},
			ResetAutoApprove: true,
		}
		repo := CommandPermissionConfig{
			AutoApprove: []CommandPattern{{Pattern: "go test"}},
		}

		result := MergeCommandPermissions(base, local, repo)

		assert.Len(t, result.AutoApprove, 2)
		assert.Equal(t, "cat", result.AutoApprove[0].Pattern)
		assert.Equal(t, "go test", result.AutoApprove[1].Pattern)
	})

	t.Run("empty config in chain is handled correctly", func(t *testing.T) {
		base := CommandPermissionConfig{
			AutoApprove: []CommandPattern{{Pattern: "ls"}},
			Deny:        []CommandPattern{{Pattern: "sudo"}},
		}
		empty := CommandPermissionConfig{}
		repo := CommandPermissionConfig{
			AutoApprove: []CommandPattern{{Pattern: "go test"}},
		}

		result := MergeCommandPermissions(base, empty, repo)

		assert.Len(t, result.AutoApprove, 2)
		assert.Equal(t, "ls", result.AutoApprove[0].Pattern)
		assert.Equal(t, "go test", result.AutoApprove[1].Pattern)

		assert.Len(t, result.Deny, 1)
		assert.Equal(t, "sudo", result.Deny[0].Pattern)
	})

	t.Run("reset with empty list clears all previous", func(t *testing.T) {
		base := CommandPermissionConfig{
			AutoApprove:     []CommandPattern{{Pattern: "ls"}, {Pattern: "cat"}},
			RequireApproval: []CommandPattern{{Pattern: "git push"}},
		}
		local := CommandPermissionConfig{
			ResetAutoApprove:     true,
			ResetRequireApproval: true,
		}

		result := MergeCommandPermissions(base, local)

		assert.Empty(t, result.AutoApprove)
		assert.Empty(t, result.RequireApproval)
	})

	t.Run("message field is preserved", func(t *testing.T) {
		base := CommandPermissionConfig{
			Deny: []CommandPattern{{Pattern: "sudo", Message: "no sudo allowed"}},
		}
		local := CommandPermissionConfig{
			Deny: []CommandPattern{{Pattern: "rm -rf /", Message: "dangerous operation"}},
		}

		result := MergeCommandPermissions(base, local)

		assert.Len(t, result.Deny, 2)
		assert.Equal(t, "no sudo allowed", result.Deny[0].Message)
		assert.Equal(t, "dangerous operation", result.Deny[1].Message)
	})
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		name          string
		pattern       string
		command       string
		expectMatch   bool
		expectMatches []string
	}{
		{
			name:          "exact prefix match",
			pattern:       "ls",
			command:       "ls -la",
			expectMatch:   true,
			expectMatches: []string{"ls"},
		},
		{
			name:          "exact prefix match with full command",
			pattern:       "git status",
			command:       "git status --short",
			expectMatch:   true,
			expectMatches: []string{"git status"},
		},
		{
			name:        "prefix no match",
			pattern:     "cat",
			command:     "ls -la",
			expectMatch: false,
		},
		{
			name:          "regex pattern with dot",
			pattern:       "rm.*-rf",
			command:       "rm -rf /tmp",
			expectMatch:   true,
			expectMatches: []string{"rm -rf"},
		},
		{
			name:          "regex pattern with capture group",
			pattern:       `rm -rf (.+)`,
			command:       "rm -rf /home/user",
			expectMatch:   true,
			expectMatches: []string{"rm -rf /home/user", "/home/user"},
		},
		{
			name:        "regex no match",
			pattern:     "^sudo.*",
			command:     "ls -la",
			expectMatch: false,
		},
		{
			name:          "pattern already anchored",
			pattern:       "^git push",
			command:       "git push origin main",
			expectMatch:   true,
			expectMatches: []string{"git push"},
		},
		{
			name:        "invalid regex returns no match",
			pattern:     "[invalid",
			command:     "anything",
			expectMatch: false,
		},
		{
			name:          "pattern with pipe metachar",
			pattern:       "cat|dog",
			command:       "cat file.txt",
			expectMatch:   true,
			expectMatches: []string{"cat"},
		},
		{
			name:          "pattern with question mark",
			pattern:       "tests?",
			command:       "test foo",
			expectMatch:   true,
			expectMatches: []string{"test"},
		},
		{
			name:          "pattern with plus",
			pattern:       "go+gle",
			command:       "google search",
			expectMatch:   true,
			expectMatches: []string{"google"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, matches := matchPattern(tt.pattern, tt.command)
			assert.Equal(t, tt.expectMatch, matched)
			if tt.expectMatch {
				assert.Equal(t, tt.expectMatches, matches)
			}
		})
	}
}

func TestInterpolateMessage(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		matches  []string
		expected string
	}{
		{
			name:     "no placeholders",
			message:  "This is a simple message",
			matches:  []string{"match"},
			expected: "This is a simple message",
		},
		{
			name:     "single placeholder $0",
			message:  "Command '$0' is not allowed",
			matches:  []string{"sudo rm"},
			expected: "Command 'sudo rm' is not allowed",
		},
		{
			name:     "multiple placeholders",
			message:  "Cannot run $0, specifically $1",
			matches:  []string{"rm -rf /home", "/home"},
			expected: "Cannot run rm -rf /home, specifically /home",
		},
		{
			name:     "empty matches",
			message:  "Message with $0",
			matches:  []string{},
			expected: "Message with $0",
		},
		{
			name:     "placeholder not in matches",
			message:  "Value: $5",
			matches:  []string{"a", "b"},
			expected: "Value: $5",
		},
		{
			name:     "multiple occurrences of same placeholder",
			message:  "$0 and $0 again",
			matches:  []string{"test"},
			expected: "test and test again",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := interpolateMessage(tt.message, tt.matches)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEvaluateCommandPermission(t *testing.T) {
	config := CommandPermissionConfig{
		AutoApprove: []CommandPattern{
			{Pattern: "ls"},
			{Pattern: "cat"},
			{Pattern: "git status"},
			{Pattern: "go test"},
		},
		RequireApproval: []CommandPattern{
			{Pattern: "git push"},
			{Pattern: "npm publish"},
		},
		Deny: []CommandPattern{
			{Pattern: "sudo", Message: "sudo commands require manual execution"},
			{Pattern: "rm -rf /", Message: "Cannot delete root directory"},
			{Pattern: `rmdir --recursive (.+)`, Message: "Cannot recursively delete $1"},
		},
	}

	tests := []struct {
		name           string
		command        string
		expectedResult PermissionResult
		expectedMsg    string
	}{
		{
			name:           "auto-approve exact match",
			command:        "ls",
			expectedResult: PermissionAutoApprove,
			expectedMsg:    "",
		},
		{
			name:           "auto-approve prefix match",
			command:        "ls -la /tmp",
			expectedResult: PermissionAutoApprove,
			expectedMsg:    "",
		},
		{
			name:           "auto-approve multi-word pattern",
			command:        "git status --short",
			expectedResult: PermissionAutoApprove,
			expectedMsg:    "",
		},
		{
			name:           "require-approval match",
			command:        "git push origin main",
			expectedResult: PermissionRequireApproval,
			expectedMsg:    "",
		},
		{
			name:           "deny match with message",
			command:        "sudo apt-get install",
			expectedResult: PermissionDeny,
			expectedMsg:    "sudo commands require manual execution",
		},
		{
			name:           "deny match with interpolation",
			command:        "rmdir --recursive /home/user",
			expectedResult: PermissionDeny,
			expectedMsg:    "Cannot recursively delete /home/user",
		},
		{
			name:           "unknown command defaults to require-approval",
			command:        "some-unknown-command",
			expectedResult: PermissionRequireApproval,
			expectedMsg:    "",
		},
		{
			name:           "deny takes precedence over auto-approve",
			command:        "sudo ls",
			expectedResult: PermissionDeny,
			expectedMsg:    "sudo commands require manual execution",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, msg := EvaluateCommandPermission(config, tt.command)
			assert.Equal(t, tt.expectedResult, result)
			assert.Equal(t, tt.expectedMsg, msg)
		})
	}
}

func TestEvaluateCommandPermission_EmptyConfig(t *testing.T) {
	config := CommandPermissionConfig{}

	result, msg := EvaluateCommandPermission(config, "any command")
	assert.Equal(t, PermissionRequireApproval, result)
	assert.Empty(t, msg)
}

func TestEvaluateCommandPermission_DenyPrecedence(t *testing.T) {
	config := CommandPermissionConfig{
		AutoApprove: []CommandPattern{
			{Pattern: "rm"},
		},
		Deny: []CommandPattern{
			{Pattern: "rm -rf", Message: "dangerous"},
		},
	}

	// "rm" alone should be auto-approved
	result, _ := EvaluateCommandPermission(config, "rm file.txt")
	assert.Equal(t, PermissionAutoApprove, result)

	// "rm -rf" should be denied even though "rm" is auto-approved
	result, msg := EvaluateCommandPermission(config, "rm -rf /tmp")
	assert.Equal(t, PermissionDeny, result)
	assert.Equal(t, "dangerous", msg)
}

func TestEvaluateScriptPermission(t *testing.T) {
	config := CommandPermissionConfig{
		AutoApprove: []CommandPattern{
			{Pattern: "ls"},
			{Pattern: "cat"},
			{Pattern: "echo"},
		},
		RequireApproval: []CommandPattern{
			{Pattern: "git push"},
		},
		Deny: []CommandPattern{
			{Pattern: "sudo", Message: "no sudo allowed"},
		},
	}

	tests := []struct {
		name           string
		script         string
		expectedResult PermissionResult
		expectedMsg    string
	}{
		{
			name:           "all commands auto-approved",
			script:         "ls -la && cat file.txt",
			expectedResult: PermissionAutoApprove,
			expectedMsg:    "",
		},
		{
			name:           "single auto-approved command",
			script:         "echo hello",
			expectedResult: PermissionAutoApprove,
			expectedMsg:    "",
		},
		{
			name:           "one command requires approval",
			script:         "ls -la && git push origin main",
			expectedResult: PermissionRequireApproval,
			expectedMsg:    "",
		},
		{
			name:           "one command denied",
			script:         "ls -la && sudo apt-get update",
			expectedResult: PermissionDeny,
			expectedMsg:    "no sudo allowed",
		},
		{
			name:           "denied command stops evaluation",
			script:         "sudo rm -rf / && ls",
			expectedResult: PermissionDeny,
			expectedMsg:    "no sudo allowed",
		},
		{
			name:           "unknown command requires approval",
			script:         "ls && unknown-command",
			expectedResult: PermissionRequireApproval,
			expectedMsg:    "",
		},
		{
			name:           "empty script requires approval",
			script:         "",
			expectedResult: PermissionRequireApproval,
			expectedMsg:    "",
		},
		{
			name:           "piped commands",
			script:         "cat file.txt | grep pattern",
			expectedResult: PermissionRequireApproval,
			expectedMsg:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, msg := EvaluateScriptPermission(config, tt.script)
			assert.Equal(t, tt.expectedResult, result, "unexpected result for script: %s", tt.script)
			assert.Equal(t, tt.expectedMsg, msg)
		})
	}
}

func TestEvaluateScriptPermission_WithBasePermissions(t *testing.T) {
	config := BaseCommandPermissions()

	t.Run("common safe commands are auto-approved", func(t *testing.T) {
		safeScripts := []string{
			"ls -la",
			"cat README.md",
			"git status",
			"git log --oneline",
			"go test ./...",
			"pwd",
		}

		for _, script := range safeScripts {
			result, _ := EvaluateScriptPermission(config, script)
			assert.Equal(t, PermissionAutoApprove, result, "expected auto-approve for: %s", script)
		}
	})

	t.Run("dangerous commands are denied", func(t *testing.T) {
		dangerousScripts := []string{
			"sudo apt-get install",
			"rm -rf /",
			"chmod 777 /etc",
		}

		for _, script := range dangerousScripts {
			result, msg := EvaluateScriptPermission(config, script)
			assert.Equal(t, PermissionDeny, result, "expected deny for: %s", script)
			require.NotEmpty(t, msg, "expected message for denied command: %s", script)
		}
	})

	t.Run("sensitive commands require approval", func(t *testing.T) {
		sensitiveScripts := []string{
			"env",
			"printenv",
			"curl https://example.com",
			"wget http://example.com/file",
			"cat .env",
			"cat .envrc",
			"source .env",
			"grep password .env",
		}

		for _, script := range sensitiveScripts {
			result, _ := EvaluateScriptPermission(config, script)
			assert.Equal(t, PermissionRequireApproval, result, "expected require-approval for: %s", script)
		}
	})

	t.Run("require-approval takes precedence over user auto-approve", func(t *testing.T) {
		// Simulate a user config that tries to auto-approve curl
		userConfig := CommandPermissionConfig{
			AutoApprove: []CommandPattern{
				{Pattern: "curl"},
			},
		}
		mergedConfig := MergeCommandPermissions(BaseCommandPermissions(), userConfig)

		// curl should still require approval because base RequireApproval takes precedence
		result, _ := EvaluateCommandPermission(mergedConfig, "curl https://example.com")
		assert.Equal(t, PermissionRequireApproval, result, "require-approval should take precedence over auto-approve")
	})
}
