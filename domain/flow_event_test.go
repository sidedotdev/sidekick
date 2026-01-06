package domain

import (
	"encoding/json"
	"sidekick/common"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnmarshalFlowEvent_CodeDiffEvent(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    FlowEvent
		wantErr bool
	}{
		{
			name: "valid code diff event",
			json: `{
				"eventType": "code_diff",
				"subflowId": "sf_123",
				"diff": "diff --git a/file.txt b/file.txt\n@@ -1,1 +1,1 @@\n-old\n+new"
			}`,
			want: CodeDiffEvent{
				EventType: CodeDiffEventType,
				SubflowId: "sf_123",
				Diff:      "diff --git a/file.txt b/file.txt\n@@ -1,1 +1,1 @@\n-old\n+new",
			},
			wantErr: false,
		},
		{
			name: "invalid json",
			json: `{
				"eventType": "codeDiff",
				"subflowId": "sf_123"
				"diff": "some diff"
			}`,
			want:    nil,
			wantErr: true,
		},
		{
			name: "missing required field",
			json: `{
				"eventType": "codeDiff",
				"subflowId": "sf_123"
			}`,
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UnmarshalFlowEvent([]byte(tt.json))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)

			// Verify the interface methods return expected values
			if tt.want != nil {
				assert.Equal(t, tt.want.GetEventType(), got.GetEventType())
				assert.Equal(t, tt.want.GetParentId(), got.GetParentId())
			}
		})
	}
}

func TestUnmarshalFlowEvent_CodeDiffEvent_RoundTrip(t *testing.T) {
	original := CodeDiffEvent{
		EventType: CodeDiffEventType,
		SubflowId: "sf_123",
		Diff:      "diff --git a/file.txt b/file.txt\n@@ -1,1 +1,1 @@\n-old\n+new",
	}

	// Marshal to JSON
	data, err := json.Marshal(original)
	assert.NoError(t, err)

	// Unmarshal back
	got, err := UnmarshalFlowEvent(data)
	assert.NoError(t, err)

	// Compare
	assert.Equal(t, original, got)
}

func TestFlowEventContainer_MarshalUnmarshal_RoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		original FlowEventContainer
	}{
		{
			name: "ProgressTextEvent",
			original: FlowEventContainer{
				FlowEvent: ProgressTextEvent{
					EventType: ProgressTextEventType,
					ParentId:  "flow_123",
					Text:      "Running tests...",
				},
			},
		},
		{
			name: "ChatMessageDeltaEvent",
			original: FlowEventContainer{
				FlowEvent: ChatMessageDeltaEvent{
					EventType:    ChatMessageDeltaEventType,
					FlowActionId: "fa_456",
					ChatMessageDelta: common.ChatMessageDelta{
						Content: "delta content",
					},
				},
			},
		},
		{
			name: "EndStreamEvent",
			original: FlowEventContainer{
				FlowEvent: EndStreamEvent{
					EventType: EndStreamEventType,
					ParentId:  "subflow_789",
				},
			},
		},
		{
			name: "StatusChangeEvent",
			original: FlowEventContainer{
				FlowEvent: StatusChangeEvent{
					EventType: StatusChangeEventType,
					ParentId:  "flow_abc",
					TargetId:  "sf_xyz",
					Status:    "COMPLETED",
				},
			},
		},
		{
			name: "CodeDiffEvent",
			original: FlowEventContainer{
				FlowEvent: CodeDiffEvent{
					EventType: CodeDiffEventType,
					SubflowId: "sf_xyz",
					Diff:      "diff --git a/file.txt b/file.txt\n@@ -1,1 +1,1 @@\n-old\n+new",
				},
			},
		},
		{
			name: "DevRunStartedEvent",
			original: FlowEventContainer{
				FlowEvent: DevRunStartedEvent{
					EventType:      DevRunStartedEventType,
					FlowId:         "flow_123",
					DevRunId:       "devrun_456",
					CommandSummary: "npm run dev",
					WorkingDir:     "/tmp/worktree",
					Pid:            12345,
					Pgid:           12345,
				},
			},
		},
		{
			name: "DevRunOutputEvent",
			original: FlowEventContainer{
				FlowEvent: DevRunOutputEvent{
					EventType: DevRunOutputEventType,
					DevRunId:  "devrun_456",
					Stream:    "stdout",
					Chunk:     "Server started on port 3000\n",
					Sequence:  1,
					Timestamp: 1700000000000,
				},
			},
		},
		{
			name: "DevRunEndedEvent with exit status",
			original: FlowEventContainer{
				FlowEvent: DevRunEndedEvent{
					EventType:  DevRunEndedEventType,
					FlowId:     "flow_123",
					DevRunId:   "devrun_456",
					ExitStatus: intPtr(0),
				},
			},
		},
		{
			name: "DevRunEndedEvent with signal",
			original: FlowEventContainer{
				FlowEvent: DevRunEndedEvent{
					EventType: DevRunEndedEventType,
					FlowId:    "flow_123",
					DevRunId:  "devrun_456",
					Signal:    "SIGINT",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal to JSON
			data, err := json.Marshal(tt.original)
			assert.NoError(t, err)
			assert.NotEmpty(t, data)

			// Unmarshal back
			var got FlowEventContainer
			err = json.Unmarshal(data, &got)
			assert.NoError(t, err)

			// Compare
			assert.Equal(t, tt.original, got)
		})
	}
}

