package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool, failMessage string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(failMessage)
}

func TestPersistentTakeoverFromPersistent(t *testing.T) {
	t.Parallel()

	// Create a temp directory for the socket
	tmpDir, err := os.MkdirTemp("", "supervisor-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use simple echo commands that exit quickly for testing
	testProcesses := []ProcessConfig{
		{
			Name:    "test1",
			Command: "sleep",
			Args:    []string{"30"},
		},
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	outputChan1 := make(chan processOutputMsg, 100)

	// Start first persistent supervisor
	sup1 := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	if err := sup1.StartIPCServer(ctx1, outputChan1); err != nil {
		t.Fatalf("failed to start IPC server for sup1: %v", err)
	}
	defer sup1.CloseIPC()

	sup1.StartAll(ctx1, outputChan1)

	// Wait for process to start
	waitForCondition(t, 5*time.Second, func() bool {
		return sup1.processes[0].isRunning()
	}, "sup1 process should be running")

	// Start second persistent supervisor - it should take over
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	outputChan2 := make(chan processOutputMsg, 100)

	sup2 := NewSupervisor(testProcesses, false, tmpDir, tmpDir)

	// Connect to existing supervisor (sup1)
	if err := sup2.ConnectToPersistent(); err != nil {
		t.Fatalf("failed to connect to persistent: %v", err)
	}

	// sup1's processes should now be stopped
	waitForCondition(t, 5*time.Second, func() bool {
		return !sup1.processes[0].isRunning()
	}, "sup1 process should have stopped after takeover")

	// sup1 should have received takeover signal
	select {
	case <-sup1.WaitForTakeover():
		// Expected
	case <-time.After(time.Second):
		t.Error("sup1 should have received takeover signal")
	}

	// Now sup2 can start its IPC server and processes
	if err := sup2.StartIPCServer(ctx2, outputChan2); err != nil {
		t.Fatalf("failed to start IPC server for sup2: %v", err)
	}
	defer sup2.CloseIPC()

	sup2.StartAll(ctx2, outputChan2)

	// Wait for process to start
	time.Sleep(100 * time.Millisecond)

	if !sup2.processes[0].isRunning() {
		t.Error("sup2 process should be running")
	}

	// Cleanup
	sup2.StopAll()
}

func TestDoublePersistentTakeover(t *testing.T) {
	t.Parallel()

	// Create a temp directory for the socket
	tmpDir, err := os.MkdirTemp("", "supervisor-test-double-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testProcesses := []ProcessConfig{
		{
			Name:    "test1",
			Command: "sleep",
			Args:    []string{"30"},
		},
	}

	// === SUPERVISOR 1 ===
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	outputChan1 := make(chan processOutputMsg, 100)

	sup1 := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	t.Logf("sup1 socket path: %s", sup1.socketPath)

	if err := sup1.StartIPCServer(ctx1, outputChan1); err != nil {
		t.Fatalf("failed to start IPC server for sup1: %v", err)
	}

	sup1.StartAll(ctx1, outputChan1)
	time.Sleep(100 * time.Millisecond)

	if !sup1.processes[0].isRunning() {
		t.Fatal("sup1 process should be running")
	}

	// === SUPERVISOR 2 takes over from 1 (simulating real main() flow) ===
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	outputChan2 := make(chan processOutputMsg, 100)

	sup2 := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	t.Logf("sup2 socket path: %s", sup2.socketPath)

	// Connect and takeover (like main does)
	if err := sup2.ConnectToPersistent(); err != nil {
		t.Fatalf("sup2 failed to connect to sup1: %v", err)
	}

	// Immediately start IPC server (like main does - BEFORE sup1 closes)
	if err := sup2.StartIPCServer(ctx2, outputChan2); err != nil {
		t.Fatalf("failed to start IPC server for sup2: %v", err)
	}

	sup2.StartAll(ctx2, outputChan2)

	// Now sup1 receives takeover and closes (simulating the goroutine in main)
	select {
	case <-sup1.WaitForTakeover():
		t.Log("sup1 received takeover signal")
	case <-time.After(2 * time.Second):
		t.Fatal("sup1 should have received takeover signal")
	}
	sup1.CloseIPC()
	t.Log("sup1 closed IPC")

	time.Sleep(100 * time.Millisecond)

	if !sup2.processes[0].isRunning() {
		t.Fatal("sup2 process should be running")
	}

	// Verify sup2's socket is accessible AFTER sup1 has closed
	socketPath := sup2.socketPath
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Fatalf("sup2 socket file does not exist at %s after sup1 closed", socketPath)
	}
	t.Logf("sup2 socket exists at %s", socketPath)

	// === SUPERVISOR 3 takes over from 2 ===
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	outputChan3 := make(chan processOutputMsg, 100)

	sup3 := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	t.Logf("sup3 socket path: %s", sup3.socketPath)

	// Connect and takeover
	if err := sup3.ConnectToPersistent(); err != nil {
		t.Fatalf("sup3 failed to connect to sup2: %v", err)
	}

	// Immediately start IPC server (like main does)
	if err := sup3.StartIPCServer(ctx3, outputChan3); err != nil {
		t.Fatalf("failed to start IPC server for sup3: %v", err)
	}

	sup3.StartAll(ctx3, outputChan3)

	// Now sup2 receives takeover and closes
	select {
	case <-sup2.WaitForTakeover():
		t.Log("sup2 received takeover signal")
	case <-time.After(2 * time.Second):
		t.Fatal("sup2 should have received takeover signal - THIS IS THE BUG")
	}
	sup2.CloseIPC()
	t.Log("sup2 closed IPC")

	time.Sleep(100 * time.Millisecond)

	if !sup3.processes[0].isRunning() {
		t.Fatal("sup3 process should be running")
	}

	// Verify sup3's socket is accessible
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Fatalf("sup3 socket file does not exist at %s after sup2 closed", socketPath)
	}
	t.Logf("sup3 socket exists at %s", socketPath)

	sup3.CloseIPC()
	sup3.StopAll()
}

