package dev

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInterdiffChangedLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		before      string
		after       string
		wantChanged int
		wantTotal   int
	}{
		{
			name:        "identical diffs",
			before:      "--- a/foo\n+++ b/foo\n@@ -1 +1 @@\n-old\n+new\n",
			after:       "--- a/foo\n+++ b/foo\n@@ -1 +1 @@\n-old\n+new\n",
			wantChanged: 0,
			wantTotal:   2,
		},
		{
			name:        "whitespace-only difference ignored",
			before:      "+foo bar\n",
			after:       "+foo    bar\n",
			wantChanged: 0,
			wantTotal:   1,
		},
		{
			name:        "extra added line after resolution",
			before:      "+foo\n",
			after:       "+foo\n+baz\n",
			wantChanged: 1,
			wantTotal:   2,
		},
		{
			name:        "file headers are not counted",
			before:      "--- a/foo\n+++ b/foo\n",
			after:       "--- a/foo\n+++ b/foo\n+added\n",
			wantChanged: 1,
			wantTotal:   1,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			changed, total := interdiffChangedLines(tt.before, tt.after)
			assert.Equal(t, tt.wantChanged, changed, "changed")
			assert.Equal(t, tt.wantTotal, total, "total")
		})
	}
}

func TestAssessResolutionSubstantialityActivity(t *testing.T) {
	// Not parallel: mutates the process environment via t.Setenv.
	t.Setenv(conflictRereviewPercentEnvVar, "10")

	substantial, err := AssessResolutionSubstantialityActivity(context.Background(), AssessResolutionSubstantialityInput{
		BeforeDiff: "+a\n+b\n+c\n",
		AfterDiff:  "+a\n+b\n+c\n+d\n",
	})
	require.NoError(t, err)
	assert.Equal(t, 10.0, substantial.ThresholdPercent)
	assert.InDelta(t, 25.0, substantial.PercentChanged, 0.001)
	assert.True(t, substantial.Substantial)

	unchanged, err := AssessResolutionSubstantialityActivity(context.Background(), AssessResolutionSubstantialityInput{
		BeforeDiff: "+a\n+b\n",
		AfterDiff:  "+a\n+b\n",
	})
	require.NoError(t, err)
	assert.False(t, unchanged.Substantial)
	assert.Equal(t, 0.0, unchanged.PercentChanged)
}

func TestConflictRereviewPercentDefault(t *testing.T) {
	t.Setenv(conflictRereviewPercentEnvVar, "")
	assert.Equal(t, defaultConflictRereviewPercent, conflictRereviewPercent())

	t.Setenv(conflictRereviewPercentEnvVar, "not-a-number")
	assert.Equal(t, defaultConflictRereviewPercent, conflictRereviewPercent())

	t.Setenv(conflictRereviewPercentEnvVar, "2.5")
	assert.Equal(t, 2.5, conflictRereviewPercent())
}
