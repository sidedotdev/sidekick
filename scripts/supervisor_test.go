//go:build supervisor

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	time.Sleep(100 * time.Millisecond)

	if !sup1.processes[0].isRunning() {
		t.Fatal("sup1 process should be running")
	}

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
	time.Sleep(200 * time.Millisecond)

	if sup1.processes[0].isRunning() {
		t.Error("sup1 process should have stopped after takeover")
	}

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
	time.Sleep(100 * time.Millisecond)

	if !sup1.processes[0].isRunning() {
		t.Fatal("sup1 process should be running")
	}

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
	time.Sleep(200 * time.Millisecond)

	if sup1.processes[0].isRunning() {
		t.Error("sup1 process should have stopped")
	}

	// sup1 should NOT have received takeover signal (it's waiting for ephemeral to release)
	select {
	case <-sup1.WaitForTakeover():
		t.Error("sup1 should NOT have received takeover signal for ephemeral takeover")
	case <-time.After(100 * time.Millisecond):
		// Expected
	}

	// sup2 starts its processes
	sup2.StartAll(ctx2, outputChan2)
	time.Sleep(100 * time.Millisecond)

	if !sup2.processes[0].isRunning() {
		t.Error("sup2 process should be running")
	}

	// sup2 releases - sup1 should restart
	sup2.StopAll()
	sup2.ReleaseToPersistent()

	time.Sleep(500 * time.Millisecond)

	if !sup1.processes[0].isRunning() {
		t.Error("sup1 process should have restarted after ephemeral release")
	}

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
	time.Sleep(100 * time.Millisecond)

	// Start first ephemeral
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	outputChan2 := make(chan processOutputMsg, 100)

	sup2 := NewSupervisor(testProcesses, true, tmpDir, tmpDir)
	if err := sup2.ConnectToPersistent(); err != nil {
		t.Fatalf("failed to connect to persistent: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	sup2.StartAll(ctx2, outputChan2)
	time.Sleep(100 * time.Millisecond)

	if !sup2.processes[0].isRunning() {
		t.Error("sup2 process should be running")
	}

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
	time.Sleep(500 * time.Millisecond)

	select {
	case <-sup2.WaitForTakeover():
		// Expected
	case <-time.After(time.Second):
		t.Error("sup2 should have received takeover signal")
	}

	if sup2.processes[0].isRunning() {
		t.Error("sup2 process should have stopped")
	}

	// sup3 starts
	sup3.StartAll(ctx3, outputChan3)
	time.Sleep(100 * time.Millisecond)

	if !sup3.processes[0].isRunning() {
		t.Error("sup3 process should be running")
	}

	// sup3 releases - sup1 should restart (not sup2)
	sup3.StopAll()
	sup3.ReleaseToPersistent()

	time.Sleep(500 * time.Millisecond)

	if !sup1.processes[0].isRunning() {
		t.Error("sup1 process should have restarted")
	}

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
	time.Sleep(100 * time.Millisecond)

	if !sup1.processes[0].isRunning() {
		t.Fatal("sup1 process should be running")
	}

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
	time.Sleep(200 * time.Millisecond)

	if sup1.processes[0].isRunning() {
		t.Error("sup1 process should have stopped after cross-worktree takeover")
	}

	// sup2 starts its processes
	sup2.StartAll(ctx2, outputChan2)
	time.Sleep(100 * time.Millisecond)

	if !sup2.processes[0].isRunning() {
		t.Error("sup2 process should be running")
	}

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

	time.Sleep(500 * time.Millisecond)

	if !sup1.processes[0].isRunning() {
		t.Error("sup1 process should have restarted after ephemeral release")
	}

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
	time.Sleep(100 * time.Millisecond)

	if !sup1.processes[0].isRunning() {
		t.Fatal("sup1 process should be running")
	}

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
		{"hello_world", 10, "hel…orld"},  // 3 + 3-byte ellipsis + 4 = 10
		{"abcdefghij", 9, "abc…hij"},     // 3 + 3 + 3 = 9 (remaining=6, left=3, right=3)
		{"abcdefghij", 7, "ab…ij"},       // 2 + 3 + 2 = 7
		{"abcdefghij", 5, "a…j"},         // 1 + 3 + 1 = 5
		{"abcdefghij", 4, "…j"},          // 0 + 3 + 1 = 4
		{"abcdefghij", 3, "abc"},         // fallback: just take first 3
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