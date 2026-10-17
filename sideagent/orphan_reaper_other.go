//go:build !(linux && (amd64 || arm64))

package sideagent

// EnableOrphanReaping is a no-op where child subreapers are unavailable or
// the agent is not deployed.
func EnableOrphanReaping() error {
	return nil
}
