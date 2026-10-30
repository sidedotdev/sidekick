package env

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type fakeEnvTracker struct {
	switching bool
	current   EnvContainer
	lineage   []EnvIdentity
}

func (f *fakeEnvTracker) SwitchInProgress() bool { return f.switching }

func (f *fakeEnvTracker) Resolve(c EnvContainer) EnvContainer {
	if slices.Contains(f.lineage, IdentityOf(c)) {
		return f.current
	}
	return c
}

type gateTestInput struct {
	EnvContainer EnvContainer
	Label        string
}

type gateTestPointerInput struct {
	EnvContainer *EnvContainer
}

const gateTestLabel = "kept"

type gateTestActivities struct {
	calls atomic.Int32
}

func describeEnv(c EnvContainer) string {
	if c.Env == nil {
		return ""
	}
	return string(c.Env.GetType()) + ":" + c.Env.GetWorkingDirectory()
}

func (a *gateTestActivities) EnvValue(_ context.Context, c EnvContainer) (string, error) {
	a.calls.Add(1)
	return describeEnv(c), nil
}

func (a *gateTestActivities) EnvPointer(_ context.Context, c *EnvContainer) (string, error) {
	a.calls.Add(1)
	return describeEnv(*c), nil
}

func (a *gateTestActivities) EnvField(_ context.Context, in gateTestInput) (string, error) {
	a.calls.Add(1)
	if in.Label != gateTestLabel {
		return "", errors.New("non-env field was not preserved")
	}
	return describeEnv(in.EnvContainer), nil
}

func (a *gateTestActivities) EnvFieldPointer(_ context.Context, in *gateTestInput) (string, error) {
	a.calls.Add(1)
	if in.Label != gateTestLabel {
		return "", errors.New("non-env field was not preserved")
	}
	return describeEnv(in.EnvContainer), nil
}

func (a *gateTestActivities) EnvPointerField(_ context.Context, in gateTestPointerInput) (string, error) {
	a.calls.Add(1)
	return describeEnv(*in.EnvContainer), nil
}

func (a *gateTestActivities) NoEnv(_ context.Context, s string) (string, error) {
	a.calls.Add(1)
	return s, nil
}

// gateTestCallerArgs are the caller-owned values activity inputs are built
// from, so tests can assert the gate never mutates them.
type gateTestCallerArgs struct {
	container    EnvContainer
	input        gateTestInput
	pointerInput gateTestPointerInput
}

type gateTestResult struct {
	Ran                     string
	ScheduledWhileSwitching bool
	CallerUnchanged         bool
}

var (
	gateTestOldEnv     = EnvContainer{Env: &LocalGitWorktreeEnv{WorkingDirectory: "/repo-worktrees/flow"}}
	gateTestNewEnv     = EnvContainer{Env: &DevPodEnv{WorkingDirectory: "/workspaces/repo-flow", WorkspaceName: "repo"}}
	gateTestForeignEnv = EnvContainer{Env: &LocalEnv{WorkingDirectory: "/repo"}}
)

func newGateTestEnv(t *testing.T, acts *gateTestActivities) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	suite := &testsuite.WorkflowTestSuite{}
	wfEnv := suite.NewTestWorkflowEnvironment()
	wfEnv.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{NewEnvSwitchGateInterceptor()},
	})
	wfEnv.RegisterActivity(acts)
	return wfEnv
}

