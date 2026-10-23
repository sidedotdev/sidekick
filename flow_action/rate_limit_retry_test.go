package flow_action

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/srv"
	"sidekick/srv/sqlite"
	"sidekick/utils"
)

func TestRateLimitBaseBackoff(t *testing.T) {
	t.Parallel()

	const (
		firstHourly = 6 // 1,2,4,8,16,32 minutes come first
		lastHourly  = firstHourly + 47
		firstDaily  = lastHourly + 1 + 4 // 2,4,8,16 hours in between
		lastDaily   = firstDaily + 6
	)

	tests := []struct {
		name            string
		priorRateLimits int
		want            time.Duration
		wantExhausted   bool
	}{
		{name: "starts at a minute", priorRateLimits: 0, want: time.Minute},
		{name: "doubles", priorRateLimits: 1, want: 2 * time.Minute},
		{name: "keeps doubling", priorRateLimits: 5, want: 32 * time.Minute},
		{name: "caps at an hour", priorRateLimits: firstHourly, want: time.Hour},
		{name: "stays hourly for two days", priorRateLimits: lastHourly, want: time.Hour},
		{name: "doubles again after two days hourly", priorRateLimits: lastHourly + 1, want: 2 * time.Hour},
		{name: "keeps doubling towards a day", priorRateLimits: lastHourly + 4, want: 16 * time.Hour},
		{name: "caps at a day", priorRateLimits: firstDaily, want: 24 * time.Hour},
		{name: "stays daily for a week", priorRateLimits: lastDaily, want: 24 * time.Hour},
		{name: "exhausted after a week daily", priorRateLimits: lastDaily + 1, wantExhausted: true},
		{name: "stays exhausted", priorRateLimits: 1000, wantExhausted: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := rateLimitBaseBackoff(tt.priorRateLimits)
			assert.Equal(t, !tt.wantExhausted, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRateLimitRetryWait(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		retryAfter      time.Duration
		priorRateLimits int
		random          float64
		want            time.Duration
		wantHinted      bool
		wantExhausted   bool
	}{
		{name: "no hint starts at a minute", priorRateLimits: 0, random: 0.5, want: time.Minute},
		{name: "no hint doubles", priorRateLimits: 1, random: 0.5, want: 2 * time.Minute},
		{name: "no hint jitters down", priorRateLimits: 0, random: 0, want: 45 * time.Second},
		{name: "no hint jitters up", priorRateLimits: 0, random: 1, want: 75 * time.Second},
		{name: "no hint exhausts after schedule", priorRateLimits: 65, random: 0.5, wantExhausted: true},
		{name: "hint is trusted on first rejection", retryAfter: 16 * time.Second, priorRateLimits: 0, random: 0, want: 16 * time.Second, wantHinted: true},
		{name: "hint is only ever extended", retryAfter: 16 * time.Second, priorRateLimits: 0, random: 1, want: 16*time.Second + 15*time.Minute, wantHinted: true},
		{name: "hint extension is a fixed window", retryAfter: 3 * time.Hour, priorRateLimits: 0, random: 0.5, want: 3*time.Hour + 450*time.Second, wantHinted: true},
		{name: "short hint floored by backoff after repeat", retryAfter: 17 * time.Second, priorRateLimits: 1, random: 0, want: 45 * time.Second},
		{name: "short hint floored by growing backoff", retryAfter: 17 * time.Second, priorRateLimits: 5, random: 0.5, want: 16 * time.Minute},
		{name: "extended short hint can beat a small backoff", retryAfter: 17 * time.Second, priorRateLimits: 1, random: 0.5, want: 17*time.Second + 450*time.Second, wantHinted: true},
		{name: "long hint wins over backoff after repeat", retryAfter: 3 * time.Hour, priorRateLimits: 2, random: 0, want: 3 * time.Hour, wantHinted: true},
		{name: "hint delays schedule exhaustion by one", retryAfter: 17 * time.Second, priorRateLimits: 65, random: 0.5, want: 24 * time.Hour},
		{name: "hint does not outlive the schedule", retryAfter: 17 * time.Second, priorRateLimits: 66, random: 0.5, wantExhausted: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, hinted, ok := rateLimitRetryWait(tt.retryAfter, tt.priorRateLimits, tt.random)
			assert.Equal(t, !tt.wantExhausted, ok)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantHinted, hinted)
		})
	}

	t.Run("jitter stays within bounds of the scheduled wait", func(t *testing.T) {
		t.Parallel()
		for prior := 0; ; prior++ {
			base, ok := rateLimitBaseBackoff(prior)
			if !ok {
				break
			}
			for _, random := range []float64{0, 0.25, 0.5, 0.999} {
				got, _, ok := rateLimitRetryWait(0, prior, random)
				require.True(t, ok)
				assert.LessOrEqual(t, got, scaleDuration(base, 1+rateLimitRetryJitter), "prior=%d random=%v", prior, random)
				assert.GreaterOrEqual(t, got, scaleDuration(base, 1-rateLimitRetryJitter), "prior=%d random=%v", prior, random)
			}
		}
	})
}

func TestLegacyRateLimitRetryWait(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		retryAfter      time.Duration
		priorRateLimits int
		want            time.Duration
	}{
		{name: "no hint starts at a minute", priorRateLimits: 0, want: time.Minute},
		{name: "no hint doubles", priorRateLimits: 1, want: 2 * time.Minute},
		{name: "no hint caps at an hour", priorRateLimits: 6, want: time.Hour},
		{name: "no hint stays capped forever", priorRateLimits: 70, want: time.Hour},
		{name: "hint is trusted on first rejection", retryAfter: 17 * time.Second, priorRateLimits: 0, want: 17 * time.Second},
		{name: "short hint floored by backoff after repeat", retryAfter: 17 * time.Second, priorRateLimits: 3, want: 4 * time.Minute},
		{name: "long hint wins over backoff after repeat", retryAfter: 3 * time.Hour, priorRateLimits: 2, want: 3 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, legacyRateLimitRetryWait(tt.retryAfter, tt.priorRateLimits))
		})
	}
}

