package persisted_ai

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepairToolCallArgumentsActivity(t *testing.T) {
	t.Parallel()

	input := RepairToolCallArgsInput{
		Arguments: []string{
			`{"analysis": "Blah blah.... </analysis> <parameter name=\"steps\">[{\"key\": \"value\"}]", "other_fields": ["whatever"]}`,
			`{"already": "clean"}`,
		},
	}

	output, err := RepairToolCallArgumentsActivity(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, output.Arguments, len(input.Arguments))

	expected := []string{
		`{"analysis": "Blah blah.... ", "steps": [{"key": "value"}], "other_fields": ["whatever"]}`,
		`{"already": "clean"}`,
	}
	for i, exp := range expected {
		var wantJSON, gotJSON interface{}
		require.NoError(t, json.Unmarshal([]byte(exp), &wantJSON))
		require.NoError(t, json.Unmarshal([]byte(output.Arguments[i]), &gotJSON))
		assert.Equal(t, wantJSON, gotJSON)
	}
}
