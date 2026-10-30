package common

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentSourceFilesCoverAgentBuild: a source file compiled into the
// side-agent but missing from agentSourceFiles leaves the hash unchanged when
// it is edited, so a stale cached agent keeps being reused and uploaded.
func TestAgentSourceFilesCoverAgentBuild(t *testing.T) {
	t.Parallel()
	root, err := findModuleRoot()
	require.NoError(t, err)

	listed := make(map[string]bool, len(agentSourceFiles))
	for _, path := range agentSourceFiles {
		listed[path] = true
	}
	for _, dir := range []string{"cmd/side-agent", "sideagent"} {
		sources, err := filepath.Glob(filepath.Join(root, dir, "*.go"))
		require.NoError(t, err)
		require.NotEmpty(t, sources, "no Go sources found in %s", dir)
		for _, source := range sources {
			if strings.HasSuffix(source, "_test.go") {
				continue
			}
			rel, err := filepath.Rel(root, source)
			require.NoError(t, err)
			covered := listed[filepath.ToSlash(rel)]
			for parent := filepath.Dir(rel); parent != "."; parent = filepath.Dir(parent) {
				if listed[filepath.ToSlash(parent)] {
					covered = true
					break
				}
			}
			assert.True(t, covered, "%s is part of the side-agent build but missing from agentSourceFiles", rel)
		}
	}
}