func TestEnvSwitchGate(t *testing.T) {
	t.Parallel()

	type scheduleFn func(ctx workflow.Context, acts *gateTestActivities, args *gateTestCallerArgs) workflow.Future
	scheduleValue := func(ctx workflow.Context, acts *gateTestActivities, args *gateTestCallerArgs) workflow.Future {
		return workflow.ExecuteActivity(ctx, acts.EnvValue, args.container)
	}

	cases := []struct {
		name           string
		callerEnv      EnvContainer
		withoutTracker bool
		markCtx        func(workflow.Context) workflow.Context
		schedule       scheduleFn
		wantGated      bool
		wantRan        string
	}{
		{
			name:      "container value is held and resolved",
			callerEnv: gateTestOldEnv,
			schedule:  scheduleValue,
			wantGated: true,
			wantRan:   describeEnv(gateTestNewEnv),
		},
		{
			name:      "container pointer is held and resolved",
			callerEnv: gateTestOldEnv,
			schedule: func(ctx workflow.Context, acts *gateTestActivities, args *gateTestCallerArgs) workflow.Future {
				return workflow.ExecuteActivity(ctx, acts.EnvPointer, &args.container)
			},
			wantGated: true,
			wantRan:   describeEnv(gateTestNewEnv),
		},
		{
			name:      "struct field is held and resolved",
			callerEnv: gateTestOldEnv,
			schedule: func(ctx workflow.Context, acts *gateTestActivities, args *gateTestCallerArgs) workflow.Future {
				return workflow.ExecuteActivity(ctx, acts.EnvField, args.input)
			},
			wantGated: true,
			wantRan:   describeEnv(gateTestNewEnv),
		},
		{
			name:      "struct pointer field is held and resolved",
			callerEnv: gateTestOldEnv,
			schedule: func(ctx workflow.Context, acts *gateTestActivities, args *gateTestCallerArgs) workflow.Future {
				return workflow.ExecuteActivity(ctx, acts.EnvFieldPointer, &args.input)
			},
			wantGated: true,
			wantRan:   describeEnv(gateTestNewEnv),
		},
		{
			name:      "struct field holding a container pointer is held and resolved",
			callerEnv: gateTestOldEnv,
			schedule: func(ctx workflow.Context, acts *gateTestActivities, args *gateTestCallerArgs) workflow.Future {
				return workflow.ExecuteActivity(ctx, acts.EnvPointerField, args.pointerInput)
			},
			wantGated: true,
			wantRan:   describeEnv(gateTestNewEnv),
		},
		{
			name:      "container outside the lineage is held but not rewritten",
			callerEnv: gateTestForeignEnv,
			schedule:  scheduleValue,
			wantGated: true,
			wantRan:   describeEnv(gateTestForeignEnv),
		},
		{
			name:      "pinned ctx passes through untouched",
			callerEnv: gateTestOldEnv,
			markCtx:   WithPinnedEnv,
			schedule:  scheduleValue,
			wantRan:   describeEnv(gateTestOldEnv),
		},
		{
			name:      "switch bypass ctx passes through untouched",
			callerEnv: gateTestOldEnv,
			markCtx:   WithSwitchBypass,
			schedule:  scheduleValue,
			wantRan:   describeEnv(gateTestOldEnv),
		},
		{
			name:           "ctx without a tracker passes through untouched",
			callerEnv:      gateTestOldEnv,
			withoutTracker: true,
			schedule:       scheduleValue,
			wantRan:        describeEnv(gateTestOldEnv),
		},
		{
			name:      "activity without an env is not held",
			callerEnv: gateTestOldEnv,
			schedule: func(ctx workflow.Context, acts *gateTestActivities, _ *gateTestCallerArgs) workflow.Future {
				return workflow.ExecuteActivity(ctx, acts.NoEnv, "plain")
			},
			wantRan: "plain",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			acts := &gateTestActivities{}
			wfEnv := newGateTestEnv(t, acts)

			wfEnv.ExecuteWorkflow(func(ctx workflow.Context) (gateTestResult, error) {
				ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
				tracker := &fakeEnvTracker{
					switching: true,
					current:   gateTestOldEnv,
					lineage:   []EnvIdentity{IdentityOf(gateTestOldEnv)},
				}
				if !tc.withoutTracker {
					ctx = WithEnvTracker(ctx, tracker)
				}
				if tc.markCtx != nil {
					ctx = tc.markCtx(ctx)
				}
				workflow.Go(ctx, func(gCtx workflow.Context) {
					_ = workflow.Sleep(gCtx, time.Minute)
					tracker.current = gateTestNewEnv
					tracker.lineage = append(tracker.lineage, IdentityOf(gateTestNewEnv))
					tracker.switching = false
				})

				args := &gateTestCallerArgs{
					container: tc.callerEnv,
					input:     gateTestInput{EnvContainer: tc.callerEnv, Label: gateTestLabel},
				}
				args.pointerInput = gateTestPointerInput{EnvContainer: &args.container}

				future := tc.schedule(ctx, acts, args)
				result := gateTestResult{ScheduledWhileSwitching: tracker.switching}
				if err := future.Get(ctx, &result.Ran); err != nil {
					return gateTestResult{}, err
				}
				result.CallerUnchanged = describeEnv(args.container) == describeEnv(tc.callerEnv) &&
					describeEnv(args.input.EnvContainer) == describeEnv(tc.callerEnv) &&
					args.pointerInput.EnvContainer == &args.container
				return result, nil
			})

			require.True(t, wfEnv.IsWorkflowCompleted())
			require.NoError(t, wfEnv.GetWorkflowError())
			var result gateTestResult
			require.NoError(t, wfEnv.GetWorkflowResult(&result))
			require.Equal(t, tc.wantRan, result.Ran)
			require.Equal(t, !tc.wantGated, result.ScheduledWhileSwitching)
			require.True(t, result.CallerUnchanged, "caller-owned activity args must not be mutated")
			require.EqualValues(t, 1, acts.calls.Load())
		})
	}
}

