package sideagent

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// staleBinaryMaxAge spares recently installed binaries and in-flight install
// temps from startup GC.
const staleBinaryMaxAge = 24 * time.Hour

// CleanupStaleSiblings best-effort removes stale side-agent binaries (and
// abandoned install temps) that share the running executable's directory.
// It backs the `side-agent gc` mode, which the install chain invokes on the
// freshly installed binary, so cleanup runs opportunistically only when an
// install occurs — and in Go, without relying on find/glob semantics of any
// remote toolchain. Failures are deliberately ignored: inability to remove
// stale files must never affect an install or serving. The running
// executable itself is never removed.
func CleanupStaleSiblings() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cleanupStaleAgentBinaries(filepath.Dir(exe), filepath.Base(exe), staleBinaryMaxAge)
}

func cleanupStaleAgentBinaries(dir, keepName string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == keepName || !strings.HasPrefix(name, "side-agent-") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}
