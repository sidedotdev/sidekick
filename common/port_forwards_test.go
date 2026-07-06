package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatPortForwards(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", FormatPortForwards(nil))
	assert.Equal(t, "18855:18855", FormatPortForwards([]PortForwardConfig{{HostPort: 18855}}))
	assert.Equal(t, "18855:28855,8080:8080", FormatPortForwards([]PortForwardConfig{
		{HostPort: 18855, ContainerPort: 28855},
		{HostPort: 8080},
	}))
}

func TestLookupForwardedPort(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		t.Setenv(PortForwardsEnvVar, "")
		_, ok := LookupForwardedPort(18855)
		assert.False(t, ok)
	})

	t.Run("match, default and miss", func(t *testing.T) {
		t.Setenv(PortForwardsEnvVar, "18855:28855,8080:8080")

		port, ok := LookupForwardedPort(18855)
		assert.True(t, ok)
		assert.Equal(t, 28855, port)

		port, ok = LookupForwardedPort(8080)
		assert.True(t, ok)
		assert.Equal(t, 8080, port)

		_, ok = LookupForwardedPort(9999)
		assert.False(t, ok)
	})

	t.Run("malformed entries are ignored", func(t *testing.T) {
		t.Setenv(PortForwardsEnvVar, "garbage,18855:x,18855:28855")
		port, ok := LookupForwardedPort(18855)
		assert.True(t, ok)
		assert.Equal(t, 28855, port)
	})
}
