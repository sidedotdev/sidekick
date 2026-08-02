package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStringToProjectPriority(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input   string
		want    ProjectPriority
		wantErr bool
	}{
		{"none", ProjectPriorityNone, false},
		{"low", ProjectPriorityLow, false},
		{"medium", ProjectPriorityMedium, false},
		{"high", ProjectPriorityHigh, false},
		{"urgent", ProjectPriorityUrgent, false},
		{"", "", true},
		{"critical", "", true},
		{"None", "", true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got, err := StringToProjectPriority(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for input %q, got none", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for input %q: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProjectPriority_SortBucket(t *testing.T) {
	t.Parallel()

	ordered := []ProjectPriority{
		ProjectPriorityUrgent,
		ProjectPriorityHigh,
		ProjectPriorityMedium,
		ProjectPriorityLow,
		ProjectPriorityNone,
	}
	for i := 1; i < len(ordered); i++ {
		if ordered[i-1].SortBucket() >= ordered[i].SortBucket() {
			t.Errorf("expected %q to sort before %q", ordered[i-1], ordered[i])
		}
	}
}

func TestProject_MarshalJSON_UTC(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("failed to load location: %v", err)
	}

	project := Project{
		WorkspaceId: "ws_1",
		Id:          "project_1",
		Title:       "Test Project",
		Description: "A description",
		Priority:    ProjectPriorityHigh,
		Rank:        "aa",
		Created:     time.Date(2025, 1, 1, 12, 0, 0, 0, loc),
		Updated:     time.Date(2025, 1, 2, 12, 0, 0, 0, loc),
	}

	data, err := json.Marshal(project)
	if err != nil {
		t.Fatalf("failed to marshal project: %v", err)
	}
	jsonStr := string(data)

	if strings.Contains(jsonStr, "+09:00") {
		t.Errorf("expected UTC timestamps, got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"created":"2025-01-01T03:00:00Z"`) {
		t.Errorf("expected UTC created timestamp, got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"updated":"2025-01-02T03:00:00Z"`) {
		t.Errorf("expected UTC updated timestamp, got: %s", jsonStr)
	}
	for _, field := range []string{`"workspaceId":"ws_1"`, `"id":"project_1"`, `"title":"Test Project"`, `"description":"A description"`, `"priority":"high"`, `"rank":"aa"`} {
		if !strings.Contains(jsonStr, field) {
			t.Errorf("expected JSON to contain %s, got: %s", field, jsonStr)
		}
	}

	var roundTripped Project
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("failed to unmarshal project: %v", err)
	}
	if roundTripped.Priority != ProjectPriorityHigh {
		t.Errorf("priority round-trip mismatch: got %q", roundTripped.Priority)
	}
}
