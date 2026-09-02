package common

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeArch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"x86_64", "amd64"},
		{"X86_64", "amd64"},
		{"amd64", "amd64"},
		{"aarch64", "arm64"},
		{"arm64", "arm64"},
		{"AARCH64", "arm64"},
		{"riscv64", "riscv64"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeArch(tt.input); got != tt.expected {
				t.Errorf("NormalizeArch(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeOS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"Linux", "linux"},
		{"linux", "linux"},
		{"Darwin", "darwin"},
		{"darwin", "darwin"},
		{"macos", "darwin"},
		{"MacOS", "darwin"},
		{"FreeBSD", "freebsd"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeOS(tt.input); got != tt.expected {
				t.Errorf("NormalizeOS(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGetAgentBinaryPath(t *testing.T) {
	t.Parallel()

	binaryPath, err := GetLocalAgentBinaryPath()
	if err != nil {
		t.Fatalf("GetLocalAgentBinaryPath() error: %v", err)
	}
	if binaryPath == "" {
		t.Fatal("GetLocalAgentBinaryPath() returned an empty path")
	}

	cachedPath, err := GetLocalAgentBinaryPath()
	if err != nil {
		t.Fatalf("second GetLocalAgentBinaryPath() error: %v", err)
	}
	if cachedPath != binaryPath {
		t.Errorf("expected cached path %q, got %q", binaryPath, cachedPath)
	}
}

func TestGetAgentBinaryPathReleaseMode(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", cacheDir)

	agentDir := filepath.Join(cacheDir, "agent-binaries")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}

	fakeVersion := "99.0.0-test"
	fakeHash := "abcdef123456"
	binaryName := fmt.Sprintf("side-agent-linux-amd64-%s", fakeHash)
	fakeBinaryPath := filepath.Join(agentDir, binaryName)
	if err := os.WriteFile(fakeBinaryPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(agentDir, "side-agent-linux-amd64.version"),
		[]byte(fakeVersion+":"+fakeHash),
		0644,
	); err != nil {
		t.Fatal(err)
	}

	originalVersion := agentVersion
	agentVersion = fakeVersion
	t.Cleanup(func() { agentVersion = originalVersion })

	originalFiles := agentSourceFiles
	agentSourceFiles = []string{"nonexistent/file.go"}
	t.Cleanup(func() { agentSourceFiles = originalFiles })

	binaryPath, err := GetAgentBinaryPath("linux", "amd64")
	if err != nil {
		t.Fatalf("GetAgentBinaryPath() error: %v", err)
	}
	if binaryPath != fakeBinaryPath {
		t.Errorf("expected cached release path %q, got %q", fakeBinaryPath, binaryPath)
	}
}

func TestGetAgentBinaryPathEmbeddedHashOverride(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", cacheDir)

	agentDir := filepath.Join(cacheDir, "agent-binaries")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}

	fakeHash := "abcdef123456"
	fakeBinaryPath := filepath.Join(agentDir, "side-agent-linux-amd64-"+fakeHash)
	if err := os.WriteFile(fakeBinaryPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	originalOverride := agentSourceHashOverride
	agentSourceHashOverride = fakeHash
	t.Cleanup(func() { agentSourceHashOverride = originalOverride })

	originalFiles := agentSourceFiles
	agentSourceFiles = []string{"nonexistent/file.go"}
	t.Cleanup(func() { agentSourceFiles = originalFiles })

	binaryPath, err := GetAgentBinaryPath("linux", "amd64")
	if err != nil {
		t.Fatalf("GetAgentBinaryPath() error: %v", err)
	}
	if binaryPath != fakeBinaryPath {
		t.Errorf("expected cached path %q, got %q", fakeBinaryPath, binaryPath)
	}
}

func TestGetAgentBinaryPathEmbeddedHashOverrideMissing(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", cacheDir)

	originalOverride := agentSourceHashOverride
	agentSourceHashOverride = "nonexistenthash"
	t.Cleanup(func() { agentSourceHashOverride = originalOverride })

	originalVersion := agentVersion
	agentVersion = ""
	t.Cleanup(func() { agentVersion = originalVersion })

	originalFiles := agentSourceFiles
	agentSourceFiles = []string{"nonexistent/file.go"}
	t.Cleanup(func() { agentSourceFiles = originalFiles })

	_, err := GetAgentBinaryPath("linux", "amd64")
	if err == nil {
		t.Fatal("expected an error when the embedded-hash binary is missing")
	}
	if !strings.Contains(err.Error(), "pre-built agent binary not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetAgentBinaryPathNoSourceNoVersion(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", cacheDir)

	originalOverride := agentSourceHashOverride
	agentSourceHashOverride = ""
	t.Cleanup(func() { agentSourceHashOverride = originalOverride })

	originalVersion := agentVersion
	agentVersion = ""
	t.Cleanup(func() { agentVersion = originalVersion })

	originalFiles := agentSourceFiles
	agentSourceFiles = []string{"nonexistent/file.go"}
	t.Cleanup(func() { agentSourceFiles = originalFiles })

	_, err := GetAgentBinaryPath("linux", "amd64")
	if err == nil {
		t.Fatal("expected an error when source and release version are unavailable")
	}
}

func TestGetAgentRemoteIdentityLiveSource(t *testing.T) {
	identity, err := GetAgentRemoteIdentity()
	if err != nil {
		t.Fatalf("GetAgentRemoteIdentity: %v", err)
	}
	if len(identity) != 12 {
		t.Errorf("expected 12-char source hash identity, got %q", identity)
	}
}

func TestGetAgentRemoteIdentityFallbackTiers(t *testing.T) {
	originalFiles := agentSourceFiles
	agentSourceFiles = []string{"nonexistent/file.go"}
	t.Cleanup(func() { agentSourceFiles = originalFiles })

	originalOverride := agentSourceHashOverride
	originalVersion := agentVersion
	t.Cleanup(func() {
		agentSourceHashOverride = originalOverride
		agentVersion = originalVersion
	})

	agentSourceHashOverride = "abcdef123456"
	agentVersion = ""
	identity, err := GetAgentRemoteIdentity()
	if err != nil {
		t.Fatalf("override tier: %v", err)
	}
	if identity != "abcdef123456" {
		t.Errorf("override tier: expected embedded hash, got %q", identity)
	}

	agentSourceHashOverride = ""
	agentVersion = "1.2.3"
	identity, err = GetAgentRemoteIdentity()
	if err != nil {
		t.Fatalf("release tier: %v", err)
	}
	if identity != "v1.2.3" {
		t.Errorf("release tier: expected version identity, got %q", identity)
	}

	agentVersion = "1.2.3; rm -rf /"
	if _, err := GetAgentRemoteIdentity(); err == nil {
		t.Error("expected error for shell-unsafe version identity")
	}

	agentVersion = ""
	if _, err := GetAgentRemoteIdentity(); err == nil {
		t.Error("expected error when no source, override, or version is available")
	}
}
