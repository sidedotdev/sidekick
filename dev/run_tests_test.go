package dev

import (
	"context"
	"log/slog"
	"os"
	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/secret_manager"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type RunTestsTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env          *testsuite.TestWorkflowEnvironment
	dir          string
	envContainer env.EnvContainer

	devContext *DevContext

	// a wrapper is required to set the ctx1 value, so that we can a method that
	// isn't a real workflow. otherwise we get errors about not having
	// StartToClose or ScheduleToCloseTimeout set
	wrapperWorkflow func(ctx workflow.Context) (TestResult, error)
}

func (s *RunTestsTestSuite) SetupTest() {
	// log warnings only (default debug level is too noisy when tests fail)
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	s.env = s.NewTestWorkflowEnvironment()

	s.devContext = &DevContext{
		ExecContext: flow_action.ExecContext{
			GlobalState:  &flow_action.GlobalState{},
			EnvContainer: &s.envContainer,
			Secrets: &secret_manager.SecretManagerContainer{
				SecretManager: secret_manager.MockSecretManager{},
			},
			FlowScope: &flow_action.FlowScope{
				SubflowName: "RunTestsTestSuite",
			},
		},
		RepoConfig: common.RepoConfig{},
	}

	s.wrapperWorkflow = func(ctx workflow.Context) (TestResult, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := *s.devContext
		dCtx.ExecContext.Context = ctx
		// Use RepoConfig.TestCommands by default for existing tests.
		// New tests for integration commands will need to pass a different slice.
		return RunTests(dCtx, dCtx.RepoConfig.TestCommands)
	}
	s.env.RegisterWorkflow(s.wrapperWorkflow)

	// mock common activity responses that are the same for all test cases
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	dir := s.T().TempDir()
	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{
		RepoDir: dir,
	})
	if err != nil {
		s.T().Fatalf("Failed to create local environment: %v", err)
	}
	s.envContainer = env.EnvContainer{
		Env: devEnv,
	}
}

func (s *RunTestsTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
	os.RemoveAll(s.dir)
}

func (s *RunTestsTestSuite) TestRunTestsWithNoTestCommands() {
	// Temporarily set an empty command list for this specific test
	originalTestCommands := s.devContext.RepoConfig.TestCommands
	s.devContext.RepoConfig.TestCommands = []common.CommandConfig{}
	defer func() { s.devContext.RepoConfig.TestCommands = originalTestCommands }()

	s.env.ExecuteWorkflow(s.wrapperWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result TestResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.TestsPassed)
	s.True(result.TestsSkipped)
	s.Empty(result.Output)
}

func (s *RunTestsTestSuite) TestRunTestsWithAnEmptyTestCommand() {
	s.devContext.RepoConfig = common.RepoConfig{
		TestCommands: []common.CommandConfig{
			{Command: ""},
		},
	}
	s.env.ExecuteWorkflow(s.wrapperWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RunTestsTestSuite) TestRunTestsWithPassingTests() {
	s.devContext.RepoConfig = common.RepoConfig{
		TestCommands: []common.CommandConfig{
			{WorkingDir: ".", Command: "not a real command"},
		},
	}
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(env.EnvRunCommandActivityOutput{
		Stdout:     "xyz pass",
		ExitStatus: 0,
	}, nil).Times(1)

	s.env.ExecuteWorkflow(s.wrapperWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result TestResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.TestsPassed)
	s.Contains(result.Output, "Test Command: not a real command")
	s.Contains(result.Output, "Test Result: Passed")
	s.NotContains(result.Output, "xyz pass") // we don't include test output when it passes
}

func (s *RunTestsTestSuite) TestRunTestsWithFailingTests() {
	s.devContext.RepoConfig = common.RepoConfig{
		TestCommands: []common.CommandConfig{
			{WorkingDir: ".", Command: "failing command"},
		},
	}
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(env.EnvRunCommandActivityOutput{
		Stdout:     "Test output",
		Stderr:     "Error output",
		ExitStatus: 1,
	}, nil).Times(1)

	s.env.ExecuteWorkflow(s.wrapperWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result TestResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.False(result.TestsPassed)
	s.Contains(result.Output, "Test Command: failing command")
	s.Contains(result.Output, "Test Result: Failed")
	s.Contains(result.Output, "Test stderr: Error output")
	s.Contains(result.Output, "Test stdout: Test output")
}

func (s *RunTestsTestSuite) TestRunTestsWithMultipleCommands() {
	s.devContext.RepoConfig = common.RepoConfig{
		TestCommands: []common.CommandConfig{
			{WorkingDir: ".", Command: "passing command"},
			{WorkingDir: ".", Command: "failing command"},
		},
	}
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(input env.EnvRunCommandActivityInput) bool {
		return input.Args[2] == "passing command"
	})).Return(env.EnvRunCommandActivityOutput{
		Stdout:     "xyz pass",
		ExitStatus: 0,
	}, nil).Times(1)
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(input env.EnvRunCommandActivityInput) bool {
		return input.Args[2] == "failing command"
	})).Return(env.EnvRunCommandActivityOutput{
		Stdout:     "abc fail",
		Stderr:     "error output",
		ExitStatus: 1,
	}, nil).Times(1)

	s.env.ExecuteWorkflow(s.wrapperWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result TestResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.False(result.TestsPassed)
	s.Contains(result.Output, "Test Command: passing command")
	s.Contains(result.Output, "Test Result: Passed")
	s.Contains(result.Output, "Test Command: failing command")
	s.Contains(result.Output, "Test Result: Failed")
	s.NotContains(result.Output, "xyz pass")
	s.Contains(result.Output, "abc fail")
	s.Contains(result.Output, "error output")
}

func (s *RunTestsTestSuite) TestRunTestsWithMultipleTestsMixedResult() {
	s.devContext.RepoConfig = common.RepoConfig{
		TestCommands: []common.CommandConfig{
			{Command: "not a real command"},
			{Command: "not a real command 2"},
			{Command: "not a real command 3"},
		},
	}
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(env.EnvRunCommandActivityOutput{
		Stdout:     "test1 pass",
		ExitStatus: 0,
	}, nil).Times(1)
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(env.EnvRunCommandActivityOutput{
		Stdout:     "test2 pass",
		ExitStatus: 0,
	}, nil).Times(1)
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(env.EnvRunCommandActivityOutput{
		Stdout:     "test3 fail out",
		Stderr:     "test3 fail err",
		ExitStatus: 1,
	}, nil).Times(1)

	s.env.ExecuteWorkflow(s.wrapperWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result TestResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.False(result.TestsPassed)
	s.Contains(result.Output, "Test Result: Failed")
	s.NotContains(result.Output, "test1 pass") // leave out passing tests
	s.NotContains(result.Output, "test1 pass") // leave out passing tests
	s.Contains(result.Output, "test3 fail out")
	s.Contains(result.Output, "test3 fail err")
}

func TestRunTestsTestSuite(t *testing.T) {
	suite.Run(t, new(RunTestsTestSuite))
}