func TestEphemeralTakeoverFromPersistent(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testProcesses := []ProcessConfig{
		{
			Name:    "test1",
			Command: "sleep",
			Args:    []string{"30"},
		},
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	outputChan1 := make(chan processOutputMsg, 100)

	// Start persistent supervisor
	sup1 := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	if err := sup1.StartIPCServer(ctx1, outputChan1); err != nil {
		t.Fatalf("failed to start IPC server: %v", err)
	}
	defer sup1.CloseIPC()

	sup1.StartAll(ctx1, outputChan1)

	waitForCondition(t, 5*time.Second, func() bool {
		return sup1.processes[0].isRunning()
	}, "sup1 process should be running")

	// Start ephemeral supervisor
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	outputChan2 := make(chan processOutputMsg, 100)

	sup2 := NewSupervisor(testProcesses, true, tmpDir, tmpDir)

	if err := sup2.ConnectToPersistent(); err != nil {
		t.Fatalf("failed to connect to persistent: %v", err)
	}
	defer sup2.ReleaseToPersistent()

	// sup1's processes should be stopped
	waitForCondition(t, 5*time.Second, func() bool {
		return !sup1.processes[0].isRunning()
	}, "sup1 process should have stopped")

	// sup1 should NOT have received takeover signal (it's waiting for ephemeral to release)
	select {
	case <-sup1.WaitForTakeover():
		t.Error("sup1 should NOT have received takeover signal for ephemeral takeover")
	case <-time.After(100 * time.Millisecond):
		// Expected
	}

	// sup2 starts its processes
	sup2.StartAll(ctx2, outputChan2)

	waitForCondition(t, 5*time.Second, func() bool {
		return sup2.processes[0].isRunning()
	}, "sup2 process should be running")

	// sup2 releases - sup1 should restart
	sup2.StopAll()
	sup2.ReleaseToPersistent()

	waitForCondition(t, 5*time.Second, func() bool {
		return sup1.processes[0].isRunning()
	}, "sup1 process should have restarted after ephemeral release")

	sup1.StopAll()
}

func TestEphemeralTakeoverFromEphemeral(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testProcesses := []ProcessConfig{
		{
			Name:    "test1",
			Command: "sleep",
			Args:    []string{"30"},
		},
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	outputChan1 := make(chan processOutputMsg, 100)

	// Start persistent supervisor
	sup1 := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	if err := sup1.StartIPCServer(ctx1, outputChan1); err != nil {
		t.Fatalf("failed to start IPC server: %v", err)
	}
	defer sup1.CloseIPC()

	sup1.StartAll(ctx1, outputChan1)
	// No need to verify sup1 running here as we do it implicitly by sup2 connecting

	// Start first ephemeral
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	outputChan2 := make(chan processOutputMsg, 100)

	sup2 := NewSupervisor(testProcesses, true, tmpDir, tmpDir)
	if err := sup2.ConnectToPersistent(); err != nil {
		t.Fatalf("failed to connect to persistent: %v", err)
	}

	// Wait for sup1 to stop if we wanted, but let's just proceed to start sup2
	sup2.StartAll(ctx2, outputChan2)

	waitForCondition(t, 5*time.Second, func() bool {
		return sup2.processes[0].isRunning()
	}, "sup2 process should be running")

	// Start second ephemeral - should take over from first
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()

	outputChan3 := make(chan processOutputMsg, 100)

	sup3 := NewSupervisor(testProcesses, true, tmpDir, tmpDir)
	if err := sup3.ConnectToPersistent(); err != nil {
		t.Fatalf("failed to connect to persistent: %v", err)
	}
	defer sup3.ReleaseToPersistent()

	// sup2 should have received takeover and stopped
	select {
	case <-sup2.WaitForTakeover():
		// Expected
	case <-time.After(5 * time.Second):
		t.Error("sup2 should have received takeover signal")
	}

	waitForCondition(t, 5*time.Second, func() bool {
		return !sup2.processes[0].isRunning()
	}, "sup2 process should have stopped")

	// sup3 starts
	sup3.StartAll(ctx3, outputChan3)

	waitForCondition(t, 5*time.Second, func() bool {
		return sup3.processes[0].isRunning()
	}, "sup3 process should be running")

	// sup3 releases - sup1 should restart (not sup2)
	sup3.StopAll()
	sup3.ReleaseToPersistent()

	waitForCondition(t, 5*time.Second, func() bool {
		return sup1.processes[0].isRunning()
	}, "sup1 process should have restarted")

	sup1.StopAll()
}

func TestSocketPath(t *testing.T) {
	t.Parallel()

	path1 := getSocketPath("/some/project/root")
	path2 := getSocketPath("/some/project/root")
	path3 := getSocketPath("/different/project")

	// Same project root should give same socket path
	if path1 != path2 {
		t.Errorf("same project root should give same socket path, got %s and %s", path1, path2)
	}

	// Different project roots should give different socket paths
	if path1 == path3 {
		t.Errorf("different project roots should give different socket paths")
	}

	// Should be in temp dir
	if !strings.HasPrefix(path1, os.TempDir()) {
		t.Errorf("socket path should be in temp dir, got %s", path1)
	}
}

func TestCrossWorktreeTakeover(t *testing.T) {
	t.Parallel()

	// Create two separate temp directories to simulate different worktrees
	worktreeA, err := os.MkdirTemp("", "supervisor-worktree-a-*")
	if err != nil {
		t.Fatalf("failed to create worktree A dir: %v", err)
	}
	defer os.RemoveAll(worktreeA)

	worktreeB, err := os.MkdirTemp("", "supervisor-worktree-b-*")
	if err != nil {
		t.Fatalf("failed to create worktree B dir: %v", err)
	}
	defer os.RemoveAll(worktreeB)

	// Use a common project ID to simulate same repo across worktrees
	commonProjectID := worktreeA + "-common"

	testProcesses := []ProcessConfig{
		{
			Name:    "test1",
			Command: "sleep",
			Args:    []string{"30"},
		},
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	outputChan1 := make(chan processOutputMsg, 100)

	// Start persistent supervisor in worktree A
	sup1 := NewSupervisor(testProcesses, false, commonProjectID, worktreeA)
	if err := sup1.StartIPCServer(ctx1, outputChan1); err != nil {
		t.Fatalf("failed to start IPC server: %v", err)
	}
	defer sup1.CloseIPC()

	sup1.StartAll(ctx1, outputChan1)

	waitForCondition(t, 5*time.Second, func() bool {
		return sup1.processes[0].isRunning()
	}, "sup1 process should be running")

	// Start ephemeral supervisor in worktree B with same project ID
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	outputChan2 := make(chan processOutputMsg, 100)

	sup2 := NewSupervisor(testProcesses, true, commonProjectID, worktreeB)

	// Should successfully connect despite different execution roots
	if err := sup2.ConnectToPersistent(); err != nil {
		t.Fatalf("failed to connect to persistent from different worktree: %v", err)
	}
	defer sup2.ReleaseToPersistent()

	// sup1's processes should be stopped
	waitForCondition(t, 5*time.Second, func() bool {
		return !sup1.processes[0].isRunning()
	}, "sup1 process should have stopped after cross-worktree takeover")

	// sup2 starts its processes
	sup2.StartAll(ctx2, outputChan2)

	waitForCondition(t, 5*time.Second, func() bool {
		return sup2.processes[0].isRunning()
	}, "sup2 process should be running")

	// Verify execution roots are different
	if sup1.executionRoot == sup2.executionRoot {
		t.Error("execution roots should be different")
	}

	// Verify project IDs are the same
	if sup1.projectID != sup2.projectID {
		t.Error("project IDs should be the same")
	}

	// sup2 releases - sup1 should restart
	sup2.StopAll()
	sup2.ReleaseToPersistent()

	waitForCondition(t, 5*time.Second, func() bool {
		return sup1.processes[0].isRunning()
	}, "sup1 process should have restarted after ephemeral release")

	sup1.StopAll()
}

func TestDifferentProjectIDsNoCoordination(t *testing.T) {
	t.Parallel()

	tmpDir1, err := os.MkdirTemp("", "supervisor-project1-*")
	if err != nil {
		t.Fatalf("failed to create temp dir 1: %v", err)
	}
	defer os.RemoveAll(tmpDir1)

	tmpDir2, err := os.MkdirTemp("", "supervisor-project2-*")
	if err != nil {
		t.Fatalf("failed to create temp dir 2: %v", err)
	}
	defer os.RemoveAll(tmpDir2)

	testProcesses := []ProcessConfig{
		{
			Name:    "test1",
			Command: "sleep",
			Args:    []string{"30"},
		},
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	outputChan1 := make(chan processOutputMsg, 100)

	// Start persistent supervisor with project ID "alpha"
	sup1 := NewSupervisor(testProcesses, false, "project-alpha", tmpDir1)
	if err := sup1.StartIPCServer(ctx1, outputChan1); err != nil {
		t.Fatalf("failed to start IPC server: %v", err)
	}
	defer sup1.CloseIPC()

	sup1.StartAll(ctx1, outputChan1)

	waitForCondition(t, 5*time.Second, func() bool {
		return sup1.processes[0].isRunning()
	}, "sup1 process should be running")

	// Start ephemeral supervisor with different project ID "beta"
	sup2 := NewSupervisor(testProcesses, true, "project-beta", tmpDir2)

	// ConnectToPersistent returns nil both when no supervisor is running
	// and when connection succeeds. With different project IDs, they use
	// different sockets, so sup2 won't find sup1's socket.
	err = sup2.ConnectToPersistent()
	if err != nil {
		t.Errorf("ConnectToPersistent should not error: %v", err)
	}

	// The key test: sup1's processes should still be running because
	// sup2 connected to a different socket (no takeover occurred)
	if !sup1.processes[0].isRunning() {
		t.Error("sup1 process should still be running (no takeover occurred)")
	}

	// sup2 should have no parent connection (nothing was listening on its socket)
	if sup2.parentConn != nil {
		t.Error("sup2 should have no parent connection with different project ID")
	}

	sup1.StopAll()
}

func TestEncodeDecodeProjectID(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple", "project-alpha", "project-alpha"},
		{"with slashes", "/home/user/project", "_shome_suser_sproject"},
		{"with underscores", "my_project_name", "my__project__name"},
		{"with spaces", "my project name", "my_pproject_pname"},
		{"with colons", "C:\\Users\\test", "C_c_bUsers_btest"},
		{"mixed special chars", "/home/user/my_project:test file", "_shome_suser_smy__project_ctest_pfile"},
		{"empty", "", ""},
		{"only special", "/_\\ :", "_s___b_p_c"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := encodeProjectID(tc.input)
			if encoded != tc.expected {
				t.Errorf("encodeProjectID(%q) = %q, want %q", tc.input, encoded, tc.expected)
			}
		})
	}

	// Verify round-trip: decode(encode(x)) == x
	for _, tc := range testCases {
		t.Run(tc.name+" round-trip", func(t *testing.T) {
			encoded := encodeProjectID(tc.input)
			decoded := decodeProjectID(encoded)
			if decoded != tc.input {
				t.Errorf("round-trip failed: encodeProjectID(%q) = %q, decodeProjectID(%q) = %q",
					tc.input, encoded, encoded, decoded)
			}
		})
	}

	// Verify encoding is injective (no collisions)
	encoded1 := encodeProjectID("a/b")
	encoded2 := encodeProjectID("a_b")
	if encoded1 == encoded2 {
		t.Errorf("encoding should be injective: 'a/b' -> %q, 'a_b' -> %q", encoded1, encoded2)
	}
}

func TestSocketPathWithProjectID(t *testing.T) {
	t.Parallel()

	path1 := getSocketPath("project-alpha")
	path2 := getSocketPath("project-alpha")
	path3 := getSocketPath("project-beta")

	// Same project ID should give same socket path
	if path1 != path2 {
		t.Errorf("same project ID should give same socket path, got %s and %s", path1, path2)
	}

	// Different project IDs should give different socket paths
	if path1 == path3 {
		t.Errorf("different project IDs should give different socket paths")
	}

	// Should be in temp dir
	if !strings.HasPrefix(path1, os.TempDir()) {
		t.Errorf("socket path should be in temp dir, got %s", path1)
	}

	// Socket path should not exceed unix domain socket limits
	if len(path1) > 100 {
		t.Errorf("socket path too long: %d bytes", len(path1))
	}

	// Verify prefix
	if !strings.Contains(path1, "sidesup-") {
		t.Errorf("socket path should contain sidesup- prefix, got %s", path1)
	}

	// Test with a very long project ID (simulating deep directory path)
	longProjectID := "/home/user/very/long/path/to/some/deeply/nested/project/directory/that/exceeds/normal/limits"
	longPath := getSocketPath(longProjectID)
	if len(longPath) > 100 {
		t.Errorf("long project ID should produce socket path <= 100 bytes, got %d", len(longPath))
	}

	// Long path should use rightmost segments
	longBase := filepath.Base(longPath)
	if !strings.Contains(longBase, "limits") {
		t.Errorf("long path should include rightmost segment 'limits', got %s", longBase)
	}

	// Extracted project ID should be usable as --project (idempotent)
	longExtracted := extractProjectIDFromSocketPath(longPath)
	longRoundTrip := getSocketPath(longExtracted)
	if longRoundTrip != longPath {
		t.Errorf("long path round-trip failed: extracted %q produces %q, want %q",
			longExtracted, longRoundTrip, longPath)
	}

	// Test round-trip for short project IDs
	shortExtracted := extractProjectIDFromSocketPath(path1)
	shortRoundTrip := getSocketPath(shortExtracted)
	if shortRoundTrip != path1 {
		t.Errorf("short round-trip failed: extracted %q produces %q, want %q", shortExtracted, shortRoundTrip, path1)
	}

	// Test that a/b and a_b produce different sockets (injective encoding)
	pathWithSlash := getSocketPath("a/b")
	pathWithUnderscore := getSocketPath("a_b")
	if pathWithSlash == pathWithUnderscore {
		t.Errorf("a/b and a_b should produce different socket paths, both got %s", pathWithSlash)
	}

	// Test round-trip for path with special characters
	specialPath := "/home/user/my project:test"
	specialSocketPath := getSocketPath(specialPath)
	specialExtracted := extractProjectIDFromSocketPath(specialSocketPath)
	specialRoundTrip := getSocketPath(specialExtracted)
	if specialRoundTrip != specialSocketPath {
		t.Errorf("special char round-trip failed: extracted %q produces %q, want %q",
			specialExtracted, specialRoundTrip, specialSocketPath)
	}
}

func TestTruncateMiddle(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"hello_world", 11, "hello_world"},
		{"hello_world", 10, "hel…orld"}, // 3 + 3-byte ellipsis + 4 = 10
		{"abcdefghij", 9, "abc…hij"},    // 3 + 3 + 3 = 9 (remaining=6, left=3, right=3)
		{"abcdefghij", 7, "ab…ij"},      // 2 + 3 + 2 = 7
		{"abcdefghij", 5, "a…j"},        // 1 + 3 + 1 = 5
		{"abcdefghij", 4, "…j"},         // 0 + 3 + 1 = 4
		{"abcdefghij", 3, "abc"},        // fallback: just take first 3
		{"ab", 1, "a"},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s_%d", tc.input, tc.maxLen), func(t *testing.T) {
			result := truncateMiddle(tc.input, tc.maxLen)
			if result != tc.expected {
				t.Errorf("truncateMiddle(%q, %d) = %q, want %q", tc.input, tc.maxLen, result, tc.expected)
			}
		})
	}
}

