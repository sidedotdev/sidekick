package fflag

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	ffclient "github.com/thomaspoignant/go-feature-flag"
	"github.com/thomaspoignant/go-feature-flag/retriever/fileretriever"
)

func TestEvaluateFlags(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "flags.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
notifications:
  variations:
    enabled: true
    disabled: false
  targeting:
    - query: tier eq "enterprise"
      variation: enabled
  defaultRule:
    variation: disabled
offset:
  variations:
    value: -7
  defaultRule:
    variation: value
invalid:
  variations:
    value: "wrong type"
  defaultRule:
    variation: value
`), 0600))
	client, err := ffclient.New(ffclient.Config{
		Retriever: &fileretriever.Retriever{Path: path},
	})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	activities := &FFlagActivities{FFlag: FFlag{Client: client}}

	for _, tier := range []string{"enterprise", "personal"} {
		t.Run(tier, func(t *testing.T) {
			t.Parallel()
			input := EvaluateFlagsInput{
				TargetingKey: "customer-123",
				Attributes:   map[string]interface{}{"tier": tier},
				BoolFlags: map[string]bool{
					"notifications": false,
					"missing":       true,
					"invalid":       true,
				},
				IntFlags: map[string]int{
					"offset":  42,
					"missing": 1000,
					"invalid": 20,
				},
			}
			data, err := json.Marshal(input)
			require.NoError(t, err)
			var decoded EvaluateFlagsInput
			require.NoError(t, json.Unmarshal(data, &decoded))

			output, err := activities.EvaluateFlags(context.Background(), decoded)
			require.NoError(t, err)
			require.Equal(t, EvaluateFlagsOutput{
				BoolValues: map[string]bool{
					"notifications": tier == "enterprise",
					"missing":       true,
					"invalid":       true,
				},
				IntValues: map[string]int{
					"offset":  -7,
					"missing": 1000,
					"invalid": 20,
				},
			}, output)

			data, err = json.Marshal(output)
			require.NoError(t, err)
			var restored EvaluateFlagsOutput
			require.NoError(t, json.Unmarshal(data, &restored))
			require.Equal(t, output, restored)
		})
	}
}

func TestEvaluateFlagsUnavailableClient(t *testing.T) {
	t.Parallel()
	for _, activities := range []*FFlagActivities{nil, {}} {
		input := EvaluateFlagsInput{
			TargetingKey: "customer",
			BoolFlags:    map[string]bool{"enabled": true, "optional": false},
			IntFlags:     map[string]int{"limit": 1000, "offset": -1, "disabled": 0},
		}
		output, err := activities.EvaluateFlags(context.Background(), input)
		require.NoError(t, err)
		require.Equal(t, input.BoolFlags, output.BoolValues)
		require.Equal(t, input.IntFlags, output.IntValues)
	}
}

func TestEvaluateFlagsStringArrays(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "flags.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
reports:
  variations:
    selected: [EditBlockReport, TestResult, AutoReviewFeedback, Summary]
  defaultRule:
    variation: selected
invalid:
  variations:
    selected: [TestResult, 42]
  defaultRule:
    variation: selected
empty:
  variations:
    selected: []
  defaultRule:
    variation: selected
`), 0600))
	client, err := ffclient.New(ffclient.Config{
		Retriever: &fileretriever.Retriever{Path: path},
	})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	input := EvaluateFlagsInput{StringArrayFlags: map[string][]string{
		"reports": {"Summary"},
		"invalid": {"TestResult"},
		"missing": {"EditBlockReport"},
		"empty":   {"Summary"},
	}}
	activities := &FFlagActivities{FFlag: FFlag{Client: client}}
	output, err := activities.EvaluateFlags(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, []string{"EditBlockReport", "TestResult", "AutoReviewFeedback", "Summary"}, output.StringArrayValues["reports"])
	require.Equal(t, input.StringArrayFlags["invalid"], output.StringArrayValues["invalid"])
	require.Equal(t, input.StringArrayFlags["missing"], output.StringArrayValues["missing"])
	require.Empty(t, output.StringArrayValues["empty"])

	output, err = (*FFlagActivities)(nil).EvaluateFlags(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, input.StringArrayFlags, output.StringArrayValues)
}

func TestEvaluateFlagsAssignmentJournal(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	path := filepath.Join(t.TempDir(), "flags.yml")
	require.NoError(t, os.WriteFile(path, []byte(`
experiment:
  version: "experiment-v2"
  variations:
    on: true
    off: false
  defaultRule:
    percentage:
      on: 50
      off: 50
targeted:
  variations:
    small: 10
    large: 20
  targeting:
    - query: tier eq "enterprise"
      percentage:
        small: 50
        large: 50
  defaultRule:
    variation: small
fixed:
  variations:
    on: true
  defaultRule:
    variation: on
`), 0600))
	client, err := ffclient.New(ffclient.Config{
		Retriever: &fileretriever.Retriever{Path: path},
	})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	activities := &FFlagActivities{FFlag: FFlag{Client: client}}
	input := EvaluateFlagsInput{
		TargetingKey: "flow-123",
		Attributes:   map[string]interface{}{"tier": "enterprise"},
		BoolFlags:    map[string]bool{"experiment": false, "fixed": false, "missing": false},
		IntFlags:     map[string]int{"targeted": 0},
	}
	output, err := activities.EvaluateFlags(context.Background(), input)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(state, "sidekick", "flag-assignments.jsonl"))
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(data))
	records := map[string]map[string]interface{}{}
	for decoder.More() {
		var record map[string]interface{}
		require.NoError(t, decoder.Decode(&record))
		require.Equal(t, "flow-123", record["targetingKey"])
		require.NotEmpty(t, record["evaluatedAt"])
		require.NotEmpty(t, record["variant"])
		records[record["flag"].(string)] = record
	}
	require.Len(t, records, 2)
	require.Equal(t, output.BoolValues["experiment"], records["experiment"]["value"])
	require.Equal(t, "experiment-v2", records["experiment"]["version"])
	require.Equal(t, float64(output.IntValues["targeted"]), records["targeted"]["value"])

	t.Setenv("XDG_STATE_HOME", path)
	again, err := activities.EvaluateFlags(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, output, again)
}
