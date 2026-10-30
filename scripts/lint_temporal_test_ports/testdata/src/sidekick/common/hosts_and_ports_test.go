package common

import "testing"

// The defining package may exercise the resolvers in its own unit tests.
func TestResolvers(t *testing.T) {
	_ = GetTemporalServerHostPort()
	_ = GetTemporalClientHostPort()
	_ = GetTemporalServerPort()
}