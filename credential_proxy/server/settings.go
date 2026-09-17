package server

import (
	"os"
	"path/filepath"
	"sidekick/credential_proxy"
)

// HostPort is the loopback port sandboxes reach through the reverse forward
// declared in side.yml port_forwards.
const HostPort = 22855

const PathPrefix = credential_proxy.PathPrefix

// ControlSocketPath is the private host socket for capability activation.
// Whether the proxy runs at all is decided by application wiring, not here.
func ControlSocketPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sidekick", "credential-proxy", "control.sock"), nil
}
