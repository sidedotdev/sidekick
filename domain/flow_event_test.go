package domain

import (
	"encoding/json"
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
