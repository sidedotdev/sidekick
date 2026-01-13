package dev

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/utils"
	"sync"
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

	// mock activities used by GetUserResponse for pause-flow version
	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()

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
	s.False(result.TestsPassed)
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

func (s *RunTestsTestSuite) TestRunTestsWithActivityErrorReturnsError() {
	// Test that when an activity fails with an error (not a test failure),
	// and human-in-the-loop is disabled, we get an error back
	s.devContext.RepoConfig = common.RepoConfig{
		TestCommands: []common.CommandConfig{
			{WorkingDir: ".", Command: "timeout command"},
		},
	}
	s.devContext.ExecContext.DisableHumanInTheLoop = true

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(
		env.EnvRunCommandActivityOutput{}, errors.New("activity timeout"),
	).Times(1)

	s.env.ExecuteWorkflow(s.wrapperWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "activity timeout")
}

func (s *RunTestsTestSuite) TestRunTestsWithRetryOnActivityError() {
	// Test that when an activity fails and user chooses to retry,
	// the failed command is retried and succeeds
	s.devContext.RepoConfig = common.RepoConfig{
		TestCommands: []common.CommandConfig{
			{WorkingDir: ".", Command: "flaky command"},
		},
	}
	s.devContext.ExecContext.DisableHumanInTheLoop = false

	callCount := 0
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
			callCount++
			if callCount == 1 {
				return env.EnvRunCommandActivityOutput{}, errors.New("temporary failure")
			}
			return env.EnvRunCommandActivityOutput{
				Stdout:     "success output",
				ExitStatus: 0,
			}, nil
		},
	)

	// Create a parent workflow that runs the test as a child workflow
	parentWorkflow := func(ctx workflow.Context) (TestResult, error) {
		// Handle signal from child workflow requesting user response
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			var req flow_action.RequestForUser
			signalCh.Receive(ctx, &req)
			// Simulate user responding to retry prompt
			workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", flow_action.SignalNameUserResponse, flow_action.UserResponse{}).Get(ctx, nil)
		})

		// Execute the child workflow
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: "child-test-workflow",
		})
		var result TestResult
		err := workflow.ExecuteChildWorkflow(childCtx, s.wrapperWorkflow).Get(ctx, &result)
		return result, err
	}
	s.env.RegisterWorkflow(parentWorkflow)

	s.env.ExecuteWorkflow(parentWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result TestResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.TestsPassed)
	s.Equal(2, callCount)
}

func (s *RunTestsTestSuite) TestRunTestsWithMultipleCommandsPartialActivityErrorAndRetry() {
	// Regression test: when multiple commands fail with activity errors,
	// user gets a single retry prompt, and all failed commands are retried
	s.devContext.RepoConfig = common.RepoConfig{
		TestCommands: []common.CommandConfig{
			{WorkingDir: ".", Command: "passing command"},
			{WorkingDir: ".", Command: "flaky command 1"},
			{WorkingDir: ".", Command: "flaky command 2"},
		},
	}
	s.devContext.ExecContext.DisableHumanInTheLoop = false

	var mu sync.Mutex
	flakyCallCounts := map[string]int{}
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(input env.EnvRunCommandActivityInput) bool {
		return input.Args[2] == "passing command"
	})).Return(env.EnvRunCommandActivityOutput{
		Stdout:     "pass output",
		ExitStatus: 0,
	}, nil).Times(1)

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(input env.EnvRunCommandActivityInput) bool {
		return input.Args[2] == "flaky command 1"
	})).Return(
		func(ctx context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
			mu.Lock()
			flakyCallCounts["flaky1"]++
			count := flakyCallCounts["flaky1"]
			mu.Unlock()
			if count == 1 {
				return env.EnvRunCommandActivityOutput{}, errors.New("timeout error 1")
			}
			return env.EnvRunCommandActivityOutput{
				Stdout:     "flaky1 success",
				ExitStatus: 0,
			}, nil
		},
	)

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(input env.EnvRunCommandActivityInput) bool {
		return input.Args[2] == "flaky command 2"
	})).Return(
		func(ctx context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
			mu.Lock()
			flakyCallCounts["flaky2"]++
			count := flakyCallCounts["flaky2"]
			mu.Unlock()
			if count == 1 {
				return env.EnvRunCommandActivityOutput{}, errors.New("timeout error 2")
			}
			return env.EnvRunCommandActivityOutput{
				Stdout:     "flaky2 success",
				ExitStatus: 0,
			}, nil
		},
	)

	// Create a parent workflow that runs the test as a child workflow
	parentWorkflow := func(ctx workflow.Context) (TestResult, error) {
		// Handle signal from child workflow requesting user response
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			var req flow_action.RequestForUser
			signalCh.Receive(ctx, &req)
			// Simulate user responding to retry prompt
			workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", flow_action.SignalNameUserResponse, flow_action.UserResponse{}).Get(ctx, nil)
		})

		// Execute the child workflow
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: "child-test-workflow",
		})
		var result TestResult
		err := workflow.ExecuteChildWorkflow(childCtx, s.wrapperWorkflow).Get(ctx, &result)
		return result, err
	}
	s.env.RegisterWorkflow(parentWorkflow)

	s.env.ExecuteWorkflow(parentWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result TestResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.TestsPassed)
	// Both flaky commands should have been called twice (initial + retry)
	mu.Lock()
	flaky1Count := flakyCallCounts["flaky1"]
	flaky2Count := flakyCallCounts["flaky2"]
	mu.Unlock()
	s.Equal(2, flaky1Count)
	s.Equal(2, flaky2Count)
}

func (s *RunTestsTestSuite) TestRunTestsWithMultipleCommandsPartialActivityError() {
	// Regression test: when multiple commands are configured and only some fail
	// with activity errors, verify that the error message includes all failures
	s.devContext.RepoConfig = common.RepoConfig{
		TestCommands: []common.CommandConfig{
			{WorkingDir: ".", Command: "passing command"},
			{WorkingDir: ".", Command: "timeout command 1"},
			{WorkingDir: ".", Command: "timeout command 2"},
		},
	}
	s.devContext.ExecContext.DisableHumanInTheLoop = true

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(input env.EnvRunCommandActivityInput) bool {
		return input.Args[2] == "passing command"
	})).Return(env.EnvRunCommandActivityOutput{
		Stdout:     "pass output",
		ExitStatus: 0,
	}, nil).Times(1)

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(input env.EnvRunCommandActivityInput) bool {
		return input.Args[2] == "timeout command 1"
	})).Return(env.EnvRunCommandActivityOutput{}, errors.New("timeout error 1")).Times(1)

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(input env.EnvRunCommandActivityInput) bool {
		return input.Args[2] == "timeout command 2"
	})).Return(env.EnvRunCommandActivityOutput{}, errors.New("timeout error 2")).Times(1)

	s.env.ExecuteWorkflow(s.wrapperWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	// Both timeout errors should be reported
	errMsg := s.env.GetWorkflowError().Error()
	s.Contains(errMsg, "timeout error 1")
	s.Contains(errMsg, "timeout error 2")
}

func TestRunTestsTestSuite(t *testing.T) {
	suite.Run(t, new(RunTestsTestSuite))
}
