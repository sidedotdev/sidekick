package main

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
)

func TestLatestBuildFilterDoesNotExcuseUnavailableDescriptions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		resp *workflowservice.DescribeWorkflowExecutionResponse
		err  error
	}{
		{name: "missing stamp", resp: &workflowservice.DescribeWorkflowExecutionResponse{
			WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{},
		}},
		{name: "missing info", resp: &workflowservice.DescribeWorkflowExecutionResponse{}},
		{name: "lookup error", err: serviceerror.NewNotFound("run missing")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			filter := newLatestBuildFilter(strings.Repeat("a", 40))
			build, excuse := filter.excuse(context.Background(), func(_ context.Context, id, run string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
				require.Equal(t, "workflow", id)
				require.Equal(t, "explicit-run", run)
				return tc.resp, tc.err
			}, "workflow", "explicit-run")
			require.Empty(t, build)
			require.Empty(t, excuse)
		})
	}
}
