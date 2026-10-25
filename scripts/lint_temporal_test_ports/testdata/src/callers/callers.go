package callers

import "sidekick/common"

// Production code legitimately connects to the host server.
func ProductionHostPort() string {
	return common.GetTemporalClientHostPort()
}