package gotestreport_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/gotestreport"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTempModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// go.mod
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644))

	// passing package
	passDir := filepath.Join(dir, "passing")
	require.NoError(t, os.MkdirAll(passDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(passDir, "pass_test.go"), []byte(`package passing

import (
	"fmt"
	"testing"
)

func TestPass(t *testing.T) {
	fmt.Println("this output should be suppressed")
}
`), 0644))

	// skipped package
	skipDir := filepath.Join(dir, "skipping")
	require.NoError(t, os.MkdirAll(skipDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skipDir, "skip_test.go"), []byte(`package skipping

import (
	"fmt"
	"testing"
)

func TestSkipped(t *testing.T) {
	fmt.Println("skip output should appear")
	t.Skip("skipping this test")
}
`), 0644))

	// failing package
	failDir := filepath.Join(dir, "failing")
	require.NoError(t, os.MkdirAll(failDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(failDir, "fail_test.go"), []byte(`package failing

import (
	"fmt"
	"testing"
)

func TestFail(t *testing.T) {
	fmt.Println("fail output should appear")
	t.Fatal("intentional failure")
}
`), 0644))

	return dir
}

func TestIntegrationStreamingAggregator(t *testing.T) {
	t.Parallel()

	dir := setupTempModule(t)

	cmd := exec.Command("go", "test", "-json", "./...")
	cmd.Dir = dir

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)

	var buf bytes.Buffer
	streamer := gotestreport.NewStreamer(&buf)

	require.NoError(t, cmd.Start())
	require.NoError(t, streamer.ProcessReader(stdout))
	// go test exits non-zero when tests fail; that's expected
	_ = cmd.Wait()

	output := buf.String()
	summary := streamer.Summary()

	// Passing test output should be suppressed
	assert.NotContains(t, output, "this output should be suppressed")

	// Skipped test output should appear
	assert.Contains(t, output, "skip output should appear")

	// Failed test output should appear
	assert.Contains(t, output, "fail output should appear")

	// Summary should exist
	assert.Contains(t, summary, "=== Summary ===")

	// Since there's a failure, summary should only show the failed package
	assert.Contains(t, summary, "FAIL")
	assert.Contains(t, summary, "testmod/failing")
	assert.NotContains(t, summary, "testmod/passing")
	assert.NotContains(t, summary, "testmod/skipping")

	assert.True(t, streamer.HasFailures())
}

func TestIntegrationAllPassAndSkip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644))

	// passing package
	passDir := filepath.Join(dir, "passing")
	require.NoError(t, os.MkdirAll(passDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(passDir, "pass_test.go"), []byte(`package passing

import "testing"

func TestPass(t *testing.T) {}
`), 0644))

	// skipped package
	skipDir := filepath.Join(dir, "skipping")
	require.NoError(t, os.MkdirAll(skipDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skipDir, "skip_test.go"), []byte(`package skipping

import "testing"

func TestSkipped(t *testing.T) {
	t.Skip("skipping")
}
`), 0644))

	cmd := exec.Command("go", "test", "-json", "./...")
	cmd.Dir = dir

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)

	var buf bytes.Buffer
	streamer := gotestreport.NewStreamer(&buf)

	require.NoError(t, cmd.Start())
	require.NoError(t, streamer.ProcessReader(stdout))
	require.NoError(t, cmd.Wait())

	summary := streamer.Summary()

	// No failures: summary should show all packages
	assert.Contains(t, summary, "=== Summary ===")
	assert.Contains(t, summary, "testmod/passing")
	assert.Contains(t, summary, "testmod/skipping")

	// Verify SKIP status appears for the skipped package
	lines := strings.Split(summary, "\n")
	for _, line := range lines {
		if strings.Contains(line, "testmod/skipping") {
			assert.Contains(t, line, "SKIP")
		}
	}

	assert.False(t, streamer.HasFailures())
}
