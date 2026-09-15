package env

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteModalExecResult: diagnostics are scripted, so a remote failure
// that reports success sends the reader chasing truncated or missing output
// as if it were the real in-sandbox state.
func TestWriteModalExecResult(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		exitCode int
		stderr   string
		wantErr  bool
	}{
		{name: "successful script", exitCode: 0, wantErr: false},
		{name: "failed script", exitCode: 1, stderr: "NameError: name 'PY' is not defined\n", wantErr: true},
		{name: "signalled script", exitCode: 143, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out strings.Builder
			err := writeModalExecResult(&out, "sb-1", "partial output\n", tc.stderr, tc.exitCode)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "exited with code")
			} else {
				require.NoError(t, err)
			}
			assert.Contains(t, out.String(), "partial output\n", "output must be reported either way")
			if tc.stderr != "" {
				assert.Contains(t, out.String(), tc.stderr)
			}
		})
	}
}