func TestRestartProcessClearsLogsAndShowsStoppingStatus(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-restart-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a script that outputs a unique marker based on an env var
	scriptPath := filepath.Join(tmpDir, "test.sh")
	script := `#!/bin/sh
echo "RUN_MARKER=$RUN_MARKER"
sleep 30
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	testProcesses := []ProcessConfig{
		{
			Name:    "echo-test",
			Command: "sh",
			Args:    []string{scriptPath},
			Env:     []string{"RUN_MARKER=FIRST_RUN"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)

	p := sup.processes[0]

	// Wait for process to start and produce output
	waitForCondition(t, 5*time.Second, func() bool {
		return p.isRunning() && len(p.getOutput()) > 0
	}, "process should be running and have produced output")

	// Verify initial output exists
	initialOutput := p.getOutput()

	foundFirstRun := false
	for _, line := range initialOutput {
		if strings.Contains(line, "FIRST_RUN") {
			foundFirstRun = true
			break
		}
	}
	if !foundFirstRun {
		t.Fatalf("expected 'FIRST_RUN' in logs, got: %v", initialOutput)
	}

	// Drain any pending messages
	for len(outputChan) > 0 {
		<-outputChan
	}

	// Now restart the process
	go sup.RestartProcess(ctx, p, outputChan)

	// Check that stopping status is set
	waitForCondition(t, 2*time.Second, func() bool {
		return p.isStopping()
	}, "process should be in stopping state after restart begins")

	// Check that logs are cleared
	outputAfterClear := p.getOutput()
	for _, line := range outputAfterClear {
		if strings.Contains(line, "FIRST_RUN") {
			t.Errorf("old output should be cleared, but found: %s", line)
		}
	}

	// Wait for restart to complete
	time.Sleep(1 * time.Second)

	// Process should be running again
	if !p.isRunning() {
		t.Error("process should be running after restart")
	}

	// Should not be in stopping state
	if p.isStopping() {
		t.Error("process should not be in stopping state after restart completes")
	}

	// Old logs should still not be present (FIRST_RUN should be gone)
	// New logs should show FIRST_RUN again since env is same
	finalOutput := p.getOutput()
	for _, line := range finalOutput {
		if strings.Contains(line, "[Process exited]") {
			t.Errorf("'[Process exited]' from old process should not appear, but found: %s", line)
		}
	}

	sup.StopAll()
}

func TestRestartProcessUINotification(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-restart-ui-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testProcesses := []ProcessConfig{
		{
			Name:    "test-proc",
			Command: "sh",
			Args:    []string{"-c", "echo 'line1'; sleep 30"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)

	time.Sleep(200 * time.Millisecond)

	p := sup.processes[0]
	if !p.isRunning() {
		t.Fatal("process should be running")
	}

	// Drain initial messages
	for len(outputChan) > 0 {
		<-outputChan
	}

	// Restart and collect UI notifications
	done := make(chan struct{})
	var notifications []processOutputMsg
	go func() {
		defer close(done)
		timeout := time.After(2 * time.Second)
		for {
			select {
			case msg := <-outputChan:
				notifications = append(notifications, msg)
			case <-timeout:
				return
			}
		}
	}()

	sup.RestartProcess(ctx, p, outputChan)

	<-done

	// Should have received at least one notification for the restart
	if len(notifications) == 0 {
		t.Error("should have received UI notifications during restart")
	}

	// Verify notifications are for the correct process
	for _, n := range notifications {
		if n.name != "test-proc" {
			t.Errorf("unexpected notification for process: %s", n.name)
		}
	}

	sup.StopAll()
}

func TestRestartProcessGenerationPreventsOldLogs(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-generation-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use a process that outputs continuously with unique prefix
	testProcesses := []ProcessConfig{
		{
			Name:    "continuous",
			Command: "sh",
			Args:    []string{"-c", "for i in $(seq 1 100); do echo \"old-line-$i\"; sleep 0.05; done"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 1000)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)

	p := sup.processes[0]
	// Wait for some output
	waitForCondition(t, 5*time.Second, func() bool {
		return p.isRunning() && len(p.getOutput()) > 0
	}, "process should be running and have output")

	// Restart while process is still outputting
	sup.RestartProcess(ctx, p, outputChan)

	// Wait for new process to start and produce some output
	waitForCondition(t, 5*time.Second, func() bool {
		output := p.getOutput()
		// We expect new output, but since logs are cleared, just checking for non-empty should be enough
		// assuming clear happened.
		return p.isRunning() && len(output) > 0
	}, "process should be running and have new output after restart")

	finalOutput := p.getOutput()

	// The key check: no "[Process exited]" message should appear
	for _, line := range finalOutput {
		if strings.Contains(line, "[Process exited]") {
			t.Errorf("'[Process exited]' from old process should not appear in logs: %s", line)
		}
	}

	sup.StopAll()
}

func TestRestartProcessRealWorld(t *testing.T) {
	// This test simulates a more realistic scenario with continuous output
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-realworld-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use a script that outputs continuously
	scriptPath := filepath.Join(tmpDir, "test.sh")
	counterPath := filepath.Join(tmpDir, "counter")
	script := fmt.Sprintf(`#!/bin/sh
if [ -f "%s" ]; then
    count=$(cat "%s")
    count=$((count + 1))
    echo $count > "%s"
else
    echo 1 > "%s"
    count=1
fi

echo "=== Starting run $count ==="
i=0
while true; do
    echo "run $count: output line $i"
    i=$((i + 1))
    sleep 0.1
done
`, counterPath, counterPath, counterPath, counterPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	testProcesses := []ProcessConfig{
		{
			Name:    "realworld",
			Command: "sh",
			Args:    []string{scriptPath},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)

	// Wait for some output
	time.Sleep(500 * time.Millisecond)

	p := sup.processes[0]
	if !p.isRunning() {
		t.Fatal("process should be running")
	}

	// Verify we have output from run 1
	initialOutput := p.getOutput()
	t.Logf("Initial output count: %d", len(initialOutput))
	if len(initialOutput) < 3 {
		t.Fatalf("expected at least 3 lines of output, got %d", len(initialOutput))
	}

	foundRun1 := false
	for _, line := range initialOutput {
		if strings.Contains(line, "run 1:") || strings.Contains(line, "Starting run 1") {
			foundRun1 = true
			break
		}
	}
	if !foundRun1 {
		t.Fatalf("expected output from run 1, got: %v", initialOutput)
	}

	// Drain initial messages
	for len(outputChan) > 0 {
		<-outputChan
	}

	// Track state changes
	var stateChanges []string
	stateMu := sync.Mutex{}
	recordState := func(label string) {
		stateMu.Lock()
		defer stateMu.Unlock()
		state := fmt.Sprintf("%s: stopping=%v, running=%v, output_lines=%d",
			label, p.isStopping(), p.isRunning(), len(p.getOutput()))
		stateChanges = append(stateChanges, state)
	}

	recordState("before_restart")

	// Start restart in goroutine
	restartDone := make(chan struct{})
	go func() {
		sup.RestartProcess(ctx, p, outputChan)
		close(restartDone)
	}()

	// Monitor state changes while restart is happening
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-restartDone:
				return
			case <-ticker.C:
				recordState("during_restart")
			}
		}
	}()

	// Wait for first notification
	select {
	case <-outputChan:
		recordState("after_notification")

		// Key assertions at notification time
		if !p.isStopping() {
			t.Errorf("stopping should be true at notification time")
		}
		output := p.getOutput()
		if len(output) > 0 {
			t.Errorf("output should be cleared at notification time, got %d lines: %v", len(output), output)
		}
		// Should NOT have any "run 1" output
		for _, line := range output {
			if strings.Contains(line, "run 1:") || strings.Contains(line, "Starting run 1") {
				t.Errorf("old output from run 1 should be cleared")
				break
			}
		}

	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for notification")
	}

	// Wait for restart to complete
	<-restartDone
	<-monitorDone

	recordState("after_restart")

	// Wait for new output
	time.Sleep(500 * time.Millisecond)

	recordState("final")

	// Log all state changes
	stateMu.Lock()
	t.Log("State changes:")
	for _, s := range stateChanges {
		t.Log("  " + s)
	}
	stateMu.Unlock()

	// Final assertions
	finalOutput := p.getOutput()
	t.Logf("Final output count: %d", len(finalOutput))

	// Should have output from run 2
	foundRun2 := false
	for _, line := range finalOutput {
		if strings.Contains(line, "run 2:") || strings.Contains(line, "Starting run 2") {
			foundRun2 = true
			break
		}
	}
	if !foundRun2 {
		t.Errorf("expected output from run 2, got: %v", finalOutput)
	}

	// Should NOT have output from run 1
	for _, line := range finalOutput {
		if strings.Contains(line, "run 1:") || strings.Contains(line, "Starting run 1") {
			t.Errorf("old output from run 1 should not be present: %v", finalOutput)
			break
		}
	}

	sup.StopAll()
}

func TestRestartProcessSlowStop(t *testing.T) {
	// This test simulates a process that takes time to stop
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-slow-stop-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use a script that ignores SIGTERM for a bit
	scriptPath := filepath.Join(tmpDir, "test.sh")
	counterPath := filepath.Join(tmpDir, "counter")
	script := fmt.Sprintf(`#!/bin/sh
# Trap SIGTERM and delay exit
trap 'echo "received SIGTERM, waiting..."; sleep 2; exit 0' TERM

if [ -f "%s" ]; then
    count=$(cat "%s")
    count=$((count + 1))
    echo $count > "%s"
    echo "run number $count"
else
    echo 1 > "%s"
    echo "run number 1"
fi

# Keep running
while true; do
    sleep 1
done
`, counterPath, counterPath, counterPath, counterPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	testProcesses := []ProcessConfig{
		{
			Name:    "slow-stop",
			Command: "sh",
			Args:    []string{scriptPath},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)

	time.Sleep(200 * time.Millisecond)

	p := sup.processes[0]
	if !p.isRunning() {
		t.Fatal("process should be running")
	}

	// Verify initial output
	initialOutput := p.getOutput()
	t.Logf("Initial output: %v", initialOutput)

	// Drain initial messages
	for len(outputChan) > 0 {
		<-outputChan
	}

	// Start restart
	restartDone := make(chan struct{})
	go func() {
		sup.RestartProcess(ctx, p, outputChan)
		close(restartDone)
	}()

	// Wait for first notification
	select {
	case <-outputChan:
		// Check state immediately
		stopping := p.isStopping()
		running := p.isRunning()
		output := p.getOutput()
		t.Logf("After first notification: stopping=%v, running=%v, output=%v", stopping, running, output)

		if !stopping {
			t.Errorf("stopping should be true")
		}
		if len(output) > 0 {
			t.Errorf("output should be cleared, got: %v", output)
		}

		// The process should still be "stopping" for a while because it delays exit
		time.Sleep(500 * time.Millisecond)
		stillStopping := p.isStopping()
		t.Logf("After 500ms: stopping=%v", stillStopping)

	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for notification")
	}

	// Wait for restart to complete
	select {
	case <-restartDone:
		t.Log("Restart completed")
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for restart")
	}

	// Final state
	time.Sleep(200 * time.Millisecond)
	finalOutput := p.getOutput()
	t.Logf("Final output: %v", finalOutput)

	sup.StopAll()
}

func TestRestartProcessWithBubbletea(t *testing.T) {
	// This test uses the actual bubbletea model to simulate the UI flow
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-bubbletea-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use a script that outputs different things on each run
	scriptPath := filepath.Join(tmpDir, "test.sh")
	counterPath := filepath.Join(tmpDir, "counter")
	script := fmt.Sprintf(`#!/bin/sh
if [ -f "%s" ]; then
    count=$(cat "%s")
    count=$((count + 1))
    echo $count > "%s"
    echo "run number $count"
else
    echo 1 > "%s"
    echo "run number 1"
fi
sleep 30
`, counterPath, counterPath, counterPath, counterPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	testProcesses := []ProcessConfig{
		{
			Name:    "bubbletea-test",
			Command: "sh",
			Args:    []string{scriptPath},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)
	p := sup.processes[0]

	waitForCondition(t, 5*time.Second, func() bool {
		return p.isRunning() && len(p.getOutput()) > 0
	}, "process should be running and have output")

	// Verify initial output
	initialOutput := p.getOutput()
	t.Logf("Initial output: %v", initialOutput)

	// Create the model (simulating what main() does)
	m := model{
		supervisor: sup,
		viewMode:   viewTiled,
		activeTab:  0,
		outputChan: outputChan,
		ctx:        ctx,
	}

	// Initialize viewports
	m.viewports = make([]viewport.Model, len(sup.processes))
	for i := range m.viewports {
		m.viewports[i] = viewport.New(80, 20)
	}
	m.updateViewportContent()

	// Verify initial viewport content
	initialViewportContent := m.viewports[0].View()
	if !strings.Contains(initialViewportContent, "run number 1") {
		t.Fatalf("expected 'run number 1' in viewport, got: %q", initialViewportContent)
	}

	// Drain initial messages
	for len(outputChan) > 0 {
		<-outputChan
	}

	// Simulate pressing 'r' - this is what Update() does
	go sup.RestartProcess(ctx, p, outputChan)

	// Wait for notification
	select {
	case msg := <-outputChan:
		// Simulate what Update() does when it receives processOutputMsg
		t.Logf("Received notification for: %s", msg.name)

		// Check process state
		t.Logf("Process state: stopping=%v, running=%v", p.isStopping(), p.isRunning())

		// Update viewport content (this is what Update() does)
		m.updateViewportContent()

		// Check viewport content
		viewportContent := m.viewports[0].View()
		t.Logf("Viewport content after notification: %q", viewportContent)

		// The viewport should be empty (logs cleared)
		if strings.Contains(viewportContent, "run number 1") {
			t.Errorf("old output 'run number 1' should be cleared from viewport")
		}

		// Check status indicator
		statusIndicator := m.getStatusIndicator(p)
		t.Logf("Status indicator: %s", statusIndicator)
		if !strings.Contains(statusIndicator, "Stopping") {
			t.Errorf("status should show 'Stopping', got: %s", statusIndicator)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for notification")
	}

	// Wait for restart to complete
	time.Sleep(1 * time.Second)

	// Drain remaining messages and update viewport
	for len(outputChan) > 0 {
		<-outputChan
	}
	m.updateViewportContent()

	// Final state
	finalViewportContent := m.viewports[0].View()
	t.Logf("Final viewport content: %q", finalViewportContent)

	if !strings.Contains(finalViewportContent, "run number 2") {
		t.Errorf("expected 'run number 2' in final viewport, got: %q", finalViewportContent)
	}
	if strings.Contains(finalViewportContent, "run number 1") {
		t.Errorf("old output 'run number 1' should not be in final viewport")
	}

	sup.StopAll()
}

func TestRestartProcessUIFlow(t *testing.T) {
	// This test simulates the exact UI flow when user presses 'r'
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-ui-flow-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use a script that outputs different things on each run
	scriptPath := filepath.Join(tmpDir, "test.sh")
	counterPath := filepath.Join(tmpDir, "counter")
	script := fmt.Sprintf(`#!/bin/sh
if [ -f "%s" ]; then
    count=$(cat "%s")
    count=$((count + 1))
    echo $count > "%s"
    echo "run number $count"
else
    echo 1 > "%s"
    echo "run number 1"
fi
sleep 30
`, counterPath, counterPath, counterPath, counterPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	testProcesses := []ProcessConfig{
		{
			Name:    "ui-test",
			Command: "sh",
			Args:    []string{scriptPath},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)

	time.Sleep(2 * time.Second)

	p := sup.processes[0]
	if !p.isRunning() {
		t.Fatal("process should be running")
	}

	// Verify initial state - should have "run number 1"
	initialOutput := p.getOutput()
	foundRun1 := false
	for _, line := range initialOutput {
		if strings.Contains(line, "run number 1") {
			foundRun1 = true
		}
	}
	if !foundRun1 {
		t.Fatalf("expected 'run number 1' in initial output, got: %v", initialOutput)
	}

	// Drain initial messages
	for len(outputChan) > 0 {
		<-outputChan
	}

	// Start RestartProcess in goroutine
	restartDone := make(chan struct{})
	go func() {
		sup.RestartProcess(ctx, p, outputChan)
		close(restartDone)
	}()

	// Check state immediately after starting goroutine
	waitForCondition(t, 2*time.Second, func() bool {
		return p.isStopping()
	}, "process should be stopping")

	immediateState := struct {
		stopping bool
		running  bool
		output   []string
	}{
		stopping: p.isStopping(),
		running:  p.isRunning(),
		output:   p.getOutput(),
	}
	t.Logf("Immediate state: stopping=%v, running=%v, output=%v",
		immediateState.stopping, immediateState.running, immediateState.output)

	// Wait for first notification (should show stopping state with cleared logs)
	select {
	case <-outputChan:
		notificationState := struct {
			stopping bool
			running  bool
			output   []string
		}{
			stopping: p.isStopping(),
			running:  p.isRunning(),
			output:   p.getOutput(),
		}
		t.Logf("After first notification: stopping=%v, running=%v, output=%v",
			notificationState.stopping, notificationState.running, notificationState.output)

		if !notificationState.stopping {
			t.Errorf("stopping should be true after first notification")
		}
		if len(notificationState.output) > 0 {
			t.Errorf("output should be cleared after first notification, got: %v", notificationState.output)
		}
		// Should NOT have "run number 1" anymore
		for _, line := range notificationState.output {
			if strings.Contains(line, "run number 1") {
				t.Errorf("old output 'run number 1' should be cleared")
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for first notification")
	}

	// Wait for restart to complete
	select {
	case <-restartDone:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for restart to complete")
	}

	// Wait for new process to produce output
	time.Sleep(2 * time.Second)

	// Drain remaining messages
	for len(outputChan) > 0 {
		<-outputChan
	}

	// Final state should be running with new output
	if !p.isRunning() {
		t.Error("process should be running after restart")
	}
	if p.isStopping() {
		t.Error("process should not be stopping after restart")
	}

	finalOutput := p.getOutput()
	t.Logf("Final output: %v", finalOutput)

	// Should have "run number 2" (new run)
	foundRun2 := false
	for _, line := range finalOutput {
		if strings.Contains(line, "run number 2") {
			foundRun2 = true
		}
	}
	if !foundRun2 {
		t.Errorf("expected 'run number 2' in final output, got: %v", finalOutput)
	}

	// Should NOT have "run number 1" (old run)
	for _, line := range finalOutput {
		if strings.Contains(line, "run number 1") {
			t.Errorf("old output 'run number 1' should not be present, got: %v", finalOutput)
		}
	}

	sup.StopAll()
}

func TestRestartProcessShowsStoppingForRunningProcess(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-stopping-indicator-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testProcesses := []ProcessConfig{
		{
			Name:    "long-running",
			Command: "sh",
			Args:    []string{"-c", "echo 'started'; sleep 30"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)
	p := sup.processes[0]

	waitForCondition(t, 5*time.Second, func() bool {
		return p.isRunning() && len(p.getOutput()) > 0
	}, "process should be running and have output")

	// Verify we have initial output
	initialOutput := p.getOutput()
	t.Logf("Initial output: %v", initialOutput)

	// Drain messages
	for len(outputChan) > 0 {
		<-outputChan
	}

	// Track state when notification is received
	notificationReceived := make(chan struct{})
	var stateOnNotification struct {
		stopping bool
		running  bool
		output   []string
	}

	go func() {
		<-outputChan
		stateOnNotification.stopping = p.isStopping()
		stateOnNotification.running = p.isRunning()
		stateOnNotification.output = p.getOutput()
		close(notificationReceived)
	}()

	time.Sleep(10 * time.Millisecond)

	// Start restart in background
	go sup.RestartProcess(ctx, p, outputChan)

	select {
	case <-notificationReceived:
		t.Logf("State on notification: stopping=%v, running=%v, output=%v",
			stateOnNotification.stopping, stateOnNotification.running, stateOnNotification.output)
		// Verify stopping indicator is shown
		if !stateOnNotification.stopping {
			t.Errorf("stopping should be true when UI receives notification, got stopping=%v running=%v",
				stateOnNotification.stopping, stateOnNotification.running)
		}
		// Verify logs are cleared
		if len(stateOnNotification.output) > 0 {
			t.Errorf("output should be cleared when UI receives notification, got: %v",
				stateOnNotification.output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for notification")
	}

	// Wait for restart to complete
	time.Sleep(1 * time.Second)

	// Verify final state
	if !p.isRunning() {
		t.Error("process should be running after restart")
	}
	if p.isStopping() {
		t.Error("process should not be stopping after restart completes")
	}

	sup.StopAll()
}

func TestRestartProcessAlreadyExited(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-already-exited-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a script that we can control - first run exits, second run stays
	scriptPath := filepath.Join(tmpDir, "test.sh")
	markerPath := filepath.Join(tmpDir, "marker")
	script := fmt.Sprintf(`#!/bin/sh
if [ -f "%s" ]; then
    echo "second run - staying alive"
    sleep 30
else
    touch "%s"
    echo "first run - exiting"
    exit 0
fi
`, markerPath, markerPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	testProcesses := []ProcessConfig{
		{
			Name:    "controlled-exit",
			Command: "sh",
			Args:    []string{scriptPath},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)

	p := sup.processes[0]

	// Wait for process to exit on its own
	waitForCondition(t, 5*time.Second, func() bool {
		return !p.isRunning()
	}, "process should have exited by now")

	// Verify first run output
	output := p.getOutput()
	foundFirstRun := false
	for _, line := range output {
		if strings.Contains(line, "first run") {
			foundFirstRun = true
		}
	}
	if !foundFirstRun {
		t.Fatalf("expected 'first run' in output, got: %v", output)
	}

	// Drain messages
	for len(outputChan) > 0 {
		<-outputChan
	}

	// Now restart the already-exited process
	sup.RestartProcess(ctx, p, outputChan)

	// Wait for new process to start and produce output
	time.Sleep(300 * time.Millisecond)

	// Process should be running again
	if !p.isRunning() {
		t.Errorf("process should be running after restart, running=%v stopping=%v", p.isRunning(), p.isStopping())
	}

	// Old output should be cleared, new output should be present
	newOutput := p.getOutput()
	for _, line := range newOutput {
		if strings.Contains(line, "first run") {
			t.Errorf("old output 'first run' should be cleared, got: %v", newOutput)
		}
		if strings.Contains(line, "[Process exited]") {
			t.Errorf("'[Process exited]' from old process should not appear, got: %v", newOutput)
		}
	}

	foundSecondRun := false
	for _, line := range newOutput {
		if strings.Contains(line, "second run") {
			foundSecondRun = true
		}
	}
	if !foundSecondRun {
		t.Errorf("expected 'second run' in new output, got: %v", newOutput)
	}

	sup.StopAll()
}

func TestRestartProcessFastRestartRace(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-fast-restart-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use a process that exits immediately when signaled
	testProcesses := []ProcessConfig{
		{
			Name:    "fast-exit",
			Command: "sh",
			Args:    []string{"-c", "echo 'started'; sleep 30"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)

	time.Sleep(200 * time.Millisecond)

	p := sup.processes[0]

	// Drain initial messages
	for len(outputChan) > 0 {
		<-outputChan
	}

	// Simulate what the UI does: call RestartProcess and collect all notifications
	var notifications []struct {
		stopping bool
		running  bool
		output   []string
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		timeout := time.After(3 * time.Second)
		for {
			select {
			case <-outputChan:
				notifications = append(notifications, struct {
					stopping bool
					running  bool
					output   []string
				}{
					stopping: p.isStopping(),
					running:  p.isRunning(),
					output:   p.getOutput(),
				})
			case <-timeout:
				return
			}
		}
	}()

	// Small delay to ensure goroutine is waiting
	time.Sleep(10 * time.Millisecond)

	// Call RestartProcess synchronously (not in goroutine) to ensure we capture all notifications
	sup.RestartProcess(ctx, p, outputChan)

	// Wait for notification collection to complete
	<-done

	// We should have received at least one notification
	if len(notifications) == 0 {
		t.Fatal("should have received at least one notification")
	}

	// The FIRST notification should show stopping=true
	first := notifications[0]
	if !first.stopping {
		t.Errorf("first notification should show stopping=true, got stopping=%v running=%v",
			first.stopping, first.running)
	}

	// First notification should have cleared output
	if len(first.output) > 0 {
		for _, line := range first.output {
			if strings.Contains(line, "[Process exited]") {
				t.Errorf("first notification should not have '[Process exited]': %v", first.output)
			}
		}
	}

	t.Logf("Received %d notifications", len(notifications))
	for i, n := range notifications {
		t.Logf("  %d: stopping=%v running=%v output_len=%d", i, n.stopping, n.running, len(n.output))
	}

	sup.StopAll()
}

func TestRestartProcessStoppingStateOnNotification(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-stopping-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testProcesses := []ProcessConfig{
		{
			Name:    "stopping-test",
			Command: "sh",
			Args:    []string{"-c", "echo 'started'; sleep 30"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)

	time.Sleep(200 * time.Millisecond)

	p := sup.processes[0]
	if !p.isRunning() {
		t.Fatal("process should be running")
	}

	// Drain initial messages
	for len(outputChan) > 0 {
		<-outputChan
	}

	// Track all notifications and their states
	type notificationState struct {
		stopping bool
		running  bool
		output   []string
	}
	notifications := make(chan notificationState, 10)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case <-outputChan:
				notifications <- notificationState{
					stopping: p.isStopping(),
					running:  p.isRunning(),
					output:   p.getOutput(),
				}
			case <-time.After(3 * time.Second):
				return
			}
		}
	}()

	// Small delay to ensure goroutine is waiting on channel
	time.Sleep(10 * time.Millisecond)

	// Call RestartProcess - this will send notifications
	sup.RestartProcess(ctx, p, outputChan)

	// Wait for goroutine to finish collecting notifications
	<-done
	close(notifications)

	// Collect all notifications
	var allNotifications []notificationState
	for n := range notifications {
		allNotifications = append(allNotifications, n)
	}

	// We should have received at least one notification where stopping was true
	// or output was cleared (the first notification during stop phase)
	foundStoppingNotification := false
	for i, n := range allNotifications {
		t.Logf("Notification %d: stopping=%v, running=%v, output=%v", i, n.stopping, n.running, n.output)
		if n.stopping || len(n.output) == 0 {
			foundStoppingNotification = true
		}
	}

	if len(allNotifications) < 2 {
		t.Errorf("expected at least 2 notifications (stop and start), got %d", len(allNotifications))
	}

	if !foundStoppingNotification {
		t.Error("expected at least one notification with stopping=true or cleared output")
	}

	// Final state should be running, not stopping
	if !p.isRunning() {
		t.Error("process should be running after restart")
	}
	if p.isStopping() {
		t.Error("process should not be stopping after restart completes")
	}

	sup.StopAll()
}

func TestRestartProcessNoOldExitMessage(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-exit-msg-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Process that exits quickly when signaled
	testProcesses := []ProcessConfig{
		{
			Name:    "quick-exit",
			Command: "sh",
			Args:    []string{"-c", "echo 'running'; trap 'echo trapped; exit 0' TERM; sleep 30"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)
	p := sup.processes[0]

	waitForCondition(t, 5*time.Second, func() bool {
		return p.isRunning() && len(p.getOutput()) > 0
	}, "process should be running and have output")

	// Verify we have initial output
	initialOutput := p.getOutput()
	foundRunning := false
	for _, line := range initialOutput {
		if strings.Contains(line, "running") {
			foundRunning = true
		}
	}
	if !foundRunning {
		t.Fatalf("expected 'running' in initial output, got: %v", initialOutput)
	}

	// Restart multiple times rapidly to stress test the race condition
	for i := 0; i < 5; i++ {
		sup.RestartProcess(ctx, p, outputChan)

		// Check output immediately after restart completes
		output := p.getOutput()
		for _, line := range output {
			if strings.Contains(line, "[Process exited]") {
				t.Errorf("iteration %d: '[Process exited]' from old process appeared: %v", i, output)
			}
		}

		// Wait a bit for new process to start
		waitForCondition(t, 5*time.Second, func() bool {
			return p.isRunning()
		}, "process should be running")
	}

	sup.StopAll()
}

func TestRestartProcessStateTransitions(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-state-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testProcesses := []ProcessConfig{
		{
			Name:    "state-test",
			Command: "sh",
			Args:    []string{"-c", "echo 'started'; sleep 30"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)

	time.Sleep(200 * time.Millisecond)

	p := sup.processes[0]
	if !p.isRunning() {
		t.Fatal("process should be running initially")
	}
	if p.isStopping() {
		t.Fatal("process should not be stopping initially")
	}

	// Drain initial messages
	for len(outputChan) > 0 {
		<-outputChan
	}

	// Track state changes during restart
	type stateSnapshot struct {
		running  bool
		stopping bool
		output   []string
	}
	var snapshots []stateSnapshot

	// Start restart in background
	restartDone := make(chan struct{})
	go func() {
		sup.RestartProcess(ctx, p, outputChan)
		close(restartDone)
	}()

	// Capture state snapshots during restart
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.After(3 * time.Second)
	for {
		select {
		case <-restartDone:
			// Capture final state
			snapshots = append(snapshots, stateSnapshot{
				running:  p.isRunning(),
				stopping: p.isStopping(),
				output:   p.getOutput(),
			})
			goto done
		case <-ticker.C:
			snapshots = append(snapshots, stateSnapshot{
				running:  p.isRunning(),
				stopping: p.isStopping(),
				output:   p.getOutput(),
			})
		case <-timeout:
			t.Fatal("restart took too long")
		}
	}
done:

	// Verify we saw the stopping state at some point
	sawStopping := false
	for _, s := range snapshots {
		if s.stopping {
			sawStopping = true
			break
		}
	}
	if !sawStopping {
		t.Error("should have observed stopping=true at some point during restart")
	}

	// Verify final state
	finalSnapshot := snapshots[len(snapshots)-1]
	if !finalSnapshot.running {
		t.Error("process should be running after restart")
	}
	if finalSnapshot.stopping {
		t.Error("process should not be stopping after restart completes")
	}

	// Verify no "[Process exited]" in any snapshot
	for i, s := range snapshots {
		for _, line := range s.output {
			if strings.Contains(line, "[Process exited]") {
				t.Errorf("snapshot %d: '[Process exited]' should not appear: %s", i, line)
			}
		}
	}

	sup.StopAll()
}

func TestSearchPerformSearch(t *testing.T) {
	t.Parallel()

	// Create a supervisor with mock processes
	sup := &Supervisor{
		processes: []*Process{
			{
				Config: ProcessConfig{Name: "proc1"},
				Output: []string{
					"line 1: hello world",
					"line 2: foo bar",
					"line 3: hello again",
					"line 4: baz qux",
				},
			},
			{
				Config: ProcessConfig{Name: "proc2"},
				Output: []string{
					"line 1: hello there",
					"line 2: goodbye",
				},
			},
		},
	}

	ctx := context.Background()
	outputChan := make(chan processOutputMsg, 10)
	m := newModel(ctx, sup, outputChan)
	m.width = 100
	m.height = 40
	m.updateViewportSizes()

	// Test searching for "hello"
	m.searchTerm = "hello"
	m.performSearch()

	// Should find 3 matches: 2 in proc1, 1 in proc2
	if len(m.searchMatches) != 3 {
		t.Errorf("expected 3 matches, got %d", len(m.searchMatches))
	}

	// Verify match positions
	expectedMatches := []struct {
		processIdx int
		lineIdx    int
		startPos   int
	}{
		{0, 0, 8}, // "hello" in "line 1: hello world" (0-indexed)
		{0, 2, 8}, // "hello" in "line 3: hello again"
		{1, 0, 8}, // "hello" in "line 1: hello there"
	}

	for i, expected := range expectedMatches {
		if i >= len(m.searchMatches) {
			break
		}
		match := m.searchMatches[i]
		if match.processIdx != expected.processIdx {
			t.Errorf("match %d: expected processIdx %d, got %d", i, expected.processIdx, match.processIdx)
		}
		if match.lineIdx != expected.lineIdx {
			t.Errorf("match %d: expected lineIdx %d, got %d", i, expected.lineIdx, match.lineIdx)
		}
		if match.startPos != expected.startPos {
			t.Errorf("match %d: expected startPos %d, got %d", i, expected.startPos, match.startPos)
		}
	}
}

func TestSearchCaseInsensitive(t *testing.T) {
	t.Parallel()

	sup := &Supervisor{
		processes: []*Process{
			{
				Config: ProcessConfig{Name: "proc1"},
				Output: []string{
					"HELLO world",
					"hello world",
					"HeLLo world",
				},
			},
		},
	}

	ctx := context.Background()
	outputChan := make(chan processOutputMsg, 10)
	m := newModel(ctx, sup, outputChan)

	m.searchTerm = "hello"
	m.performSearch()

	if len(m.searchMatches) != 3 {
		t.Errorf("expected 3 case-insensitive matches, got %d", len(m.searchMatches))
	}
}

func TestSearchNextPrevMatch(t *testing.T) {
	t.Parallel()

	sup := &Supervisor{
		processes: []*Process{
			{
				Config: ProcessConfig{Name: "proc1"},
				Output: []string{
					"target1 here",
					"nothing",
					"target2 here",
				},
			},
		},
	}

	ctx := context.Background()
	outputChan := make(chan processOutputMsg, 10)
	m := newModel(ctx, sup, outputChan)
	m.width = 100
	m.height = 40
	m.updateViewportSizes()

	m.searchTerm = "target"
	m.performSearch()

	if len(m.searchMatches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(m.searchMatches))
	}

	// Initial state
	if m.currentMatch != 0 {
		t.Errorf("expected currentMatch 0, got %d", m.currentMatch)
	}

	// Next match
	m.nextMatch()
	if m.currentMatch != 1 {
		t.Errorf("after nextMatch: expected currentMatch 1, got %d", m.currentMatch)
	}

	// Next match wraps around
	m.nextMatch()
	if m.currentMatch != 0 {
		t.Errorf("after nextMatch wrap: expected currentMatch 0, got %d", m.currentMatch)
	}

	// Prev match wraps around
	m.prevMatch()
	if m.currentMatch != 1 {
		t.Errorf("after prevMatch wrap: expected currentMatch 1, got %d", m.currentMatch)
	}

	// Prev match
	m.prevMatch()
	if m.currentMatch != 0 {
		t.Errorf("after prevMatch: expected currentMatch 0, got %d", m.currentMatch)
	}
}

func TestSearchJumpToMatch(t *testing.T) {
	t.Parallel()

	sup := &Supervisor{
		processes: []*Process{
			{
				Config: ProcessConfig{Name: "proc1"},
				Output: generateLines(100, "proc1"),
			},
			{
				Config: ProcessConfig{Name: "proc2"},
				Output: generateLines(100, "proc2"),
			},
		},
	}

	ctx := context.Background()
	outputChan := make(chan processOutputMsg, 10)
	m := newModel(ctx, sup, outputChan)
	m.width = 100
	m.height = 40
	m.updateViewportSizes()

	// Search for something that appears at specific lines
	m.searchTerm = "line 50"
	m.performSearch()

	if len(m.searchMatches) < 2 {
		t.Fatalf("expected at least 2 matches, got %d", len(m.searchMatches))
	}

	// Jump to first match (should be in proc1)
	m.jumpToMatch(0)
	if m.activeTab != 0 {
		t.Errorf("expected activeTab 0 after jumpToMatch(0), got %d", m.activeTab)
	}

	// Jump to second match (should be in proc2)
	m.jumpToMatch(1)
	if m.activeTab != 1 {
		t.Errorf("expected activeTab 1 after jumpToMatch(1), got %d", m.activeTab)
	}
}

func TestSearchJumpToMatchScrollsViewport(t *testing.T) {
	t.Parallel()

	// Create a process with many lines
	lines := make([]string, 200)
	for i := range lines {
		if i == 150 {
			lines[i] = fmt.Sprintf("line %d: FINDME target", i)
		} else {
			lines[i] = fmt.Sprintf("line %d: regular content", i)
		}
	}

	sup := &Supervisor{
		processes: []*Process{
			{
				Config: ProcessConfig{Name: "proc1"},
				Output: lines,
			},
		},
	}

	ctx := context.Background()
	outputChan := make(chan processOutputMsg, 10)
	m := newModel(ctx, sup, outputChan)
	m.width = 100
	m.height = 40
	m.updateViewportSizes()

	// Enable context mode so line indices map directly
	m.contextMode = true
	m.searchTerm = "FINDME"
	m.performSearch()

	if len(m.searchMatches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(m.searchMatches))
	}

	// Verify the match is at line 150
	if m.searchMatches[0].lineIdx != 150 {
		t.Fatalf("expected match at line 150, got %d", m.searchMatches[0].lineIdx)
	}

	// Jump to the match
	m.jumpToMatch(0)

	// The viewport should have scrolled to show line 150
	// With height 40 and centering, YOffset should be around 150 - 20 = 130
	vp := m.viewports[0]
	yOffset := vp.YOffset

	// The match should be visible in the viewport
	// Viewport shows lines from yOffset to yOffset + height
	matchLine := 150
	if matchLine < yOffset || matchLine >= yOffset+vp.Height {
		t.Errorf("match at line %d not visible in viewport (YOffset=%d, Height=%d)",
			matchLine, yOffset, vp.Height)
	}
}

func TestSearchFilteredLineIndex(t *testing.T) {
	t.Parallel()

	sup := &Supervisor{
		processes: []*Process{
			{
				Config: ProcessConfig{Name: "proc1"},
				Output: []string{
					"nothing line 0",
					"target line 1",
					"nothing line 2",
					"nothing line 3",
					"target line 4",
					"target line 5",
				},
			},
		},
	}

	ctx := context.Background()
	outputChan := make(chan processOutputMsg, 10)
	m := newModel(ctx, sup, outputChan)

	m.searchTerm = "target"
	m.performSearch()

	// Should have 3 matches at lines 1, 4, 5
	if len(m.searchMatches) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(m.searchMatches))
	}

	// In filter mode, these should map to filtered indices 0, 1, 2
	testCases := []struct {
		matchIdx            int
		expectedFilteredIdx int
	}{
		{0, 0}, // match at line 1 -> filtered index 0
		{1, 1}, // match at line 4 -> filtered index 1
		{2, 2}, // match at line 5 -> filtered index 2
	}

	for _, tc := range testCases {
		filteredIdx := m.getFilteredLineIndex(m.searchMatches[tc.matchIdx])
		if filteredIdx != tc.expectedFilteredIdx {
			t.Errorf("match %d: expected filtered index %d, got %d",
				tc.matchIdx, tc.expectedFilteredIdx, filteredIdx)
		}
	}
}

func TestSearchClearSearch(t *testing.T) {
	t.Parallel()

	sup := &Supervisor{
		processes: []*Process{
			{
				Config: ProcessConfig{Name: "proc1"},
				Output: []string{"hello world"},
			},
		},
	}

	ctx := context.Background()
	outputChan := make(chan processOutputMsg, 10)
	m := newModel(ctx, sup, outputChan)

	// Set up a search
	m.searchTerm = "hello"
	m.searchInput.SetValue("hello")
	m.performSearch()
	m.currentMatch = 0

	if len(m.searchMatches) == 0 {
		t.Fatal("expected matches before clear")
	}

	// Clear the search
	m.clearSearch()

	if m.searchTerm != "" {
		t.Errorf("expected empty searchTerm after clear, got %q", m.searchTerm)
	}
	if m.searchInput.Value() != "" {
		t.Errorf("expected empty searchInput after clear, got %q", m.searchInput.Value())
	}
	if len(m.searchMatches) != 0 {
		t.Errorf("expected no matches after clear, got %d", len(m.searchMatches))
	}
	if m.currentMatch != 0 {
		t.Errorf("expected currentMatch 0 after clear, got %d", m.currentMatch)
	}
}

func TestSearchMultipleMatchesOnSameLine(t *testing.T) {
	t.Parallel()

	sup := &Supervisor{
		processes: []*Process{
			{
				Config: ProcessConfig{Name: "proc1"},
				Output: []string{
					"foo foo foo",
					"bar",
					"foo",
				},
			},
		},
	}

	ctx := context.Background()
	outputChan := make(chan processOutputMsg, 10)
	m := newModel(ctx, sup, outputChan)

	m.searchTerm = "foo"
	m.performSearch()

	// Should find 4 matches: 3 on line 0, 1 on line 2
	if len(m.searchMatches) != 4 {
		t.Errorf("expected 4 matches, got %d", len(m.searchMatches))
	}

	// Verify positions on first line
	expectedPositions := []int{0, 4, 8}
	for i, pos := range expectedPositions {
		if i >= len(m.searchMatches) {
			break
		}
		if m.searchMatches[i].startPos != pos {
			t.Errorf("match %d: expected startPos %d, got %d", i, pos, m.searchMatches[i].startPos)
		}
	}
}

func TestSearchReenterSearchModeRetainsValue(t *testing.T) {
	t.Parallel()

	sup := &Supervisor{
		processes: []*Process{
			{
				Config: ProcessConfig{Name: "proc1"},
				Output: []string{"hello world"},
			},
		},
	}

	ctx := context.Background()
	outputChan := make(chan processOutputMsg, 10)
	m := newModel(ctx, sup, outputChan)
	m.width = 100
	m.height = 40
	m.updateViewportSizes()

	// Enter search mode and type a search term
	m.searchMode = true
	m.searchInput.SetValue("hello")
	m.searchTerm = "hello"
	m.performSearch()

	// Exit search mode (like pressing Enter)
	m.searchMode = false
	m.searchInput.Blur()

	// Verify search is still active but not in edit mode
	if m.searchMode {
		t.Error("expected searchMode to be false after exiting")
	}
	if m.searchTerm != "hello" {
		t.Errorf("expected searchTerm 'hello', got %q", m.searchTerm)
	}
	if m.searchInput.Value() != "hello" {
		t.Errorf("expected searchInput value 'hello', got %q", m.searchInput.Value())
	}

	// Re-enter search mode (like pressing "/")
	m.searchMode = true
	m.searchInput.Focus()

	// The search input should still have the previous value for editing
	if m.searchInput.Value() != "hello" {
		t.Errorf("after re-entering search mode, expected searchInput value 'hello', got %q", m.searchInput.Value())
	}
}

func generateLines(count int, prefix string) []string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = fmt.Sprintf("%s line %d: content here", prefix, i)
	}
	return lines
}

func TestEnterRestartsAllDirtyProcesses(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-enter-restart-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use a script that outputs different things on each run
	scriptPath := filepath.Join(tmpDir, "test.sh")
	counterPath := filepath.Join(tmpDir, "counter")
	script := fmt.Sprintf(`#!/bin/sh
if [ -f "%s" ]; then
    count=$(cat "%s")
    count=$((count + 1))
    echo $count > "%s"
    echo "run number $count"
else
    echo 1 > "%s"
    echo "run number 1"
fi
sleep 30
`, counterPath, counterPath, counterPath, counterPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	testProcesses := []ProcessConfig{
		{
			Name:    "enter-restart-test",
			Command: "sh",
			Args:    []string{scriptPath},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	sup.StartAll(ctx, outputChan)
	p := sup.processes[0]

	waitForCondition(t, 5*time.Second, func() bool {
		return p.isRunning() && len(p.getOutput()) > 0
	}, "process should be running and have output")

	// Verify initial output
	initialOutput := p.getOutput()
	t.Logf("Initial output: %v", initialOutput)

	// Create the model
	m := model{
		supervisor: sup,
		viewMode:   viewTiled,
		activeTab:  0,
		outputChan: outputChan,
		ctx:        ctx,
	}

	// Initialize viewports
	m.viewports = make([]viewport.Model, len(sup.processes))
	for i := range m.viewports {
		m.viewports[i] = viewport.New(80, 20)
	}
	m.updateViewportContent()

	// Verify initial viewport content
	initialViewportContent := m.viewports[0].View()
	if !strings.Contains(initialViewportContent, "run number 1") {
		t.Fatalf("expected 'run number 1' in viewport, got: %q", initialViewportContent)
	}

	// Verify process is not dirty initially
	if p.isDirty() {
		t.Fatal("process should not be dirty initially")
	}

	// Verify status indicator does not show "Changes"
	statusBefore := m.getStatusIndicator(p)
	if strings.Contains(statusBefore, "Changes") {
		t.Errorf("status should not show 'Changes' before setDirty, got: %s", statusBefore)
	}

	// Set process as dirty
	p.setDirty(true)

	// Verify status indicator now shows "Changes"
	statusAfterDirty := m.getStatusIndicator(p)
	if !strings.Contains(statusAfterDirty, "Changes") {
		t.Errorf("status should show 'Changes' after setDirty, got: %s", statusAfterDirty)
	}

	// Drain initial messages
	for len(outputChan) > 0 {
		<-outputChan
	}

	// Simulate pressing Enter - this triggers restart of dirty processes
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	m.Update(enterMsg)

	// Wait for notification
	select {
	case msg := <-outputChan:
		t.Logf("Received notification for: %s", msg.name)

		// Check process state - dirty should be cleared by RestartProcess
		if p.isDirty() {
			t.Error("process should not be dirty after restart initiated")
		}

		// Check status indicator - should show "Stopping" now
		statusIndicator := m.getStatusIndicator(p)
		t.Logf("Status indicator after restart: %s", statusIndicator)
		if !strings.Contains(statusIndicator, "Stopping") {
			t.Errorf("status should show 'Stopping', got: %s", statusIndicator)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for notification")
	}

	// Wait for restart to complete
	time.Sleep(1 * time.Second)

	// Drain remaining messages and update viewport
	for len(outputChan) > 0 {
		<-outputChan
	}
	m.updateViewportContent()

	// Final state
	finalViewportContent := m.viewports[0].View()
	t.Logf("Final viewport content: %q", finalViewportContent)

	if !strings.Contains(finalViewportContent, "run number 2") {
		t.Errorf("expected 'run number 2' in final viewport, got: %q", finalViewportContent)
	}
	if strings.Contains(finalViewportContent, "run number 1") {
		t.Errorf("old output 'run number 1' should not be in final viewport")
	}

	sup.StopAll()
}

func TestEnterDoesNothingWhenNoProcessesDirty(t *testing.T) {
	t.Parallel()

	sup := &Supervisor{
		processes: []*Process{
			{
				Config:  ProcessConfig{Name: "proc1"},
				Output:  []string{"line1"},
				running: true,
			},
		},
	}

	ctx := context.Background()
	outputChan := make(chan processOutputMsg, 10)
	m := newModel(ctx, sup, outputChan)
	m.width = 100
	m.height = 40
	m.updateViewportSizes()

	// Verify process is not dirty
	p := sup.processes[0]
	if p.isDirty() {
		t.Fatal("process should not be dirty")
	}

	// Simulate pressing Enter
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	m.Update(enterMsg)

	// No restart should be triggered, output should remain unchanged
	if len(p.getOutput()) != 1 || p.getOutput()[0] != "line1" {
		t.Errorf("output should remain unchanged, got: %v", p.getOutput())
	}

	// Channel should be empty (no notifications sent)
	select {
	case msg := <-outputChan:
		t.Errorf("unexpected message received: %v", msg)
	default:
		// Expected - no message
	}
}

func TestRunPrebuildSuccess(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-prebuild-success-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	markerPath := filepath.Join(tmpDir, "prebuild-marker")

	testProcesses := []ProcessConfig{
		{
			Name:            "prebuild-test",
			Command:         "echo",
			Args:            []string{"main process"},
			PrebuildCommand: "sh",
			PrebuildArgs:    []string{"-c", fmt.Sprintf("sleep 0.2 && echo done > %s", markerPath)},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	p := sup.processes[0]

	// Set dirty so we can see the prebuild status in the indicator
	p.setDirty(true)

	// Create model to check status indicator
	m := model{
		supervisor: sup,
		viewMode:   viewTiled,
		activeTab:  0,
		outputChan: outputChan,
		ctx:        ctx,
	}

	// Verify initial state
	if p.isPrebuildRunning() {
		t.Fatal("prebuild should not be running initially")
	}
	if p.getPrebuildLastErr() != "" {
		t.Fatal("prebuild error should be empty initially")
	}

	// Start prebuild in goroutine
	done := make(chan struct{})
	go func() {
		sup.runPrebuild(ctx, p, outputChan)
		close(done)
	}()

	// Wait for prebuild to start
	waitForCondition(t, 2*time.Second, func() bool {
		return p.isPrebuildRunning()
	}, "prebuild should be running")

	// Check status indicator shows prebuilding
	status := m.getStatusIndicator(p)
	if !strings.Contains(status, "prebuilding") {
		t.Errorf("status should show 'prebuilding', got: %s", status)
	}

	// Should have received a notification when prebuild started
	select {
	case msg := <-outputChan:
		if msg.name != "prebuild-test" {
			t.Errorf("expected notification for 'prebuild-test', got: %s", msg.name)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for start notification")
	}

	// Wait for prebuild to complete
	<-done

	// Verify prebuild completed successfully
	if p.isPrebuildRunning() {
		t.Error("prebuild should not be running after completion")
	}
	if p.getPrebuildLastErr() != "" {
		t.Errorf("prebuild error should be empty after success, got: %s", p.getPrebuildLastErr())
	}

	// Check status indicator no longer shows prebuilding
	statusAfter := m.getStatusIndicator(p)
	if strings.Contains(statusAfter, "prebuilding") {
		t.Errorf("status should not show 'prebuilding' after completion, got: %s", statusAfter)
	}
	if strings.Contains(statusAfter, "prebuild failed") {
		t.Errorf("status should not show 'prebuild failed' after success, got: %s", statusAfter)
	}

	// Verify marker file was created
	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		t.Error("prebuild marker file should exist")
	}

	// Should have received a notification when prebuild completed
	select {
	case msg := <-outputChan:
		if msg.name != "prebuild-test" {
			t.Errorf("expected notification for 'prebuild-test', got: %s", msg.name)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for completion notification")
	}
}

func TestRunPrebuildFailure(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-prebuild-failure-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testProcesses := []ProcessConfig{
		{
			Name:            "prebuild-fail-test",
			Command:         "echo",
			Args:            []string{"main process"},
			PrebuildCommand: "sh",
			PrebuildArgs:    []string{"-c", "exit 1"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	p := sup.processes[0]

	// Set dirty so we can see the prebuild status in the indicator
	p.setDirty(true)

	// Create model to check status indicator
	m := model{
		supervisor: sup,
		viewMode:   viewTiled,
		activeTab:  0,
		outputChan: outputChan,
		ctx:        ctx,
	}

	// Run prebuild (should fail)
	sup.runPrebuild(ctx, p, outputChan)

	// Verify prebuild failed
	if p.isPrebuildRunning() {
		t.Error("prebuild should not be running after completion")
	}
	if p.getPrebuildLastErr() == "" {
		t.Error("prebuild error should be set after failure")
	}

	// Check status indicator shows prebuild failed
	status := m.getStatusIndicator(p)
	if !strings.Contains(status, "prebuild failed") {
		t.Errorf("status should show 'prebuild failed', got: %s", status)
	}

	// Drain notifications
	for len(outputChan) > 0 {
		<-outputChan
	}
}

func TestRunPrebuildNoop(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-prebuild-noop-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testProcesses := []ProcessConfig{
		{
			Name:    "no-prebuild-test",
			Command: "echo",
			Args:    []string{"main process"},
			// No PrebuildCommand configured
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	p := sup.processes[0]

	// Run prebuild (should be a no-op)
	sup.runPrebuild(ctx, p, outputChan)

	// Verify no state changes
	if p.isPrebuildRunning() {
		t.Error("prebuild should not be running")
	}
	if p.getPrebuildLastErr() != "" {
		t.Error("prebuild error should be empty")
	}

	// No notifications should be sent
	select {
	case msg := <-outputChan:
		t.Errorf("unexpected notification: %v", msg)
	default:
		// Expected - no message
	}
}

func TestRunPrebuildClearsErrorOnSuccess(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-prebuild-clear-error-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testProcesses := []ProcessConfig{
		{
			Name:            "prebuild-clear-error-test",
			Command:         "echo",
			Args:            []string{"main process"},
			PrebuildCommand: "true",
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	p := sup.processes[0]

	// Simulate a previous failed prebuild
	p.setPrebuildLastErr("previous error")

	// Run prebuild (should succeed and clear error)
	sup.runPrebuild(ctx, p, outputChan)

	// Verify error was cleared
	if p.getPrebuildLastErr() != "" {
		t.Errorf("prebuild error should be cleared after success, got: %s", p.getPrebuildLastErr())
	}

	// Drain notifications
	for len(outputChan) > 0 {
		<-outputChan
	}
}

func TestFileWatcherTriggersDirtyAndPrebuild(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-watcher-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a subdirectory to watch
	watchDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(watchDir, 0755); err != nil {
		t.Fatalf("failed to create watch dir: %v", err)
	}

	// Create the file we'll modify
	watchedFile := filepath.Join(watchDir, "main.go")
	if err := os.WriteFile(watchedFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("failed to create watched file: %v", err)
	}

	// Marker file that prebuild will create/update
	markerPath := filepath.Join(tmpDir, "prebuild-marker")

	testProcesses := []ProcessConfig{
		{
			Name:            "watcher-test",
			Command:         "echo",
			Args:            []string{"main process"},
			PrebuildCommand: "sh",
			PrebuildArgs:    []string{"-c", fmt.Sprintf("echo ran >> %s", markerPath)},
			WatchGlobs:      []string{"src/**/*.go"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	p := sup.processes[0]

	// Start the watcher
	if err := sup.StartWatcher(ctx, outputChan); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}
	defer sup.StopWatcher()

	// Give the watcher time to initialize
	time.Sleep(100 * time.Millisecond)

	// Verify process is not dirty initially
	if p.isDirty() {
		t.Fatal("process should not be dirty initially")
	}

	// Modify the watched file
	if err := os.WriteFile(watchedFile, []byte("modified"), 0644); err != nil {
		t.Fatalf("failed to modify watched file: %v", err)
	}

	// Wait for process to become dirty
	waitForCondition(t, 2*time.Second, func() bool {
		return p.isDirty()
	}, "process should become dirty after file change")

	// Wait for prebuild to complete (debounce + execution)
	waitForCondition(t, 3*time.Second, func() bool {
		content, err := os.ReadFile(markerPath)
		return err == nil && len(content) > 0
	}, "prebuild marker file should be created")

	// Verify marker file content indicates prebuild ran
	content, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("failed to read marker file: %v", err)
	}
	if !strings.Contains(string(content), "ran") {
		t.Errorf("marker file should contain 'ran', got: %s", string(content))
	}

	// Should have received notification when process became dirty
	foundNotification := false
	for len(outputChan) > 0 {
		msg := <-outputChan
		if msg.name == "watcher-test" {
			foundNotification = true
		}
	}
	if !foundNotification {
		t.Error("should have received notification for dirty process")
	}
}

func TestFileWatcherIgnoresNonMatchingFiles(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-watcher-nomatch-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a subdirectory to watch
	watchDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(watchDir, 0755); err != nil {
		t.Fatalf("failed to create watch dir: %v", err)
	}

	// Create a file that doesn't match the glob
	nonMatchingFile := filepath.Join(watchDir, "readme.txt")
	if err := os.WriteFile(nonMatchingFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("failed to create non-matching file: %v", err)
	}

	testProcesses := []ProcessConfig{
		{
			Name:            "watcher-nomatch-test",
			Command:         "echo",
			Args:            []string{"main process"},
			PrebuildCommand: "true",
			WatchGlobs:      []string{"src/**/*.go"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	p := sup.processes[0]

	// Start the watcher
	if err := sup.StartWatcher(ctx, outputChan); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}
	defer sup.StopWatcher()

	// Give the watcher time to initialize
	time.Sleep(100 * time.Millisecond)

	// Modify the non-matching file
	if err := os.WriteFile(nonMatchingFile, []byte("modified"), 0644); err != nil {
		t.Fatalf("failed to modify non-matching file: %v", err)
	}

	// Wait a bit to ensure no false positive
	time.Sleep(300 * time.Millisecond)

	// Process should still not be dirty
	if p.isDirty() {
		t.Error("process should not be dirty for non-matching file changes")
	}
}

func TestFileWatcherMultipleProcesses(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "supervisor-watcher-multi-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create directories for each process
	backendDir := filepath.Join(tmpDir, "backend")
	frontendDir := filepath.Join(tmpDir, "frontend")
	if err := os.MkdirAll(backendDir, 0755); err != nil {
		t.Fatalf("failed to create backend dir: %v", err)
	}
	if err := os.MkdirAll(frontendDir, 0755); err != nil {
		t.Fatalf("failed to create frontend dir: %v", err)
	}

	// Create files
	backendFile := filepath.Join(backendDir, "main.go")
	frontendFile := filepath.Join(frontendDir, "app.ts")
	if err := os.WriteFile(backendFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("failed to create backend file: %v", err)
	}
	if err := os.WriteFile(frontendFile, []byte("initial"), 0644); err != nil {
		t.Fatalf("failed to create frontend file: %v", err)
	}

	testProcesses := []ProcessConfig{
		{
			Name:       "backend",
			Command:    "echo",
			Args:       []string{"backend"},
			WatchGlobs: []string{"backend/**/*.go"},
		},
		{
			Name:       "frontend",
			Command:    "echo",
			Args:       []string{"frontend"},
			WatchGlobs: []string{"frontend/**/*.ts"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputChan := make(chan processOutputMsg, 100)

	sup := NewSupervisor(testProcesses, false, tmpDir, tmpDir)
	backend := sup.processes[0]
	frontend := sup.processes[1]

	// Start the watcher
	if err := sup.StartWatcher(ctx, outputChan); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}
	defer sup.StopWatcher()

	// Give the watcher time to initialize
	time.Sleep(100 * time.Millisecond)

	// Modify only the backend file
	if err := os.WriteFile(backendFile, []byte("modified"), 0644); err != nil {
		t.Fatalf("failed to modify backend file: %v", err)
	}

	// Wait for backend to become dirty
	waitForCondition(t, 2*time.Second, func() bool {
		return backend.isDirty()
	}, "backend should become dirty after file change")

	// Frontend should not be dirty
	if frontend.isDirty() {
		t.Error("frontend should not be dirty when only backend file changed")
	}

	// Now modify frontend file
	if err := os.WriteFile(frontendFile, []byte("modified"), 0644); err != nil {
		t.Fatalf("failed to modify frontend file: %v", err)
	}

	// Wait for frontend to become dirty
	waitForCondition(t, 2*time.Second, func() bool {
		return frontend.isDirty()
	}, "frontend should become dirty after file change")
}

func TestWatchRootFromGlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern  string
		expected string
	}{
		{"src/**/*.go", "src"},
		{"**/*.go", "."},
		{"frontend/src/**/*.ts", "frontend/src"},
		{"*.go", "."},
		{"cmd/api/*.go", "cmd/api"},
		{"cmd/*/main.go", "cmd"},
		{"a/b/c/d.go", "a/b/c"},
		{"[abc]/*.go", "."},
		{"{a,b}/*.go", "."},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			result := watchRootFromGlob(tt.pattern)
			if result != tt.expected {
				t.Errorf("watchRootFromGlob(%q) = %q, want %q", tt.pattern, result, tt.expected)
			}
		})
	}
}

func TestExcludedDir(t *testing.T) {
	t.Parallel()

	excluded := []string{".git", "node_modules", ".next", "dist", "build", "__pycache__", ".cache"}
	notExcluded := []string{"src", "cmd", "pkg", "internal", "frontend", "backend"}

	for _, name := range excluded {
		if !excludedDir(name) {
			t.Errorf("excludedDir(%q) should return true", name)
		}
	}

	for _, name := range notExcluded {
		if excludedDir(name) {
			t.Errorf("excludedDir(%q) should return false", name)
		}
	}
}