// rateLimitWait is the nominal input to one automatic wait: the provider's
// hint, if any, and the scheduled backoff, if the schedule applies yet.
type rateLimitWait struct {
	hint    time.Duration
	backoff time.Duration
}

// rateLimitRetryElapsedBounds returns the range the total of the given waits
// can fall in. Each wait is the larger of its extended hint and its jittered
// backoff; legacy waits have no jitter at all.
func rateLimitRetryElapsedBounds(waits []rateLimitWait, legacy bool) (minMs, maxMs int64) {
	for _, w := range waits {
		if legacy {
			exact := max(w.hint, w.backoff).Milliseconds()
			minMs += exact
			maxMs += exact
			continue
		}
		minMs += max(w.hint, scaleDuration(w.backoff, 1-rateLimitRetryJitter)).Milliseconds()
		maxHint := w.hint
		if maxHint > 0 {
			maxHint += rateLimitHintJitterWindow
		}
		maxMs += max(maxHint, scaleDuration(w.backoff, 1+rateLimitRetryJitter)).Milliseconds()
	}
	return minMs, maxMs
}

const rateLimitRetryChildWorkflowId = "rate-limit-retry-child"

type rateLimitRetryParams struct {
	// RateLimits is the number of leading rate limit failures before success.
	RateLimits int32
	RetryAfter time.Duration
	// RetryAfterFrom is the 1-based rate limit from which RetryAfter is
	// reported; zero reports it on every rate limit.
	RetryAfterFrom int32
	// OtherFailureAfter inserts a non-rate-limit failure after this many
	// rate limits, which the user answers immediately.
	OtherFailureAfter int32
	UserRespond       bool
	// LegacySchedule replays the handler at the workflow version before the
	// jittered, exhaustible schedule.
	LegacySchedule        bool
	DisableHumanInTheLoop bool
}

