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
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeOS(tt.input); got != tt.expected {
				t.Errorf("NormalizeOS(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGetWalkerBinaryPath(t *testing.T) {
	t.Parallel()

	path, err := GetLocalWalkerBinaryPath()
	if err != nil {
		t.Fatalf("GetLocalWalkerBinaryPath() error: %v", err)
	}
	if path == "" {
		t.Fatal("GetLocalWalkerBinaryPath() returned empty path")
	}

	// Second call should return cached binary
	path2, err := GetLocalWalkerBinaryPath()
	if err != nil {
		t.Fatalf("second GetLocalWalkerBinaryPath() error: %v", err)
	}
	if path != path2 {
		t.Errorf("expected same cached path, got %q and %q", path, path2)
	}
}

func TestGetWalkerBinaryPath_ReleaseMode(t *testing.T) {
	// Cannot be parallel: mutates package-level vars and uses t.Setenv.

	// When walkerVersion is set and source hash fails, it should attempt
	// release download. We test the naming/caching logic by checking that a
	// cached binary with the version name is found without downloading.
	cacheDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", cacheDir)

	walkerDir := filepath.Join(cacheDir, "walker-binaries")
	if err := os.MkdirAll(walkerDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Pre-place a fake binary to simulate a previous download
	fakeVersion := "99.0.0-test"
	binaryName := fmt.Sprintf("side-walker-linux-amd64-%s", fakeVersion)
	fakeBinaryPath := filepath.Join(walkerDir, binaryName)
	if err := os.WriteFile(fakeBinaryPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	origVersion := walkerVersion
	walkerVersion = fakeVersion
	t.Cleanup(func() { walkerVersion = origVersion })

	// Override source hash to simulate release mode (no source available).
	// We do this by temporarily setting walkerSourceFiles to a non-existent file.
	origFiles := walkerSourceFiles
	walkerSourceFiles = []string{"nonexistent/file.go"}
	t.Cleanup(func() { walkerSourceFiles = origFiles })

	path, err := GetWalkerBinaryPath("linux", "amd64")
	if err != nil {
		t.Fatalf("GetWalkerBinaryPath() error: %v", err)
	}
	if path != fakeBinaryPath {
		t.Errorf("expected cached release path %q, got %q", fakeBinaryPath, path)
	}
}

func TestGetWalkerBinaryPath_EmbeddedHashOverride(t *testing.T) {
	// Cannot be parallel: mutates package-level vars and uses t.Setenv.

	cacheDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", cacheDir)

	walkerDir := filepath.Join(cacheDir, "walker-binaries")
	if err := os.MkdirAll(walkerDir, 0755); err != nil {
		t.Fatal(err)
	}

	fakeHash := "abcdef123456"
	binaryName := fmt.Sprintf("side-walker-linux-amd64-%s", fakeHash)
	fakeBinaryPath := filepath.Join(walkerDir, binaryName)
	if err := os.WriteFile(fakeBinaryPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	origOverride := walkerSourceHashOverride
	walkerSourceHashOverride = fakeHash
	t.Cleanup(func() { walkerSourceHashOverride = origOverride })

	origFiles := walkerSourceFiles
	walkerSourceFiles = []string{"nonexistent/file.go"}
	t.Cleanup(func() { walkerSourceFiles = origFiles })

	path, err := GetWalkerBinaryPath("linux", "amd64")
	if err != nil {
		t.Fatalf("GetWalkerBinaryPath() error: %v", err)
	}
	if path != fakeBinaryPath {
		t.Errorf("expected cached path %q, got %q", fakeBinaryPath, path)
	}
}

func TestGetWalkerBinaryPath_EmbeddedHashOverrideMissing(t *testing.T) {
	// Cannot be parallel: mutates package-level vars and uses t.Setenv.

	cacheDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", cacheDir)

	origOverride := walkerSourceHashOverride
	walkerSourceHashOverride = "nonexistenthash"
	t.Cleanup(func() { walkerSourceHashOverride = origOverride })

	origVersion := walkerVersion
	walkerVersion = ""
	t.Cleanup(func() { walkerVersion = origVersion })

	origFiles := walkerSourceFiles
	walkerSourceFiles = []string{"nonexistent/file.go"}
	t.Cleanup(func() { walkerSourceFiles = origFiles })

	_, err := GetWalkerBinaryPath("linux", "amd64")
	if err == nil {
		t.Fatal("expected error when hash override binary is missing, got nil")
	}
	if !strings.Contains(err.Error(), "pre-built walker binary not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetWalkerBinaryPath_NoSourceNoVersion(t *testing.T) {
	// Cannot be parallel: mutates package-level vars and uses t.Setenv.

	cacheDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", cacheDir)

	origOverride := walkerSourceHashOverride
	walkerSourceHashOverride = ""
	t.Cleanup(func() { walkerSourceHashOverride = origOverride })

	origVersion := walkerVersion
	walkerVersion = ""
	t.Cleanup(func() { walkerVersion = origVersion })

	origFiles := walkerSourceFiles
	walkerSourceFiles = []string{"nonexistent/file.go"}
	t.Cleanup(func() { walkerSourceFiles = origFiles })

	_, err := GetWalkerBinaryPath("linux", "amd64")
	if err == nil {
		t.Fatal("expected error when no source and no version, got nil")
	}
}
