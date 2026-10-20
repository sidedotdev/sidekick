package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/mocks"
)

func TestIsReplayableVersion(t *testing.T) {
	t.Parallel()

	output, err := exec.Command("git", "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		t.Fatalf("resolve repository HEAD: %v", err)
	}
	head := strings.TrimSpace(string(output))

	tests := []struct {
		name        string
		currentRef  string
		workflowRef string
		want        bool
	}{
		{
			name:        "same commit",
			currentRef:  head,
			workflowRef: head,
			want:        true,
		},
		{
			name:        "equivalent refs",
			currentRef:  head,
			workflowRef: "HEAD",
			want:        true,
		},
		{
			name:       "missing workflow version",
			currentRef: head,
		},
		{
			name:        "missing current version",
			workflowRef: head,
		},
		{
			name: "both versions missing",
		},
		{
			name:        "unresolved workflow version",
			currentRef:  head,
			workflowRef: "not a valid git revision",
		},
		{
			name:        "unresolved current version",
			currentRef:  "not a valid git revision",
			workflowRef: head,
		},
		{
			name:        "identical unresolved refs",
			currentRef:  "not a valid git revision",
			workflowRef: "not a valid git revision",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isReplayableVersion(tt.currentRef, tt.workflowRef); got != tt.want {
				t.Errorf("isReplayableVersion(%q, %q) = %t, want %t",
					tt.currentRef, tt.workflowRef, got, tt.want)
			}
		})
	}
}