// rateLimitFullSchedule is every automatic wait before exhaustion: doubling to
// an hour, two days hourly, doubling to a day, then a week daily.
func rateLimitFullSchedule() []rateLimitWait {
	var waits []rateLimitWait
	add := func(ds ...time.Duration) {
		for _, d := range ds {
			waits = append(waits, rateLimitWait{backoff: d})
		}
	}
	add(time.Minute, 2*time.Minute, 4*time.Minute, 8*time.Minute, 16*time.Minute, 32*time.Minute)
	add(slices.Repeat([]time.Duration{time.Hour}, 48)...)
	add(2*time.Hour, 4*time.Hour, 8*time.Hour, 16*time.Hour)
	add(slices.Repeat([]time.Duration{24 * time.Hour}, 7)...)
	return waits
}

type rateLimitRetryResult struct {
	Value     string
	Requests  []RequestForUser
	ElapsedMs int64
}

func rateLimitRetryChildWorkflow(ctx workflow.Context, params rateLimitRetryParams) (rateLimitRetryResult, error) {
	eCtx := newActivityRetryTestExecContext(ctx, params.DisableHumanInTheLoop)
	actionCtx := eCtx.NewActionContext(activityRetryTestActionName)
	start := workflow.Now(ctx)
	var value string
	err := PerformWithUserRetryOptions(actionCtx, UserRetryOptions{OnFailure: &RateLimitFailureHandler{}}, activityRetryTestActivity, &value)
	return rateLimitRetryResult{Value: value, ElapsedMs: workflow.Now(ctx).Sub(start).Milliseconds()}, err
}

func rateLimitRetryParentWorkflow(ctx workflow.Context, params rateLimitRetryParams) (rateLimitRetryResult, error) {
	var result rateLimitRetryResult
	requests := workflow.GetSignalChannel(ctx, SignalNameRequestForUser)
	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var req RequestForUser
			requests.Receive(ctx, &req)
			result.Requests = append(result.Requests, req)
			isRateLimit, _ := req.RequestParams["rateLimited"].(bool)
			_, autoRetries := req.RequestParams["retryAfter"]
			if params.UserRespond || !isRateLimit || !autoRetries {
				workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", UserResponseSignalName(req.FlowActionId), UserResponse{
					FlowActionId: req.FlowActionId,
				}).Get(ctx, nil)
			}
		}
	})

	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: rateLimitRetryChildWorkflowId})
	var childResult rateLimitRetryResult
	err := workflow.ExecuteChildWorkflow(childCtx, rateLimitRetryChildWorkflow, params).Get(ctx, &childResult)
	result.Value = childResult.Value
	result.ElapsedMs = childResult.ElapsedMs
	return result, err
}

