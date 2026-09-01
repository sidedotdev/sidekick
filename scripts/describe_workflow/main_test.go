package main

import (
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/sdk/converter"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustPayload(t *testing.T, value any) *commonpb.Payload {
	t.Helper()
	payload, err := converter.GetDefaultDataConverter().ToPayload(value)
	require.NoError(t, err)
	return payload
}

func TestSearchAttributeLines(t *testing.T) {
	t.Parallel()

	lines := searchAttributeLines(&commonpb.SearchAttributes{
		IndexedFields: map[string]*commonpb.Payload{
			"TemporalChangeVersion": mustPayload(t, []string{"hibernate-worktree-3", "pause-flow-1"}),
			"BuildIds":              mustPayload(t, []string{"unversioned"}),
			"WorkspaceId":           mustPayload(t, "ws_1"),
		},
	})

	assert.Equal(t, []string{
		"BuildIds:",
		"  unversioned",
		"TemporalChangeVersion:",
		"  hibernate-worktree-3",
		"  pause-flow-1",
		"WorkspaceId: ws_1",
	}, lines)
}

func TestSearchAttributeLinesEmpty(t *testing.T) {
	t.Parallel()

	assert.Empty(t, searchAttributeLines(nil))
}

func TestPayloadFieldLinesReportsDecodeError(t *testing.T) {
	t.Parallel()

	lines := payloadFieldLines(converter.GetDefaultDataConverter(), map[string]*commonpb.Payload{
		"broken": {
			Metadata: map[string][]byte{"encoding": []byte("unknown/encoding")},
			Data:     []byte("whatever"),
		},
		"fine": mustPayload(t, "value"),
	})

	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "broken: <decode error:")
	assert.Equal(t, "fine: value", lines[1])
}

func TestMemoLinesUsesGivenConverter(t *testing.T) {
	t.Parallel()

	lines := memoLines(converter.GetDefaultDataConverter(), &commonpb.Memo{
		Fields: map[string]*commonpb.Payload{
			"flowId": mustPayload(t, "flow_1"),
			"count":  mustPayload(t, 3),
		},
	})

	assert.Equal(t, []string{"count: 3", "flowId: flow_1"}, lines)
}

func TestVersioningLines(t *testing.T) {
	t.Parallel()

	lines := versioningLines(&workflowpb.WorkflowExecutionInfo{
		AssignedBuildId: "unversioned:abc123",
		MostRecentWorkerVersionStamp: &commonpb.WorkerVersionStamp{
			BuildId:       "abc123",
			UseVersioning: false,
		},
	})

	assert.Equal(t, []string{
		"AssignedBuildId: unversioned:abc123",
		`MostRecentWorkerVersionStamp: buildId="abc123" useVersioning=false`,
	}, lines)
}

func TestVersioningLinesEmptyWhenUnversioned(t *testing.T) {
	t.Parallel()

	assert.Empty(t, versioningLines(&workflowpb.WorkflowExecutionInfo{}))
}
