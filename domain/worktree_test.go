package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWorktreeJSONSerialization(t *testing.T) {
	now := time.Now().UTC()
	worktree := Worktree{
		Id:          "wt_123",
		FlowId:      "flow_456",
		Name:        "test-worktree",
		Created:     now,
		WorkspaceId: "ws_789",
	}

	// Serialize to JSON
	jsonData, err := json.Marshal(worktree)
	assert.NoError(t, err)

	// Deserialize from JSON
	var deserializedWorktree Worktree
	err = json.Unmarshal(jsonData, &deserializedWorktree)
	assert.NoError(t, err)

	// Compare original and deserialized worktree
	assert.Equal(t, worktree.Id, deserializedWorktree.Id)
	assert.Equal(t, worktree.FlowId, deserializedWorktree.FlowId)
	assert.Equal(t, worktree.Name, deserializedWorktree.Name)
	assert.Equal(t, worktree.Created.Unix(), deserializedWorktree.Created.Unix())
	assert.Equal(t, worktree.WorkspaceId, deserializedWorktree.WorkspaceId)
}

func TestWorktreeIdPrefix(t *testing.T) {
	worktree := Worktree{
		Id: "wt_123",
	}

	assert.True(t, len(worktree.Id) > 3)
	assert.Equal(t, "wt_", worktree.Id[:3])
}