func TestRateLimitFailureHandler(t *testing.T) {
	t.Parallel()

	minutes := func(ns ...int) []time.Duration {
		ds := make([]time.Duration, len(ns))
		for i, n := range ns {
			ds[i] = time.Duration(n) * time.Minute
		}
		return ds
	}
	backoffs := func(ds ...time.Duration) []rateLimitWait {
		waits := make([]rateLimitWait, len(ds))
		for i, d := range ds {
			waits[i] = rateLimitWait{backoff: d}
		}
		return waits
	}
	hintEach := func(hint time.Duration, waits []rateLimitWait) []rateLimitWait {
		hinted := make([]rateLimitWait, len(waits))
		for i, w := range waits {
			hinted[i] = rateLimitWait{hint: hint, backoff: w.backoff}
		}
		return hinted
	}
	fullSchedule := rateLimitFullSchedule()

	tests := []struct {
		name         string
		params       rateLimitRetryParams
		wantCalls    int32
		wantRequests int
		wantWaits    []rateLimitWait
		// wantExhaustedRequests is how many trailing requests should offer
		// no automatic retry.
		wantExhaustedRequests int
		// wantOfferedRetryAfters are the exact waits offered per request,
		// for schedules without jitter.
		wantOfferedRetryAfters []time.Duration
		// wantResetHinted pins whether given requests (by index) claim the
		// provider's hint set the wait; requests without a hint are always
		// checked to not claim it.
		wantResetHinted map[int]bool
	}{
		{
			name:            "a repeated short hint stops being presented as the reset time once backoff dominates",
			params:          rateLimitRetryParams{RateLimits: 6, RetryAfter: 17 * time.Second},
			wantCalls:       7,
			wantRequests:    6,
			wantWaits:       hintEach(17*time.Second, append([]rateLimitWait{{}}, backoffs(minutes(1, 2, 4, 8, 16)...)...)),
			wantResetHinted: map[int]bool{0: true, 5: false},
		},
		{
			name:         "automatic retries back off without user input",
			params:       rateLimitRetryParams{RateLimits: 3},
			wantCalls:    4,
			wantRequests: 3,
			wantWaits:    backoffs(minutes(1, 2, 4)...),
		},
		{
			name:         "provider hint used for the first wait then floored",
			params:       rateLimitRetryParams{RateLimits: 2, RetryAfter: 10 * time.Second},
			wantCalls:    3,
			wantRequests: 2,
			wantWaits:    []rateLimitWait{{hint: 10 * time.Second}, {hint: 10 * time.Second, backoff: time.Minute}},
		},
		{
			name:         "user retry wins the race",
			params:       rateLimitRetryParams{RateLimits: 2, UserRespond: true},
			wantCalls:    3,
			wantRequests: 2,
		},
		{
			name:         "other failure resets the backoff",
			params:       rateLimitRetryParams{RateLimits: 3, OtherFailureAfter: 2},
			wantCalls:    5,
			wantRequests: 4,
			wantWaits:    backoffs(minutes(1, 2, 1)...),
		},
		{
			name:                  "automatic retries stop after the schedule is exhausted",
			params:                rateLimitRetryParams{RateLimits: int32(len(fullSchedule)) + 2},
			wantCalls:             int32(len(fullSchedule)) + 3,
			wantRequests:          len(fullSchedule) + 2,
			wantWaits:             fullSchedule,
			wantExhaustedRequests: 2,
		},
		{
			name:                  "hinted runs get the hint plus the full schedule before exhausting",
			params:                rateLimitRetryParams{RateLimits: int32(len(fullSchedule)) + 3, RetryAfter: 10 * time.Second},
			wantCalls:             int32(len(fullSchedule)) + 4,
			wantRequests:          len(fullSchedule) + 3,
			wantWaits:             hintEach(10*time.Second, append([]rateLimitWait{{}}, fullSchedule...)),
			wantExhaustedRequests: 2,
		},
		{
			name: "a hint first appearing at the end of the schedule does not extend it",
			params: rateLimitRetryParams{
				RateLimits:     int32(len(fullSchedule)) + 2,
				RetryAfter:     10 * time.Second,
				RetryAfterFrom: int32(len(fullSchedule)) + 1,
			},
			wantCalls:             int32(len(fullSchedule)) + 3,
			wantRequests:          len(fullSchedule) + 2,
			wantWaits:             fullSchedule,
			wantExhaustedRequests: 2,
		},
		{
			name: "a hint after a manual retry does not revive exhausted automatic retries",
			params: rateLimitRetryParams{
				RateLimits:     int32(len(fullSchedule)) + 2,
				RetryAfter:     10 * time.Second,
				RetryAfterFrom: int32(len(fullSchedule)) + 2,
			},
			wantCalls:             int32(len(fullSchedule)) + 3,
			wantRequests:          len(fullSchedule) + 2,
			wantWaits:             fullSchedule,
			wantExhaustedRequests: 2,
		},
		{
			name:         "manual retries do not advance or exhaust the schedule",
			params:       rateLimitRetryParams{RateLimits: int32(len(fullSchedule)) + 5, UserRespond: true},
			wantCalls:    int32(len(fullSchedule)) + 6,
			wantRequests: len(fullSchedule) + 5,
		},
		{
			name:                   "legacy schedule waits without jitter and caps hourly",
			params:                 rateLimitRetryParams{RateLimits: 8, LegacySchedule: true},
			wantCalls:              9,
			wantRequests:           8,
			wantWaits:              backoffs(minutes(1, 2, 4, 8, 16, 32, 60, 60)...),
			wantOfferedRetryAfters: minutes(1, 2, 4, 8, 16, 32, 60, 60),
		},
		{
			name:                   "legacy schedule advances on manual retries",
			params:                 rateLimitRetryParams{RateLimits: 3, UserRespond: true, LegacySchedule: true},
			wantCalls:              4,
			wantRequests:           3,
			wantOfferedRetryAfters: minutes(1, 2, 4),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			storage := sqlite.NewTestSqliteStorage(t, "rate-limit-retry")
			service := srv.NewDelegator(storage, srv.NewMemoryStreamer())
			require.NoError(t, storage.PersistFlow(context.Background(), domain.Flow{
				WorkspaceId: "ws_test",
				Id:          rateLimitRetryChildWorkflowId,
				Status:      "in_progress",
			}))

			suite := &testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())
			env.SetTestTimeout(10 * time.Second)
			env.RegisterWorkflow(rateLimitRetryChildWorkflow)
			env.RegisterWorkflow(rateLimitRetryParentWorkflow)
			env.RegisterActivity(&FlowActivities{Service: service})
			env.RegisterActivity(&srv.Activities{Service: service})
			if tt.params.LegacySchedule {
				env.OnGetVersion(rateLimitRetryScheduleChangeId, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
			}

			var calls, rateLimits int32
			env.OnActivity(activityRetryTestActivity, mock.Anything).Return(
				func(ctx context.Context) (string, error) {
					atomic.AddInt32(&calls, 1)
					if tt.params.OtherFailureAfter > 0 && atomic.LoadInt32(&rateLimits) == tt.params.OtherFailureAfter && atomic.LoadInt32(&calls) == tt.params.OtherFailureAfter+1 {
						return "", errors.New("simulated failure")
					}
					if atomic.LoadInt32(&rateLimits) < tt.params.RateLimits {
						rateLimit := atomic.AddInt32(&rateLimits, 1)
						var retryAfter time.Duration
						if rateLimit >= tt.params.RetryAfterFrom {
							retryAfter = tt.params.RetryAfter
						}
						return "", common.NewProviderRateLimitError(common.ProviderRateLimitErrorDetails{
							Provider:   "anthropic",
							RetryAfter: retryAfter,
						}, errors.New("429 Too Many Requests"))
					}
					return "success", nil
				},
			)

			env.ExecuteWorkflow(rateLimitRetryParentWorkflow, tt.params)
			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())

			var result rateLimitRetryResult
			require.NoError(t, env.GetWorkflowResult(&result))

			assert.Equal(t, "success", result.Value)
			assert.Equal(t, tt.wantCalls, atomic.LoadInt32(&calls))
			minElapsedMs, maxElapsedMs := rateLimitRetryElapsedBounds(tt.wantWaits, tt.params.LegacySchedule)
			assert.GreaterOrEqual(t, result.ElapsedMs, minElapsedMs)
			assert.LessOrEqual(t, result.ElapsedMs, maxElapsedMs)
			require.Len(t, result.Requests, tt.wantRequests)
			for i, req := range result.Requests {
				assert.Equal(t, RequestKindContinue, req.RequestKind)
				assert.Equal(t, "try_again", req.RequestParams["continueTag"])
				rateLimited, _ := req.RequestParams["rateLimited"].(bool)
				if !rateLimited {
					continue
				}
				assert.Contains(t, req.Content, "limit")
				assert.Contains(t, req.Content, "429 Too Many Requests")
				retryAfterParam, autoRetries := req.RequestParams["retryAfter"]
				exhausted := i >= len(result.Requests)-tt.wantExhaustedRequests
				assert.Equal(t, !exhausted, autoRetries, "request %d", i)
				if exhausted {
					assert.Contains(t, req.Content, "over a week")
					assert.NotContains(t, req.RequestParams, "retryAt")
					continue
				}
				offered, err := time.ParseDuration(retryAfterParam.(string))
				require.NoError(t, err)
				if !tt.params.LegacySchedule {
					retryAt, err := time.Parse(time.RFC3339, req.RequestParams["retryAt"].(string))
					require.NoError(t, err, "request %d", i)
					assert.False(t, retryAt.IsZero())
					hintReported := tt.params.RetryAfter > 0 && int32(i+1) >= tt.params.RetryAfterFrom
					if want, pinned := tt.wantResetHinted[i]; pinned {
						assert.Equal(t, want, req.RequestParams["resetHinted"], "request %d", i)
					} else if !hintReported {
						assert.Equal(t, false, req.RequestParams["resetHinted"], "request %d", i)
					}
				}
				if tt.wantOfferedRetryAfters != nil {
					assert.Equal(t, tt.wantOfferedRetryAfters[i], offered, "request %d", i)
				} else if tt.params.UserRespond && tt.params.RetryAfter == 0 {
					assert.LessOrEqual(t, offered, scaleDuration(rateLimitRetryInitialBackoff, 1+rateLimitRetryJitter), "request %d: manual retries must not advance the schedule", i)
				}
			}

			flow, err := storage.GetFlow(context.Background(), "ws_test", rateLimitRetryChildWorkflowId)
			require.NoError(t, err)
			if !tt.params.UserRespond && tt.params.OtherFailureAfter == 0 && tt.wantExhaustedRequests == 0 {
				assert.Equal(t, "in_progress", flow.Status, "timer-resolved requests must resume the flow")
			}
		})
	}
}

