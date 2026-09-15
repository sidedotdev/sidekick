package fflag

import (
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