func intPtr(i int) *int {
	return &i
}

func TestUnmarshalFlowEvent_DevRunStartedEvent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		json    string
		want    FlowEvent
		wantErr bool
	}{
		{
			name: "valid dev run started event",
			json: `{
				"eventType": "dev_run_started",
				"flowId": "flow_123",
				"devRunId": "devrun_456",
				"commandSummary": "npm run dev",
				"workingDir": "/tmp/worktree",
				"pid": 12345,
				"pgid": 12345
			}`,
			want: DevRunStartedEvent{
				EventType:      DevRunStartedEventType,
				FlowId:         "flow_123",
				DevRunId:       "devrun_456",
				CommandSummary: "npm run dev",
				WorkingDir:     "/tmp/worktree",
				Pid:            12345,
				Pgid:           12345,
			},
			wantErr: false,
		},
		{
			name:    "invalid json",
			json:    `{"eventType": "dev_run_started", "flowId": "flow_123" "devRunId": "devrun_456"}`,
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := UnmarshalFlowEvent([]byte(tt.json))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)

			if tt.want != nil {
				assert.Equal(t, tt.want.GetEventType(), got.GetEventType())
				assert.Equal(t, tt.want.GetParentId(), got.GetParentId())
			}
		})
	}
}

func TestUnmarshalFlowEvent_DevRunStartedEvent_RoundTrip(t *testing.T) {
	t.Parallel()
	original := DevRunStartedEvent{
		EventType:      DevRunStartedEventType,
		FlowId:         "flow_123",
		DevRunId:       "devrun_456",
		CommandSummary: "npm run dev",
		WorkingDir:     "/tmp/worktree",
		Pid:            12345,
		Pgid:           12345,
	}

	data, err := json.Marshal(original)
	assert.NoError(t, err)

	got, err := UnmarshalFlowEvent(data)
	assert.NoError(t, err)
	assert.Equal(t, original, got)
}

func TestUnmarshalFlowEvent_DevRunOutputEvent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		json    string
		want    FlowEvent
		wantErr bool
	}{
		{
			name: "valid stdout output event",
			json: `{
				"eventType": "dev_run_output",
				"devRunId": "devrun_456",
				"stream": "stdout",
				"chunk": "Server started\n",
				"sequence": 1,
				"timestamp": 1700000000000
			}`,
			want: DevRunOutputEvent{
				EventType: DevRunOutputEventType,
				DevRunId:  "devrun_456",
				Stream:    "stdout",
				Chunk:     "Server started\n",
				Sequence:  1,
				Timestamp: 1700000000000,
			},
			wantErr: false,
		},
		{
			name: "valid stderr output event",
			json: `{
				"eventType": "dev_run_output",
				"devRunId": "devrun_456",
				"stream": "stderr",
				"chunk": "Warning: deprecated API\n",
				"sequence": 2,
				"timestamp": 1700000000001
			}`,
			want: DevRunOutputEvent{
				EventType: DevRunOutputEventType,
				DevRunId:  "devrun_456",
				Stream:    "stderr",
				Chunk:     "Warning: deprecated API\n",
				Sequence:  2,
				Timestamp: 1700000000001,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := UnmarshalFlowEvent([]byte(tt.json))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)

			if tt.want != nil {
				assert.Equal(t, tt.want.GetEventType(), got.GetEventType())
				assert.Equal(t, tt.want.GetParentId(), got.GetParentId())
			}
		})
	}
}

