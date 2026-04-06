package env

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"sidekick/common"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create parent dirs for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func TestWalkCodeDirectoryViaEnv_LocalEnv(t *testing.T) {
	t.Parallel()

	dir := setupTestGitRepo(t)

	writeTestFile(t, filepath.Join(dir, "main.go"), "package main")
	writeTestFile(t, filepath.Join(dir, "README.md"), "# readme")
	os.MkdirAll(filepath.Join(dir, "pkg"), 0755)
	writeTestFile(t, filepath.Join(dir, "pkg", "lib.go"), "package pkg")

	ec := EnvContainer{Env: &LocalEnv{WorkingDirectory: dir}}

	var entries []WalkEntry
	err := WalkCodeDirectoryViaEnv(context.Background(), ec, func(path string, isDir bool) error {
		relPath, _ := filepath.Rel(dir, path)
		entries = append(entries, WalkEntry{Path: relPath, IsDir: isDir})
		return nil
	})
	if err != nil {
		t.Fatalf("WalkCodeDirectoryViaEnv() error: %v", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	expectedPaths := []string{"README.md", "main.go", "pkg", "pkg/lib.go"}
	var gotPaths []string
	for _, e := range entries {
		gotPaths = append(gotPaths, e.Path)
	}

	sort.Strings(expectedPaths)
	sort.Strings(gotPaths)

	if len(gotPaths) != len(expectedPaths) {
		t.Fatalf("got %d entries %v, want %d entries %v", len(gotPaths), gotPaths, len(expectedPaths), expectedPaths)
	}
	for i, got := range gotPaths {
		if got != expectedPaths[i] {
			t.Errorf("entry[%d] = %q, want %q", i, got, expectedPaths[i])
		}
	}
}

func TestWalkCodeDirectoryViaEnv_LocalEnvWithIgnore(t *testing.T) {
	t.Parallel()

	dir := setupTestGitRepo(t)

	writeTestFile(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	writeTestFile(t, filepath.Join(dir, "main.go"), "package main")
	writeTestFile(t, filepath.Join(dir, "debug.log"), "log data")

	ec := EnvContainer{Env: &LocalEnv{WorkingDirectory: dir}}

	var paths []string
	err := WalkCodeDirectoryViaEnv(context.Background(), ec, func(path string, isDir bool) error {
		relPath, _ := filepath.Rel(dir, path)
		paths = append(paths, relPath)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkCodeDirectoryViaEnv() error: %v", err)
	}

	sort.Strings(paths)
	expected := []string{".gitignore", "main.go"}
	sort.Strings(expected)

	if len(paths) != len(expected) {
		t.Fatalf("got %v, want %v", paths, expected)
	}
	for i := range paths {
		if paths[i] != expected[i] {
			t.Errorf("path[%d] = %q, want %q", i, paths[i], expected[i])
		}
	}
}

func TestWalkCodeDirectoryViaEnv_UnsupportedEnvType(t *testing.T) {
	t.Parallel()

	ec := EnvContainer{Env: &mockNonSSHEnv{}}
	err := WalkCodeDirectoryViaEnv(context.Background(), ec, func(path string, isDir bool) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for unsupported env type")
	}
}

func TestParseWalkerOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		line    string
		wantOk  bool
		wantDir bool
		wantRel string
	}{
		{"f:main.go", true, false, "main.go"},
		{"d:pkg", true, true, "pkg"},
		{"f:pkg/lib.go", true, false, "pkg/lib.go"},
		{"", false, false, ""},
		{"x", false, false, ""},
		{"invalid line", false, false, ""},
		{"f:", true, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			t.Parallel()
			ok, isDir, rel := parseWalkerLine(tt.line)
			if ok != tt.wantOk {
				t.Errorf("parseWalkerLine(%q) ok = %v, want %v", tt.line, ok, tt.wantOk)
			}
			if ok {
				if isDir != tt.wantDir {
					t.Errorf("parseWalkerLine(%q) isDir = %v, want %v", tt.line, isDir, tt.wantDir)
				}
				if rel != tt.wantRel {
					t.Errorf("parseWalkerLine(%q) rel = %q, want %q", tt.line, rel, tt.wantRel)
				}
			}
		})
	}
}

// mockNonSSHEnv is an env type that does not implement SSHCapableEnv.
type mockNonSSHEnv struct{}

func (m *mockNonSSHEnv) GetType() EnvType            { return "mock" }
func (m *mockNonSSHEnv) GetWorkingDirectory() string { return "/tmp" }
func (m *mockNonSSHEnv) RunCommand(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	return EnvRunCommandOutput{}, nil
}
func (m *mockNonSSHEnv) Walk(ctx context.Context, ignoreFileNames []string, handleEntry func(path string, isDir bool) error) error {
	return fmt.Errorf("walk not supported on mock env")
}

// Verify that WalkCodeDirectoryViaEnv with LocalEnv matches common.WalkDirectory
func TestWalkerProtocolConsistency(t *testing.T) {
	t.Parallel()

	dir := setupTestGitRepo(t)

	writeTestFile(t, filepath.Join(dir, "a.go"), "package a")
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	writeTestFile(t, filepath.Join(dir, "sub", "b.go"), "package sub")

	var directEntries []WalkEntry
	err := common.WalkDirectory(dir, common.SidekickIgnoreFileNames, func(path string, isDir bool) error {
		directEntries = append(directEntries, WalkEntry{Path: path, IsDir: isDir})
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDirectory() error: %v", err)
	}

	var viaEnvEntries []WalkEntry
	ec := EnvContainer{Env: &LocalEnv{WorkingDirectory: dir}}
	err = WalkCodeDirectoryViaEnv(context.Background(), ec, func(path string, isDir bool) error {
		viaEnvEntries = append(viaEnvEntries, WalkEntry{Path: path, IsDir: isDir})
		return nil
	})
	if err != nil {
		t.Fatalf("WalkCodeDirectoryViaEnv() error: %v", err)
	}

	if len(directEntries) != len(viaEnvEntries) {
		t.Fatalf("entry count mismatch: direct=%d, viaEnv=%d", len(directEntries), len(viaEnvEntries))
	}

	for i := range directEntries {
		if directEntries[i].Path != viaEnvEntries[i].Path {
			t.Errorf("entry[%d] path: direct=%q, viaEnv=%q", i, directEntries[i].Path, viaEnvEntries[i].Path)
		}
		if directEntries[i].IsDir != viaEnvEntries[i].IsDir {
			t.Errorf("entry[%d] isDir: direct=%v, viaEnv=%v", i, directEntries[i].IsDir, viaEnvEntries[i].IsDir)
		}
	}
}
