package env

import (
	"os"
	"testing"

	"sidekick/internal/testfixture"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sidekick-env-test")
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