func TestUnmarshalFlowEvent_DevRunOutputEvent_RoundTrip(t *testing.T) {
	t.Parallel()
	original := DevRunOutputEvent{
		EventType: DevRunOutputEventType,
		DevRunId:  "devrun_456",
		Stream:    "stdout",
		Chunk:     "Server started on port 3000\n",
		Sequence:  1,
		Timestamp: 1700000000000,
	}

	data, err := json.Marshal(original)
	assert.NoError(t, err)

	got, err := UnmarshalFlowEvent(data)
	assert.NoError(t, err)
	assert.Equal(t, original, got)
}

func TestUnmarshalFlowEvent_DevRunEndedEvent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		json    string
		want    FlowEvent
		wantErr bool
	}{
		{
			name: "ended with exit status 0",
			json: `{
				"eventType": "dev_run_ended",
				"flowId": "flow_123",
				"devRunId": "devrun_456",
				"exitStatus": 0
			}`,
			want: DevRunEndedEvent{
				EventType:  DevRunEndedEventType,
				FlowId:     "flow_123",
				DevRunId:   "devrun_456",
				ExitStatus: intPtr(0),
			},
			wantErr: false,
		},
		{
			name: "ended with non-zero exit status",
			json: `{
				"eventType": "dev_run_ended",
				"flowId": "flow_123",
				"devRunId": "devrun_456",
				"exitStatus": 1
			}`,
			want: DevRunEndedEvent{
				EventType:  DevRunEndedEventType,
				FlowId:     "flow_123",
				DevRunId:   "devrun_456",
				ExitStatus: intPtr(1),
			},
			wantErr: false,
		},
		{
			name: "ended with signal",
			json: `{
				"eventType": "dev_run_ended",
				"flowId": "flow_123",
				"devRunId": "devrun_456",
				"signal": "SIGINT"
			}`,
			want: DevRunEndedEvent{
				EventType: DevRunEndedEventType,
				FlowId:    "flow_123",
				DevRunId:  "devrun_456",
				Signal:    "SIGINT",
			},
			wantErr: false,
		},
		{
			name: "ended with error",
			json: `{
				"eventType": "dev_run_ended",
				"flowId": "flow_123",
				"devRunId": "devrun_456",
				"error": "failed to start process"
			}`,
			want: DevRunEndedEvent{
				EventType: DevRunEndedEventType,
				FlowId:    "flow_123",
				DevRunId:  "devrun_456",
				Error:     "failed to start process",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := UnmarshalFlowEvent([]byte(tt.json))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)

			if tt.want != nil {
				assert.Equal(t, tt.want.GetEventType(), got.GetEventType())
				assert.Equal(t, tt.want.GetParentId(), got.GetParentId())
			}
		})
	}
}

func TestUnmarshalFlowEvent_DevRunEndedEvent_RoundTrip(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		original DevRunEndedEvent
	}{
		{
			name: "with exit status",
			original: DevRunEndedEvent{
				EventType:  DevRunEndedEventType,
				FlowId:     "flow_123",
				DevRunId:   "devrun_456",
				ExitStatus: intPtr(0),
			},
		},
		{
			name: "with signal",
			original: DevRunEndedEvent{
				EventType: DevRunEndedEventType,
				FlowId:    "flow_123",
				DevRunId:  "devrun_456",
				Signal:    "SIGKILL",
			},
		},
		{
			name: "with error",
			original: DevRunEndedEvent{
				EventType: DevRunEndedEventType,
				FlowId:    "flow_123",
				DevRunId:  "devrun_456",
				Error:     "process not found",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(tc.original)
			assert.NoError(t, err)

			got, err := UnmarshalFlowEvent(data)
			assert.NoError(t, err)
			assert.Equal(t, tc.original, got)
		})
	}
}

func TestDevRunEvents_GetParentId(t *testing.T) {
	t.Parallel()

	t.Run("DevRunStartedEvent returns flowId", func(t *testing.T) {
		t.Parallel()
		event := DevRunStartedEvent{
			EventType: DevRunStartedEventType,
			FlowId:    "flow_123",
			DevRunId:  "devrun_456",
		}
		assert.Equal(t, "flow_123", event.GetParentId())
	})

	t.Run("DevRunOutputEvent returns devRunId", func(t *testing.T) {
		t.Parallel()
		event := DevRunOutputEvent{
			EventType: DevRunOutputEventType,
			DevRunId:  "devrun_456",
		}
		assert.Equal(t, "devrun_456", event.GetParentId())
	})

	t.Run("DevRunEndedEvent returns flowId", func(t *testing.T) {
		t.Parallel()
		event := DevRunEndedEvent{
			EventType: DevRunEndedEventType,
			FlowId:    "flow_123",
			DevRunId:  "devrun_456",
		}
		assert.Equal(t, "flow_123", event.GetParentId())
	})
}
