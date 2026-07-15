package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterDiagnosticOutput(t *testing.T) {
	t.Parallel()

	output := "unrelated sandbox\nside--target startup\nside--target ssh ready\nanother sandbox\n"

	assert.Equal(t,
		"side--target startup\nside--target ssh ready\n",
		filterDiagnosticOutput(output, "side--target"),
	)
	assert.Equal(t, output, filterDiagnosticOutput(output, ""))
	assert.Empty(t, filterDiagnosticOutput(output, "missing"))
}
