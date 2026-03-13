package callers

import (
	"context"
	"fixture/chathistory"
	"fixture/workflow"
)

// testWorkflowHelper is in a _test.go file and has workflow.Context.
// It should be skipped by the linter despite having workflow context.
func testWorkflowHelper(ctx workflow.Context, h *chathistory.ChatHistoryContainer, msg chathistory.Message) {
	h.Append(msg)
}

// testActivityCaller calls ActivityFunc from a test file with workflow context.
func testActivityCaller(ctx workflow.Context) {
	ActivityFunc(context.Background())
}