type rateLimitExhaustionResult struct {
	Retries   int
	Decision  RetryDecision
	ElapsedMs int64
}

// rateLimitExhaustionWorkflow feeds the handler the same rate limit error
// until it stops asking for a retry, standing in for a retry loop that keeps
// going without a human.
func rateLimitExhaustionWorkflow(ctx workflow.Context, disableHumanInTheLoop bool) (rateLimitExhaustionResult, error) {
	eCtx := newActivityRetryTestExecContext(ctx, disableHumanInTheLoop)
	handler := &RateLimitFailureHandler{}
	activityErr := common.NewProviderRateLimitError(common.ProviderRateLimitErrorDetails{Provider: "anthropic"}, errors.New("429 Too Many Requests"))
	maxRetries := 2 * len(rateLimitFullSchedule())

	start := workflow.Now(ctx)
	var result rateLimitExhaustionResult
	for {
		decision, err := handler.HandleActivityFailure(eCtx, activityRetryTestActionName, activityErr)
		if err != nil {
			return result, err
		}
		result.Decision = decision
		result.ElapsedMs = workflow.Now(ctx).Sub(start).Milliseconds()
		if decision != RetryDecisionRetry {
			return result, nil
		}
		result.Retries++
		if result.Retries > maxRetries {
			return result, errors.New("handler never stopped retrying")
		}
	}
}

