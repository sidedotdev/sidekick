package permission

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractCommands_Seatbelt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		inner  []string
	}{
		{
			name:   "profile file",
			script: `sandbox-exec -f profile.sb echo hello`,
			inner:  []string{`echo hello`},
		},
		{
			name:   "named profile",
			script: `sandbox-exec -n no-network curl example.com`,
			inner:  []string{`curl example.com`},
		},
		{
			name:   "inline profile",
			script: `sandbox-exec -p '(version 1) (allow default)' echo hello`,
			inner:  []string{`echo hello`},
		},
		{
			name:   "profile parameters",
			script: `sandbox-exec -D ROOT=repo -D 'NAME=hello world' -f 'my profile.sb' echo hello`,
			inner:  []string{`echo hello`},
		},
		{
			name:   "attached option arguments",
			script: `sandbox-exec -DROOT=repo -fprofile.sb echo hello`,
			inner:  []string{`echo hello`},
		},
		{
			name:   "absolute executable path",
			script: `/usr/bin/sandbox-exec -f profile.sb echo hello`,
			inner:  []string{`echo hello`},
		},
		{
			name:   "end of options",
			script: `sandbox-exec -f profile.sb -- echo hello`,
			inner:  []string{`echo hello`},
		},
		{
			name:   "nested shell",
			script: `sandbox-exec -p '(version 1) (allow default)' bash -c 'echo hello; rm file'`,
			inner:  []string{`bash -c 'echo hello; rm file'`, `echo hello`, `rm file`},
		},
		{
			name:   "nested wrapper",
			script: `sandbox-exec -f profile.sb env MODE=test rm file`,
			inner:  []string{`env MODE=test rm file`, `rm file`},
		},
		{
			name:   "command options are preserved",
			script: `sandbox-exec -f profile.sb rm -f file`,
			inner:  []string{`rm -f file`},
		},
		{
			name:   "no command",
			script: `sandbox-exec -f profile.sb`,
		},
		{
			name:   "missing profile argument",
			script: `sandbox-exec -f`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			expected := append([]string{tt.script}, tt.inner...)
			assert.Equal(t, expected, ExtractCommands(tt.script))
			assert.Equal(t, []string{tt.script}, ExtractCommandsLegacy(tt.script))
		})
	}
}
