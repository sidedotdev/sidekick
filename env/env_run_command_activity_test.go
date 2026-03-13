package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func TestTruncateMiddle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		maxBytes   int
		wantExact  string // if non-empty, expect exact match
		wantMaxLen int    // if wantExact is empty, check length upper bound
		wantMarker bool   // expect truncation marker
		wantPrefix string // expected prefix after truncation
		wantSuffix string // expected suffix after truncation
	}{
		{
			name:      "no truncation when under limit",
			input:     "hello world",
			maxBytes:  100,
			wantExact: "hello world",
		},
		{
			name:      "no truncation at exact limit",
			input:     strings.Repeat("A", 100),
			maxBytes:  100,
			wantExact: strings.Repeat("A", 100),
		},
		{
			name:      "empty string",
			input:     "",
			maxBytes:  100,
			wantExact: "",
		},
		{
			name:       "truncates when over limit",
			input:      strings.Repeat("A", 300),
			maxBytes:   200,
			wantMaxLen: 200,
			wantMarker: true,
			wantPrefix: "AAAA",
			wantSuffix: "...]\n\n",
		},
		{
			name:       "very small maxBytes falls back to prefix slice",
			input:      strings.Repeat("B", 200),
			maxBytes:   10,
			wantMaxLen: 10,
		},
		{
			name:       "marker includes byte count",
			input:      strings.Repeat("C", 1000),
			maxBytes:   500,
			wantMaxLen: 500,
			wantMarker: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := truncateMiddle(tt.input, tt.maxBytes)

			if tt.wantExact != "" {
				assert.Equal(t, tt.wantExact, result)
				return
			}

			assert.LessOrEqual(t, len(result), tt.wantMaxLen, "result length should be at most maxBytes")

			if tt.wantMarker {
				assert.Contains(t, result, "[... truncated")
				assert.Contains(t, result, "bytes from the middle ...]")
			}
			if tt.wantPrefix != "" {
				assert.True(t, strings.HasPrefix(result, tt.wantPrefix), "expected prefix %q", tt.wantPrefix)
			}
			if tt.wantSuffix != "" {
				assert.True(t, strings.HasSuffix(result, tt.wantSuffix), "expected suffix %q", tt.wantSuffix)
			}
		})
	}
}

func TestTruncateMiddle_PreservesStartAndEnd(t *testing.T) {
	t.Parallel()

	head := strings.Repeat("H", 500)
	tail := strings.Repeat("T", 500)
	middle := strings.Repeat("M", 2000)
	input := head + middle + tail

	result := truncateMiddle(input, 1200)
	assert.Equal(t, 1200, len(result))
	assert.True(t, strings.HasPrefix(result, "HHHH"))
	assert.Contains(t, result, "TTTT")
	assert.Contains(t, result, "[... truncated")
	assert.True(t, strings.HasSuffix(result, "...]\n\n"), "trailing marker expected")
}

func TestEnvRunCommandActivity_TruncatesLargeOutput(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	bigContent := strings.Repeat("X", maxActivityOutputBytes+1024*1024)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "big.txt"), []byte(bigContent), 0644))

	input := EnvRunCommandActivityInput{
		EnvContainer: EnvContainer{Env: &LocalEnv{WorkingDirectory: tmpDir}},
		Command:      "cat",
		Args:         []string{"big.txt"},
	}

	testSuite := &testsuite.WorkflowTestSuite{}
	actEnv := testSuite.NewTestActivityEnvironment()
	actEnv.RegisterActivity(EnvRunCommandActivity)

	result, err := actEnv.ExecuteActivity(EnvRunCommandActivity, input)
	require.NoError(t, err)

	var output EnvRunCommandActivityOutput
	require.NoError(t, result.Get(&output))

	assert.Equal(t, 0, output.ExitStatus)
	assert.LessOrEqual(t, len(output.Stdout), maxActivityOutputBytes,
		"stdout should be truncated to maxActivityOutputBytes")
	assert.Contains(t, output.Stdout, "[... truncated")
	assert.True(t, strings.HasPrefix(output.Stdout, "XXXX"))
	assert.True(t, strings.HasSuffix(output.Stdout, "...]\n\n"), "trailing marker expected")
}

func TestEnvRunCommandActivity_NoTruncationUnderLimit(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	smallContent := strings.Repeat("Y", 1024)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "small.txt"), []byte(smallContent), 0644))

	input := EnvRunCommandActivityInput{
		EnvContainer: EnvContainer{Env: &LocalEnv{WorkingDirectory: tmpDir}},
		Command:      "cat",
		Args:         []string{"small.txt"},
	}

	testSuite := &testsuite.WorkflowTestSuite{}
	actEnv := testSuite.NewTestActivityEnvironment()
	actEnv.RegisterActivity(EnvRunCommandActivity)

	result, err := actEnv.ExecuteActivity(EnvRunCommandActivity, input)
	require.NoError(t, err)

	var output EnvRunCommandActivityOutput
	require.NoError(t, result.Get(&output))

	assert.Equal(t, 0, output.ExitStatus)
	assert.Equal(t, smallContent, output.Stdout)
	assert.NotContains(t, output.Stdout, "[... truncated")
}

func TestEnvRunCommandActivity_TruncatesLargeStderr(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	bigContent := strings.Repeat("E", maxActivityOutputBytes+512*1024)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "big.txt"), []byte(bigContent), 0644))

	input := EnvRunCommandActivityInput{
		EnvContainer: EnvContainer{Env: &LocalEnv{WorkingDirectory: tmpDir}},
		Command:      "sh",
		Args:         []string{"-c", "cat big.txt >&2"},
	}

	testSuite := &testsuite.WorkflowTestSuite{}
	actEnv := testSuite.NewTestActivityEnvironment()
	actEnv.RegisterActivity(EnvRunCommandActivity)

	result, err := actEnv.ExecuteActivity(EnvRunCommandActivity, input)
	require.NoError(t, err)

	var output EnvRunCommandActivityOutput
	require.NoError(t, result.Get(&output))

	assert.LessOrEqual(t, len(output.Stderr), maxActivityOutputBytes,
		"stderr should be truncated to maxActivityOutputBytes")
	assert.Contains(t, output.Stderr, "[... truncated")
	assert.True(t, strings.HasPrefix(output.Stderr, "EEEE"))
	assert.True(t, strings.HasSuffix(output.Stderr, "...]\n\n"), "trailing marker expected")
}
