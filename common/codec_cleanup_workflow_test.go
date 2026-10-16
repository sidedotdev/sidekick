package common

import (
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	historypb "go.temporal.io/api/history/v1"
	updatepb "go.temporal.io/api/update/v1"
)

func TestExtractPayloadsFromEvent_NestedReferences(t *testing.T) {
	t.Parallel()
	payload := func(key string) *commonpb.Payload {
		return &commonpb.Payload{Metadata: map[string][]byte{
			CodecMetadataKey: []byte("codec/" + key),
		}}
	}
	header := payload("header")
	memo := payload("memo")
	search := payload("search")
	attributes := payload("failure-attributes")
	details := payload("failure-details")
	update := payload("update-result")
	tests := []struct {
		name  string
		event *historypb.HistoryEvent
		want  []*commonpb.Payload
	}{
		{
			name: "start metadata",
			event: &historypb.HistoryEvent{
				EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
				Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
					WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
						Header:           &commonpb.Header{Fields: map[string]*commonpb.Payload{"header": header}},
						Memo:             &commonpb.Memo{Fields: map[string]*commonpb.Payload{"memo": memo}},
						SearchAttributes: &commonpb.SearchAttributes{IndexedFields: map[string]*commonpb.Payload{"search": search}},
					},
				},
			},
			want: []*commonpb.Payload{header, memo, search},
		},
		{
			name: "nested failure",
			event: &historypb.HistoryEvent{
				EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED,
				Attributes: &historypb.HistoryEvent_ActivityTaskFailedEventAttributes{
					ActivityTaskFailedEventAttributes: &historypb.ActivityTaskFailedEventAttributes{
						Failure: &failurepb.Failure{
							EncodedAttributes: attributes,
							Cause: &failurepb.Failure{
								FailureInfo: &failurepb.Failure_ApplicationFailureInfo{
									ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
										Details: &commonpb.Payloads{Payloads: []*commonpb.Payload{details}},
									},
								},
							},
						},
					},
				},
			},
			want: []*commonpb.Payload{attributes, details},
		},
		{
			name: "update outcome",
			event: &historypb.HistoryEvent{
				EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_UPDATE_COMPLETED,
				Attributes: &historypb.HistoryEvent_WorkflowExecutionUpdateCompletedEventAttributes{
					WorkflowExecutionUpdateCompletedEventAttributes: &historypb.WorkflowExecutionUpdateCompletedEventAttributes{
						Outcome: &updatepb.Outcome{
							Value: &updatepb.Outcome_Success{
								Success: &commonpb.Payloads{Payloads: []*commonpb.Payload{update}},
							},
						},
					},
				},
			},
			want: []*commonpb.Payload{update},
		},
		{name: "nil event"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.ElementsMatch(t, tt.want, extractPayloadsFromEvent(tt.event))
		})
	}
}
