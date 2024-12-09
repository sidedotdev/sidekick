package poll_failures

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	commonApi "go.temporal.io/api/common/v1"
	workflowApi "go.temporal.io/api/workflow/v1"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
)

type PollFailuresWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
	pfa *PollFailuresActivities
}

func (s *PollFailuresWorkflowTestSuite) SetupTest() {
	// log warnings only (default debug level is too noisy when tests fail)
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(PollFailuresWorkflow)
	s.env.RegisterActivity(s.pfa.ListFailedWorkflows)
	s.env.RegisterActivity(s.pfa.UpdateTaskStatus)
}

func (s *PollFailuresWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestPollFailuresWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(PollFailuresWorkflowTestSuite))
}

func (s *PollFailuresWorkflowTestSuite) TestNoFailedWorkflows() {
	s.env.OnActivity(s.pfa.ListFailedWorkflows, mock.Anything, mock.Anything).Return([]*workflowApi.WorkflowExecutionInfo{}, nil)
	s.env.OnActivity(s.pfa.UpdateTaskStatus, mock.Anything, mock.Anything).Never()
	s.env.ExecuteWorkflow(PollFailuresWorkflow, PollFailuresWorkflowInput{WorkspaceId: "workspace_id"})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *PollFailuresWorkflowTestSuite) TestFailedWorkflowsDetected() {
	failedWorkflows := []*workflowApi.WorkflowExecutionInfo{
		{Execution: &commonApi.WorkflowExecution{WorkflowId: "failed_workflow_1"}},
	}
	s.env.OnActivity(s.pfa.ListFailedWorkflows, mock.Anything, mock.Anything).Return(failedWorkflows, nil)
	s.env.OnActivity(s.pfa.UpdateTaskStatus, mock.Anything, mock.Anything).Return(nil).Once()

	s.env.ExecuteWorkflow(PollFailuresWorkflow, PollFailuresWorkflowInput{WorkspaceId: "workspace_id"})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}
