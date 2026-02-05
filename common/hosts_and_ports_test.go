package common

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetServerHost(t *testing.T) {
	t.Run("returns default 127.0.0.1 when SIDE_SERVER_HOST unset", func(t *testing.T) {
		os.Unsetenv("SIDE_SERVER_HOST")
		host := GetServerHost()
		assert.Equal(t, "127.0.0.1", host)
	})

	t.Run("returns SIDE_SERVER_HOST when set", func(t *testing.T) {
		t.Setenv("SIDE_SERVER_HOST", "0.0.0.0")
		host := GetServerHost()
		assert.Equal(t, "0.0.0.0", host)
	})

	t.Run("returns IPv6 loopback when set", func(t *testing.T) {
		t.Setenv("SIDE_SERVER_HOST", "[::1]")
		host := GetServerHost()
		assert.Equal(t, "[::1]", host)
	})

	t.Run("returns custom IP when set", func(t *testing.T) {
		t.Setenv("SIDE_SERVER_HOST", "192.168.1.10")
		host := GetServerHost()
		assert.Equal(t, "192.168.1.10", host)
	})
}

func TestGetServerPort(t *testing.T) {
	t.Run("returns default 8855 when SIDE_SERVER_PORT unset", func(t *testing.T) {
		os.Unsetenv("SIDE_SERVER_PORT")
		port := GetServerPort()
		assert.Equal(t, 8855, port)
	})

	t.Run("returns SIDE_SERVER_PORT when set", func(t *testing.T) {
		t.Setenv("SIDE_SERVER_PORT", "9000")
		port := GetServerPort()
		assert.Equal(t, 9000, port)
	})
}
