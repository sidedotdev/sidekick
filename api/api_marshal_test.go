package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"sidekick/domain"
)

func TestTaskResponse_MarshalJSON_UTC(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("failed to load location: %v", err)
	}

	created := time.Date(2025, 12, 31, 15, 1, 3, 542966123, loc)
	updated := time.Date(2025, 12, 31, 16, 30, 45, 123456789, loc)

	task := domain.Task{
		WorkspaceId: "ws-id",
		Id:          "task-id",
		Title:       "Test Task",
		Description: "Test Description",
		Status:      domain.TaskStatusDrafting,
		Created:     created,
		Updated:     updated,
	}

	flows := []domain.Flow{
		{Id: "flow-1", WorkspaceId: "ws-id"},
		{Id: "flow-2", WorkspaceId: "ws-id"},
	}

	tr := TaskResponse{
		Task:  task,
		Flows: flows,
	}

	data, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("failed to marshal TaskResponse: %v", err)
	}

	jsonStr := string(data)

	// Verify UTC format (ends with Z, not timezone offset)
	if strings.Contains(jsonStr, "-08:00") || strings.Contains(jsonStr, "-07:00") {
		t.Errorf("JSON contains timezone offset instead of UTC: %s", jsonStr)
	}

	// Verify sub-millisecond precision is preserved
	if !strings.Contains(jsonStr, "542966123") {
		t.Errorf("JSON missing sub-millisecond precision for created: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, "123456789") {
		t.Errorf("JSON missing sub-millisecond precision for updated: %s", jsonStr)
	}

	// Verify flows are included
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	flowsResult, ok := result["flows"].([]interface{})
	if !ok {
		t.Fatalf("flows not found or wrong type in result")
	}
	if len(flowsResult) != 2 {
		t.Errorf("expected 2 flows, got %d", len(flowsResult))
	}

	// Verify task fields are at top level (not nested under "task")
	if _, exists := result["id"]; !exists {
		t.Error("task id should be at top level")
	}
	if _, exists := result["title"]; !exists {
		t.Error("task title should be at top level")
	}

	createdStr := result["created"].(string)
	updatedStr := result["updated"].(string)

	if !strings.HasSuffix(createdStr, "Z") {
		t.Errorf("created should end with Z: %s", createdStr)
	}
	if !strings.HasSuffix(updatedStr, "Z") {
		t.Errorf("updated should end with Z: %s", updatedStr)
	}
}
