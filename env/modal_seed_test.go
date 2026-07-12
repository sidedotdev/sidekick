package env

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModalSeedRegistry(t *testing.T) {
	t.Setenv("SIDE_DATA_HOME", t.TempDir())

	assert.Empty(t, modalSeedCandidates("/some/repo"))
	assert.Empty(t, modalSeedCandidates(""))

	modalRegisterSeedSandbox("/some/repo", "sb-1")
	modalRegisterSeedSandbox("/some/repo", "sb-2")
	modalRegisterSeedSandbox("/other/repo", "sb-other")
	assert.Equal(t, []string{"sb-2", "sb-1"}, modalSeedCandidates("/some/repo"))
	assert.Equal(t, []string{"sb-other"}, modalSeedCandidates("/other/repo"))

	// Re-registering moves a name to the front without duplicating it.
	modalRegisterSeedSandbox("/some/repo", "sb-1")
	assert.Equal(t, []string{"sb-1", "sb-2"}, modalSeedCandidates("/some/repo"))

	for i := 0; i < modalSeedRegistryCap+5; i++ {
		modalRegisterSeedSandbox("/some/repo", fmt.Sprintf("sb-cap-%d", i))
	}
	names := modalSeedCandidates("/some/repo")
	assert.Len(t, names, modalSeedRegistryCap)
	assert.Equal(t, fmt.Sprintf("sb-cap-%d", modalSeedRegistryCap+4), names[0])
}