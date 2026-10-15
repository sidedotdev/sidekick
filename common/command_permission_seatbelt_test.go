package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEvaluateScriptPermission_Seatbelt(t *testing.T) {
	t.Parallel()
	config := BaseCommandPermissions()

	tests := []struct {
		name   string
		script string
		opts   EvaluatePermissionOptions
		want   PermissionResult
	}{
		{
			name:   "inline profile with ls",
			script: `sandbox-exec -p '(version 1) (allow default)' ls`,
			want:   PermissionAutoApprove,
		},
		{
			name:   "profile file",
			script: `sandbox-exec -f profile.sb ls`,
			want:   PermissionAutoApprove,
		},
		{
			name:   "named profile",
			script: `sandbox-exec -n no-network ls`,
			want:   PermissionAutoApprove,
		},
		{
			name:   "profile parameters",
			script: `sandbox-exec -D ROOT=repo -f profile.sb ls`,
			want:   PermissionAutoApprove,
		},
		{
			name:   "attached option arguments",
			script: `sandbox-exec -DROOT=repo -fprofile.sb ls`,
			want:   PermissionAutoApprove,
		},
		{
			name:   "end of options",
			script: `sandbox-exec -f profile.sb -- ls`,
			want:   PermissionAutoApprove,
		},
		{
			name:   "absolute executable with path escalation disabled",
			script: `/usr/bin/sandbox-exec -f profile.sb ls`,
			opts:   EvaluatePermissionOptions{SkipAbsolutePathEscalation: true},
			want:   PermissionAutoApprove,
		},
		{
			name:   "nested allowed wrapper",
			script: `sandbox-exec -f profile.sb timeout 5 ls`,
			want:   PermissionAutoApprove,
		},
		{
			name:   "unapproved inner command",
			script: `sandbox-exec -f profile.sb rm file`,
			want:   PermissionRequireApproval,
		},
		{
			name:   "network inner command",
			script: `sandbox-exec -f profile.sb curl example.com`,
			want:   PermissionRequireApproval,
		},
		{
			name:   "denied inner command",
			script: `sandbox-exec -f profile.sb rm -rf /`,
			want:   PermissionDeny,
		},
		{
			name:   "denied command through nested wrapper",
			script: `sandbox-exec -f profile.sb sandbox-exec -f profile.sb rm -rf /`,
			want:   PermissionDeny,
		},
		{
			name:   "absolute profile still requires approval",
			script: `sandbox-exec -f /private/profile.sb ls`,
			want:   PermissionRequireApproval,
		},
		{
			name:   "similar executable is not allowed",
			script: `sandbox-exec-custom -f profile.sb ls`,
			want:   PermissionRequireApproval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, _ := EvaluateScriptPermissionWithOptions(config, tt.script, tt.opts)
			assert.Equal(t, tt.want, result)
		})
	}
}