func TestListRecentRunningWorkflowsUsesWorkerBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		startRef string
		worker   *commonpb.WorkerVersionStamp
		want     string
	}{
		{
			name:     "worker supersedes start memo",
			startRef: "legacy-start-revision",
			worker:   &commonpb.WorkerVersionStamp{BuildId: "latest-worker-revision"},
			want:     "latest-worker-revision",
		},
		{
			name:   "worker without start memo",
			worker: &commonpb.WorkerVersionStamp{BuildId: "latest-worker-revision"},
			want:   "latest-worker-revision",
		},
		{
			name:     "missing worker never falls back to start memo",
			startRef: "legacy-start-revision",
		},
		{
			name:     "empty worker never falls back to start memo",
			startRef: "legacy-start-revision",
			worker:   &commonpb.WorkerVersionStamp{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			info := &workflowpb.WorkflowExecutionInfo{
				Execution:                    &commonpb.WorkflowExecution{WorkflowId: "workflow", RunId: "run"},
				Status:                       enums.WORKFLOW_EXECUTION_STATUS_RUNNING,
				MostRecentWorkerVersionStamp: tt.worker,
			}
			if tt.startRef != "" {
				payload, err := converter.GetDefaultDataConverter().ToPayload(tt.startRef)
				if err != nil {
					t.Fatal(err)
				}
				info.Memo = &commonpb.Memo{Fields: map[string]*commonpb.Payload{"sidekickVersion": payload}}
			}
			c := mocks.NewClient(t)
			c.On("ListWorkflow", mock.Anything, mock.Anything).
				Return(&workflowservice.ListWorkflowExecutionsResponse{
					Executions: []*workflowpb.WorkflowExecutionInfo{info},
				}, nil).Once()

			listed, err := listRecentRunningWorkflows(context.Background(), c, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(listed) != 1 {
				t.Fatalf("got %d workflows, want 1", len(listed))
			}
			if got := listed[0].workerBuildID; got != tt.want {
				t.Errorf("worker version = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReplayHistoryEligibility(t *testing.T) {
	t.Parallel()

	output, err := exec.Command("git", "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(output))
	if reason := replayBuildSkipReason(head, head); reason != "" {
		t.Fatalf("current build must be eligible for listing selection: %s", reason)
	}

	completed := func(buildID string) *historypb.HistoryEvent {
		return &historypb.HistoryEvent{
			EventType: enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED,
			Attributes: &historypb.HistoryEvent_WorkflowTaskCompletedEventAttributes{
				WorkflowTaskCompletedEventAttributes: &historypb.WorkflowTaskCompletedEventAttributes{
					WorkerVersion: &commonpb.WorkerVersionStamp{BuildId: buildID},
				},
			},
		}
	}
	started := &historypb.HistoryEvent{
		EventType: enums.EVENT_TYPE_WORKFLOW_TASK_STARTED,
		Attributes: &historypb.HistoryEvent_WorkflowTaskStartedEventAttributes{
			WorkflowTaskStartedEventAttributes: &historypb.WorkflowTaskStartedEventAttributes{
				WorkerVersion: &commonpb.WorkerVersionStamp{BuildId: head + "-dirty"},
			},
		},
	}
	payload, err := converter.GetDefaultDataConverter().ToPayload(head)
	if err != nil {
		t.Fatal(err)
	}
	legacyStart := &historypb.HistoryEvent{
		EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
			WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
				Memo: &commonpb.Memo{Fields: map[string]*commonpb.Payload{"sidekickVersion": payload}},
			},
		},
	}

	tests := []struct {
		name       string
		events     []*historypb.HistoryEvent
		wantReason string
	}{
		{
			name:   "completed current build",
			events: []*historypb.HistoryEvent{completed(head)},
		},
		{
			name:       "eligible listing followed by incompatible completed build",
			events:     []*historypb.HistoryEvent{completed(head), completed(strings.Repeat("0", 40))},
			wantReason: "unavailable or is not an ancestor",
		},
		{
			name:       "eligible listing followed by missing completed build",
			events:     []*historypb.HistoryEvent{completed(head), completed("")},
			wantReason: "missing",
		},
		{
			name:       "latest completed task has no attributes",
			events:     []*historypb.HistoryEvent{completed(head), {EventType: enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED}},
			wantReason: "missing",
		},
		{
			name:   "in flight build does not replace completed build",
			events: []*historypb.HistoryEvent{completed(head), started},
		},
		{
			name:       "in flight task alone is insufficient",
			events:     []*historypb.HistoryEvent{started},
			wantReason: "no completed workflow task",
		},
		{
			name:       "start memo is not a worker build",
			events:     []*historypb.HistoryEvent{legacyStart},
			wantReason: "no completed workflow task",
		},
		{
			name:       "opaque legacy build hash",
			events:     []*historypb.HistoryEvent{completed(strings.Repeat("a", 32))},
			wantReason: "not a full Git commit SHA",
		},
		{
			name:       "dirty ancestor is not proven compatible",
			events:     []*historypb.HistoryEvent{completed(head + "-dirty")},
			wantReason: "uncommitted code",
		},
		{
			name:       "non hexadecimal build",
			events:     []*historypb.HistoryEvent{completed(strings.Repeat("z", 40))},
			wantReason: "not a Git commit SHA",
		},
		{
			name:       "empty history",
			wantReason: "no completed workflow task",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reason := replayHistorySkipReason(head, &historypb.History{Events: tt.events})
			if tt.wantReason == "" {
				if reason != "" {
					t.Fatalf("eligible history was excluded: %s", reason)
				}
			} else if !strings.Contains(reason, tt.wantReason) {
				t.Fatalf("exclusion reason = %q, want it to contain %q", reason, tt.wantReason)
			}
		})
	}
}

func TestIsReplayableVersionAncestry(t *testing.T) {
	t.Parallel()

	output, err := exec.Command("git", "rev-parse", "--verify", "HEAD^").Output()
	if err != nil {
		t.Skip("ancestry test requires a checkout with a parent commit")
	}
	parent := strings.TrimSpace(string(output))
	if !isReplayableVersion("HEAD", parent) {
		t.Error("parent commit must be eligible")
	}
	if isReplayableVersion(parent, "HEAD") {
		t.Error("descendant commit must not be eligible")
	}
}

func TestSelectReplayCandidatesIgnoresVisibilityBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		buildID string
	}{
		{name: "missing"},
		{name: "opaque", buildID: strings.Repeat("a", 32)},
		{name: "dirty", buildID: strings.Repeat("a", 40) + "-dirty"},
		{name: "unavailable commit", buildID: strings.Repeat("0", 40)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			listed := []listedWorkflow{
				{id: "closed", status: enums.WORKFLOW_EXECUTION_STATUS_COMPLETED},
				{id: "blacklisted", status: enums.WORKFLOW_EXECUTION_STATUS_RUNNING},
				{id: "candidate", status: enums.WORKFLOW_EXECUTION_STATUS_RUNNING, workerBuildID: tt.buildID},
				{id: "past-limit", status: enums.WORKFLOW_EXECUTION_STATUS_RUNNING},
			}
			selected := selectReplayCandidates(t, listed, map[string]struct{}{"blacklisted": {}}, 1)
			if len(selected) != 1 || selected[0].id != "candidate" {
				t.Fatalf("selected = %+v; want only candidate, regardless of visibility build", selected)
			}
		})
	}
}
