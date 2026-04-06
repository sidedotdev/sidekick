package common

import (
	"os/exec"
	"runtime/debug"
	"strings"
	"sync"
)

var (
	buildCommitSha     string
	buildCommitShaOnce sync.Once
)

// GetBuildCommitSha returns the VCS revision (full commit SHA) embedded
// at build time by the Go toolchain. Falls back to `git rev-parse HEAD`
// when build info is unavailable (e.g. during `go test`).
func GetBuildCommitSha() string {
	buildCommitShaOnce.Do(func() {
		info, ok := debug.ReadBuildInfo()
		if ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					buildCommitSha = setting.Value
					break
				}
			}
		}
		if buildCommitSha == "" {
			out, err := exec.Command("git", "rev-parse", "HEAD").Output()
			if err == nil {
				buildCommitSha = strings.TrimSpace(string(out))
			}
		}
	})
	return buildCommitSha
}
