package dev

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"sidekick/fflag"

	"github.com/stretchr/testify/require"
	ffclient "github.com/thomaspoignant/go-feature-flag"
	"github.com/thomaspoignant/go-feature-flag/retriever/fileretriever"
)

func verifierTestActivities(t *testing.T, config string) *fflag.FFlagActivities {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flags.yml")
	require.NoError(t, os.WriteFile(path, []byte(config), 0600))
	client, err := ffclient.New(ffclient.Config{
		Retriever: &fileretriever.Retriever{Path: path},
	})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return &fflag.FFlagActivities{FFlag: fflag.FFlag{Client: client}}
}

func TestVerifierSettingsDefaults(t *testing.T) {
	t.Parallel()
	config, err := os.ReadFile("../flags.yml")
	require.NoError(t, err)
	activities := verifierTestActivities(t, string(config))
	assignments := map[bool]int{}
	for i := 0; i < 200; i++ {
		input := verifierFlagsInput(fmt.Sprintf("flow-%d", i), "coding", "judge")
		first, err := activities.EvaluateFlags(context.Background(), input)
		require.NoError(t, err)
		require.True(t, verifierSettingsFromFlags(first).Enabled)
		require.Equal(t, 1000, verifierSettingsFromFlags(first).ToolResultMaxChars)
		require.Equal(t, 20, verifierSettingsFromFlags(first).RecentToolResultsCount)
		require.Equal(t, 40000, verifierSettingsFromFlags(first).ChatHistoryMaxSize)
		second, err := activities.EvaluateFlags(context.Background(), input)
		require.NoError(t, err)
		require.Equal(t, first, second)
		assignments[verifierSettingsFromFlags(first).ReuseHistory]++
	}
	require.Greater(t, assignments[true], 50)
	require.Greater(t, assignments[false], 50)
}

func TestVerifierSettingsModelTargeting(t *testing.T) {
	t.Parallel()
	activities := verifierTestActivities(t, `
review_tool_result_max_chars:
  variations:
    normal: 1000
    targeted: 0
  targeting:
    - query: modelId eq "target-judge"
      variation: targeted
  defaultRule:
    variation: normal
review_chat_history_enabled:
  variations:
    off: false
  defaultRule:
    variation: off
`)
	for _, tc := range []struct {
		model string
		want  int
	}{
		{"other-judge", 1000},
		{"target-judge", 0},
	} {
		t.Run(tc.model, func(t *testing.T) {
			output, err := activities.EvaluateFlags(context.Background(), verifierFlagsInput("same-flow", "coding", tc.model))
			require.NoError(t, err)
			require.False(t, verifierSettingsFromFlags(output).Enabled)
			require.Equal(t, tc.want, verifierSettingsFromFlags(output).ToolResultMaxChars)
			require.False(t, verifierSettingsFromFlags(output).ReuseHistory)
			require.Equal(t, 20, verifierSettingsFromFlags(output).RecentToolResultsCount)
			require.Equal(t, 40000, verifierSettingsFromFlags(output).ChatHistoryMaxSize)
		})
	}
}

func TestVerifierSettingsFailureFallbacks(t *testing.T) {
	t.Parallel()
	for _, config := range []string{
		"unrelated:\n  variations:\n    on: true\n  defaultRule:\n    variation: on\n",
		`
review_chat_history_enabled:
  variations:
    bad: "not a boolean"
  defaultRule:
    variation: bad
persistent_reviewer_history:
  variations:
    bad: "not a boolean"
  defaultRule:
    variation: bad
review_tool_result_max_chars:
  variations:
    bad: -1
  defaultRule:
    variation: bad
review_recent_tool_results_count:
  variations:
    bad: -1
  defaultRule:
    variation: bad
review_chat_history_max_size:
  variations:
    bad: -1
  defaultRule:
    variation: bad
`,
	} {
		activities := verifierTestActivities(t, config)
		output, err := activities.EvaluateFlags(context.Background(), verifierFlagsInput("flow", "", "judge"))
		require.NoError(t, err)
		require.Equal(t, DefaultVerifierSettings(), verifierSettingsFromFlags(output))
	}
}
