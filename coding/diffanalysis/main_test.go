package diffanalysis

import (
	"os"
	"testing"

	"sidekick/internal/testfixture"
)

// The Git oracle behind the fuzz seeds spawns thousands of git processes, so
// macOS's developer-tools launcher would dominate the package's run time.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sidekick-diffanalysis-test")
	if err != nil {
		panic(err)
	}
	gitPath, err := testfixture.DirectGitPath(dir)
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("PATH", gitPath); err != nil {
		panic(err)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
