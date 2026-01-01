package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFlowAction_MarshalJSON_UTC(t *testing.T) {
	t.Parallel()

	// Use a non-UTC timezone with sub-millisecond precision
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("failed to load location: %v", err)
	}

	created := time.Date(2025, 12, 31, 15, 1, 3, 542966123, loc)
	updated := time.Date(2025, 12, 31, 16, 30, 45, 123456789, loc)

	fa := FlowAction{
		Id:           "test-id",
		FlowId:       "flow-id",
		WorkspaceId:  "ws-id",
		Created:      created,
		Updated:      updated,
		ActionType:   "test",
		ActionStatus: ActionStatusPending,
	}

	data, err := json.Marshal(fa)
	if err != nil {
		t.Fatalf("failed to marshal FlowAction: %v", err)
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

	// Unmarshal and verify the times are correct
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	createdStr := result["created"].(string)
	updatedStr := result["updated"].(string)

	if !strings.HasSuffix(createdStr, "Z") {
		t.Errorf("created should end with Z: %s", createdStr)
	}
	if !strings.HasSuffix(updatedStr, "Z") {
		t.Errorf("updated should end with Z: %s", updatedStr)
	}

	// Parse and verify the actual time values are equivalent
	parsedCreated, err := time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		t.Fatalf("failed to parse created: %v", err)
	}
	if !parsedCreated.Equal(created) {
		t.Errorf("created time mismatch: got %v, want %v", parsedCreated, created)
	}
}

func TestTask_MarshalJSON_UTC(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("failed to load location: %v", err)
	}

	created := time.Date(2025, 6, 15, 10, 30, 0, 999888777, loc)
	updated := time.Date(2025, 6, 15, 11, 45, 30, 111222333, loc)
	archived := time.Date(2025, 6, 15, 12, 0, 0, 444555666, loc)

	task := Task{
		WorkspaceId: "ws-id",
		Id:          "task-id",
		Title:       "Test Task",
		Description: "Test Description",
		Status:      TaskStatusDrafting,
		Created:     created,
		Updated:     updated,
		Archived:    &archived,
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("failed to marshal Task: %v", err)
	}

	jsonStr := string(data)

	// Verify no timezone offset
	if strings.Contains(jsonStr, "-05:00") || strings.Contains(jsonStr, "-04:00") {
		t.Errorf("JSON contains timezone offset instead of UTC: %s", jsonStr)
	}

	// Verify sub-millisecond precision
	if !strings.Contains(jsonStr, "999888777") {
		t.Errorf("JSON missing sub-millisecond precision for created: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, "111222333") {
		t.Errorf("JSON missing sub-millisecond precision for updated: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, "444555666") {
		t.Errorf("JSON missing sub-millisecond precision for archived: %s", jsonStr)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	for _, field := range []string{"created", "updated", "archived"} {
		val := result[field].(string)
		if !strings.HasSuffix(val, "Z") {
			t.Errorf("%s should end with Z: %s", field, val)
		}
	}
}

func TestTask_MarshalJSON_NilArchived(t *testing.T) {
	t.Parallel()

	task := Task{
		WorkspaceId: "ws-id",
		Id:          "task-id",
		Title:       "Test Task",
		Created:     time.Now(),
		Updated:     time.Now(),
		Archived:    nil,
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("failed to marshal Task: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if _, exists := result["archived"]; exists {
		t.Errorf("archived should be omitted when nil, got: %v", result["archived"])
	}
}

func TestUTCTime(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("failed to load location: %v", err)
	}

	original := time.Date(2025, 1, 1, 12, 0, 0, 123456789, loc)
	result := UTCTime(original)

	if result.Location() != time.UTC {
		t.Errorf("expected UTC location, got %v", result.Location())
	}
	if !result.Equal(original) {
		t.Errorf("time value changed: got %v, want %v", result, original)
	}
}

func TestUTCTimePtr(t *testing.T) {
	t.Parallel()

	t.Run("nil input", func(t *testing.T) {
		t.Parallel()
		result := UTCTimePtr(nil)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("non-nil input", func(t *testing.T) {
		t.Parallel()
		loc, err := time.LoadLocation("Europe/London")
		if err != nil {
			t.Fatalf("failed to load location: %v", err)
		}

		original := time.Date(2025, 7, 1, 15, 30, 0, 987654321, loc)
		result := UTCTimePtr(&original)

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Location() != time.UTC {
			t.Errorf("expected UTC location, got %v", result.Location())
		}
		if !result.Equal(original) {
			t.Errorf("time value changed: got %v, want %v", *result, original)
		}
	})
}

func TestWorkspace_MarshalJSON_UTC(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("failed to load location: %v", err)
	}

	created := time.Date(2025, 12, 31, 15, 1, 3, 542966123, loc)
	updated := time.Date(2025, 12, 31, 16, 30, 45, 123456789, loc)

	ws := Workspace{
		Id:           "ws-test-id",
		Name:         "Test Workspace",
		LocalRepoDir: "/path/to/repo",
		ConfigMode:   "merge",
		Created:      created,
		Updated:      updated,
	}

	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("failed to marshal Workspace: %v", err)
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

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	createdStr := result["created"].(string)
	updatedStr := result["updated"].(string)

	if !strings.HasSuffix(createdStr, "Z") {
		t.Errorf("created should end with Z: %s", createdStr)
	}
	if !strings.HasSuffix(updatedStr, "Z") {
		t.Errorf("updated should end with Z: %s", updatedStr)
	}

	// Parse and verify the actual time values are equivalent
	parsedCreated, err := time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		t.Fatalf("failed to parse created: %v", err)
	}
	if !parsedCreated.Equal(created) {
		t.Errorf("created time mismatch: got %v, want %v", parsedCreated, created)
	}
}
