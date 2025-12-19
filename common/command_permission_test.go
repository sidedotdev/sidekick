package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
