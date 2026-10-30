package callers_test

import (
	"testing"

	"sidekick/common"
)

func TestResolvesHostServer(t *testing.T) {
	_ = common.GetTemporalServerHostPort() // want "GetTemporalServerHostPort resolves the host Temporal server"
	_ = common.GetTemporalClientHostPort() // want "GetTemporalClientHostPort resolves the host Temporal server"
	_ = common.GetTemporalServerPort()     // want "GetTemporalServerPort resolves the host Temporal server"
	resolve := common.GetTemporalServerHostPort // want "GetTemporalServerHostPort resolves the host Temporal server"
	_ = resolve
}

func TestReadOnlyProxyIsAllowed(t *testing.T) {
	_ = common.GetTemporalDiagnosticHostPort()
}