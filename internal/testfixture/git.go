package testfixture

import (
	"fmt"
	"os"
	"os/exec"
)

// Git snapshots a repository built by a shell script with isolated Git config.
// Linked worktrees must be added after copying: their metadata uses absolute paths.
func Git(script string) *Snapshot {
	return New(func(dir string) error {
		cmd := exec.Command("sh", "-eu", "-c", script)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_CONFIG_SYSTEM="+os.DevNull,
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_COUNT=0",
			"GIT_AUTHOR_NAME=Test User",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test User",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("build git fixture: %w\n%s", err, output)
		}
		return nil
	})
}