// Without a human to authorize further attempts, a limit that never resets
// must end the action with its rate limit error once the automatic schedule
// is spent, rather than hammering the provider.
func TestRateLimitFailureHandler_NoHumanInTheLoopFailsWhenExhausted(t *testing.T) {
	t.Parallel()

	fullSchedule := rateLimitFullSchedule()
	tests := []struct {
		name          string
		legacyExhaust bool
		wantDecision  RetryDecision
		wantRetries   int
	}{
		{name: "fails once the schedule is exhausted", wantDecision: RetryDecisionFail, wantRetries: len(fullSchedule)},
		// Replaying histories recorded before the terminal guard must keep
		// their immediate retries; the workflow stops them here.
		{name: "legacy histories keep retrying", legacyExhaust: true, wantDecision: RetryDecisionRetry, wantRetries: 2*len(fullSchedule) + 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			suite := &testsuite.WorkflowTestSuite{}
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())
			env.SetTestTimeout(10 * time.Second)
			env.RegisterWorkflow(rateLimitExhaustionWorkflow)
			if tt.legacyExhaust {
				env.OnGetVersion(rateLimitExhaustedFailChangeId, workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
			}

			env.ExecuteWorkflow(rateLimitExhaustionWorkflow, true)
			require.True(t, env.IsWorkflowCompleted())

			var result rateLimitExhaustionResult
			if tt.legacyExhaust {
				require.ErrorContains(t, env.GetWorkflowError(), "handler never stopped retrying")
				return
			}
			require.NoError(t, env.GetWorkflowError())
			require.NoError(t, env.GetWorkflowResult(&result))
			assert.Equal(t, tt.wantDecision, result.Decision)
			assert.Equal(t, tt.wantRetries, result.Retries, "one automatic retry per scheduled wait")
			minElapsedMs, maxElapsedMs := rateLimitRetryElapsedBounds(fullSchedule, false)
			assert.GreaterOrEqual(t, result.ElapsedMs, minElapsedMs)
			assert.LessOrEqual(t, result.ElapsedMs, maxElapsedMs)
		})
	}
}