func TestEnvSwitchGateCancellationFailsWithoutScheduling(t *testing.T) {
	t.Parallel()
	acts := &gateTestActivities{}
	wfEnv := newGateTestEnv(t, acts)

	wfEnv.ExecuteWorkflow(func(ctx workflow.Context) (bool, error) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
		tracker := &fakeEnvTracker{
			switching: true,
			current:   gateTestOldEnv,
			lineage:   []EnvIdentity{IdentityOf(gateTestOldEnv)},
		}
		gatedCtx, cancel := workflow.WithCancel(WithEnvTracker(ctx, tracker))
		workflow.Go(ctx, func(gCtx workflow.Context) {
			_ = workflow.Sleep(gCtx, time.Minute)
			cancel()
		})

		err := workflow.ExecuteActivity(gatedCtx, acts.EnvValue, gateTestOldEnv).Get(ctx, nil)
		return temporal.IsCanceledError(err), nil
	})

	require.True(t, wfEnv.IsWorkflowCompleted())
	require.NoError(t, wfEnv.GetWorkflowError())
	var canceled bool
	require.NoError(t, wfEnv.GetWorkflowResult(&canceled))
	require.True(t, canceled)
	require.Zero(t, acts.calls.Load())
}

func TestExpectedEnvGuard(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"unchanged", "switching", "directory", "replacement", "mutated configuration"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			acts := &gateTestActivities{}
			wfEnv := newGateTestEnv(t, acts)
			wfEnv.ExecuteWorkflow(func(ctx workflow.Context) (bool, error) {
				ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
				current := EnvContainer{Env: &LocalEnv{WorkingDirectory: "/original"}}
				if scenario == "replacement" {
					current = EnvContainer{Env: &DevPodEnv{WorkingDirectory: "/workspace", WorkspaceName: "old"}}
				}
				tracker := &fakeEnvTracker{current: current, lineage: []EnvIdentity{IdentityOf(current)}}
				ctx = WithEnvTracker(ctx, tracker)
				guarded, err := WithExpectedEnv(ctx, current)
				if err != nil {
					return false, err
				}
				switch scenario {
				case "switching":
					tracker.switching = true
				case "directory":
					tracker.current = EnvContainer{Env: &LocalEnv{WorkingDirectory: "/other"}}
				case "replacement":
					tracker.current = EnvContainer{Env: &DevPodEnv{WorkingDirectory: "/workspace", WorkspaceName: "replacement"}}
				case "mutated configuration":
					current.Env.(*LocalEnv).Hibernated = true
				}
				future := workflow.ExecuteActivity(guarded, acts.EnvValue, current)
				if scenario != "unchanged" && !future.IsReady() {
					return false, errors.New("stale dispatch was not rejected synchronously")
				}
				err = future.Get(ctx, nil)
				if scenario == "unchanged" {
					return err == nil, err
				}
				return errors.Is(err, ErrEnvChanged), nil
			})
			require.NoError(t, wfEnv.GetWorkflowError())
			var ok bool
			require.NoError(t, wfEnv.GetWorkflowResult(&ok))
			require.True(t, ok)
			if scenario == "unchanged" {
				require.EqualValues(t, 1, acts.calls.Load())
			} else {
				require.Zero(t, acts.calls.Load())
			}
		})
	}
}

func TestExpectedEnvDispatchDoesNotYield(t *testing.T) {
	t.Parallel()
	acts := &gateTestActivities{}
	wfEnv := newGateTestEnv(t, acts)
	wfEnv.ExecuteWorkflow(func(ctx workflow.Context) (bool, error) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
		tracker := &fakeEnvTracker{current: gateTestOldEnv, lineage: []EnvIdentity{IdentityOf(gateTestOldEnv)}}
		ctx = WithEnvTracker(ctx, tracker)
		guarded, err := WithExpectedEnv(ctx, gateTestOldEnv)
		if err != nil {
			return false, err
		}
		workflow.Go(ctx, func(workflow.Context) {
			tracker.switching = true
		})
		future := workflow.ExecuteActivity(guarded, acts.EnvValue, gateTestOldEnv)
		scheduledBeforeSwitch := !tracker.switching
		return scheduledBeforeSwitch, future.Get(ctx, nil)
	})
	require.NoError(t, wfEnv.GetWorkflowError())
	var scheduledBeforeSwitch bool
	require.NoError(t, wfEnv.GetWorkflowResult(&scheduledBeforeSwitch))
	require.True(t, scheduledBeforeSwitch)
	require.EqualValues(t, 1, acts.calls.Load())
}