// The retry loop itself never consults the handler without a human in the
// loop: a rate limited activity fails on its first attempt.
func TestRateLimitFailureHandler_NoHumanInTheLoopLoopStopsAfterFirstAttempt(t *testing.T) {
	t.Parallel()

	storage := sqlite.NewTestSqliteStorage(t, "rate-limit-retry-no-human")
	service := srv.NewDelegator(storage, srv.NewMemoryStreamer())
	require.NoError(t, storage.PersistFlow(context.Background(), domain.Flow{
		WorkspaceId: "ws_test",
		Id:          rateLimitRetryChildWorkflowId,
		Status:      "in_progress",
	}))

	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.SetTestTimeout(10 * time.Second)
	env.RegisterWorkflow(rateLimitRetryChildWorkflow)
	env.RegisterWorkflow(rateLimitRetryParentWorkflow)
	env.RegisterActivity(&FlowActivities{Service: service})
	env.RegisterActivity(&srv.Activities{Service: service})

	var calls int32
	env.OnActivity(activityRetryTestActivity, mock.Anything).Return(func(ctx context.Context) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "", common.NewProviderRateLimitError(common.ProviderRateLimitErrorDetails{Provider: "anthropic"}, errors.New("429 Too Many Requests"))
	})

	env.ExecuteWorkflow(rateLimitRetryParentWorkflow, rateLimitRetryParams{DisableHumanInTheLoop: true})
	require.True(t, env.IsWorkflowCompleted())

	err := env.GetWorkflowError()
	require.Error(t, err)
	details, ok := common.AsProviderRateLimitError(err)
	require.True(t, ok, "action must end with the provider rate limit error, got: %v", err)
	assert.Equal(t, "anthropic", details.Provider)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))

	flowActions, err := storage.GetFlowActions(context.Background(), "ws_test", rateLimitRetryChildWorkflowId)
	require.NoError(t, err)
	for _, fa := range flowActions {
		assert.False(t, fa.IsHumanAction, "no user request may be raised without a human in the loop: %s", fa.ActionType)
	}
}
