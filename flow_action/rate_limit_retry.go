package flow_action

import (
	"fmt"
	"math/rand"
	"sidekick/common"
	"time"

	"go.temporal.io/sdk/workflow"
)

const (
	rateLimitRetryInitialBackoff = time.Minute

	// rateLimitRetryJitter bounds the random scaling of each scheduled wait,
	// so flows that were rate limited together don't all retry together.
	rateLimitRetryJitter = 0.25

	// rateLimitHintJitterWindow is the random extra wait added on top of a
	// provider's reset hint, since flows sharing an account also share the
	// reset time.
	rateLimitHintJitterWindow = 15 * time.Minute

	// rateLimitRetryScheduleChangeId gates the jittered, exhaustible schedule.
	// Earlier histories keep the legacy hourly-capped schedule.
	rateLimitRetryScheduleChangeId = "rate-limit-retry-schedule"

	// rateLimitExhaustedFailChangeId gates failing the action once automatic
	// retries are exhausted and no human can authorize more. Earlier
	// histories recorded an immediate retry there.
	rateLimitExhaustedFailChangeId = "rate-limit-exhausted-fail"
)

// rateLimitBackoffPlateaus lists the ceilings the doubling backoff pauses at
// and how many automatic retries to spend at each before doubling resumes.
// Automatic retries stop once the last plateau is used up.
var rateLimitBackoffPlateaus = []struct {
	ceiling time.Duration
	retries int
}{
	{ceiling: time.Hour, retries: 48},     // two days hourly
	{ceiling: 24 * time.Hour, retries: 7}, // a week daily
}

// RateLimitFailureHandler races a scheduled automatic retry against the
// user's "Try Again" when an activity fails with a provider rate limit error.
// The automatic retry waits for the provider's own hint when it gives one and
// otherwise backs off exponentially per rateLimitBackoffPlateaus, with jitter.
// Once automatic retries are exhausted, only the user can retry. Other
// failures go to Fallback. A handler instance tracks the current run of rate
// limits, so use one per retry loop.
type RateLimitFailureHandler struct {
	// Fallback handles failures that aren't rate limits. Defaults to
	// ContinuePromptFailureHandler.
	Fallback ActivityFailureHandler

	// backoffStep is how far along the schedule the current run of rate
	// limits is. It only advances when a scheduled wait runs to completion,
	// since manual retries give the limit no time to reset.
	backoffStep int

	// exhausted sticks for the rest of the run once the schedule is used up,
	// so a later error that happens to carry a hint can't restart automation.
	exhausted bool
}

func (h *RateLimitFailureHandler) HandleActivityFailure(eCtx ExecContext, actionName string, activityErr error) (RetryDecision, error) {
	details, ok := common.AsProviderRateLimitError(activityErr)
	if !ok {
		h.backoffStep = 0
		h.exhausted = false
		fallback := h.Fallback
		if fallback == nil {
			fallback = ContinuePromptFailureHandler{}
		}
		return fallback.HandleActivityFailure(eCtx, actionName, activityErr)
	}

	if workflow.GetVersion(eCtx, rateLimitRetryScheduleChangeId, workflow.DefaultVersion, 1) == workflow.DefaultVersion {
		return h.handleLegacy(eCtx, actionName, details, activityErr)
	}

	var random float64
	if err := workflow.SideEffect(eCtx, func(ctx workflow.Context) interface{} {
		return rand.Float64()
	}).Get(&random); err != nil {
		return RetryDecisionFail, err
	}
	wait, resetHinted, autoRetry := rateLimitRetryWait(details.RetryAfter, h.backoffStep, random)
	if !autoRetry {
		h.exhausted = true
	}

	requestParams := map[string]any{
		"continueTag": "try_again",
		"rateLimited": true,
	}
	if h.exhausted {
		if eCtx.DisableHumanInTheLoop && workflow.GetVersion(eCtx, rateLimitExhaustedFailChangeId, workflow.DefaultVersion, 1) == 1 {
			return RetryDecisionFail, nil
		}
		prompt := fmt.Sprintf(
			"You've reached a usage limit and it hasn't reset after retrying automatically for over a week. Try again once it resets.\n\n```\n%s\n```",
			activityErr.Error(),
		)
		if err := GetUserContinue(eCtx, prompt, requestParams); err != nil {
			return RetryDecisionFail, err
		}
		return RetryDecisionRetry, nil
	}

	retryAt := workflow.Now(eCtx).Add(wait).UTC()
	if workflow.GetVersion(eCtx, retryAttemptsChangeId, workflow.DefaultVersion, 1) == 1 {
		if err := scheduleAutomaticRetry(eCtx, retryAt); err != nil {
			return RetryDecisionFail, err
		}
	}

	prompt := fmt.Sprintf(
		"You've reached a usage limit. We'll automatically continue in about %s, or you can try again now.\n\n```\n%s\n```",
		wait.Round(time.Second), activityErr.Error(),
	)
	requestParams["retryAfter"] = wait.String()
	requestParams["retryAt"] = retryAt.Format(time.RFC3339)
	// Lets the UI say when the limit resets, rather than merely when we'll
	// next try, only when the provider's hint actually set the wait.
	requestParams["resetHinted"] = resetHinted
	userAnswered, err := GetUserContinueOrTimeout(eCtx, prompt, requestParams, wait)
	if err != nil {
		return RetryDecisionFail, err
	}
	if workflow.GetVersion(eCtx, retryAttemptsChangeId, workflow.DefaultVersion, 1) == 1 {
		if err := finishScheduledRetry(eCtx); err != nil {
			return RetryDecisionFail, err
		}
	}
	if !userAnswered {
		h.backoffStep++
		// Latch at the end of the schedule as this run has experienced it,
		// so a hint that first appears afterwards can't shift it longer.
		if _, _, more := rateLimitRetryWait(details.RetryAfter, h.backoffStep, 0); !more {
			h.exhausted = true
		}
	}
	return RetryDecisionRetry, nil
}

// handleLegacy is the schedule from before rateLimitRetryScheduleChangeId:
// unjittered doubling from a minute, capped at an hour forever, advancing on
// every rate limit including manual retries.
func (h *RateLimitFailureHandler) handleLegacy(eCtx ExecContext, actionName string, details *common.ProviderRateLimitErrorDetails, activityErr error) (RetryDecision, error) {
	wait := legacyRateLimitRetryWait(details.RetryAfter, h.backoffStep)
	h.backoffStep++

	prompt := fmt.Sprintf(
		"%s failed because %s rate limits were exceeded. Retrying automatically in %s, or try again now.\n\n```\n%s\n```",
		actionName, details.Provider, wait.Round(time.Second), activityErr.Error(),
	)
	requestParams := map[string]any{
		"continueTag": "try_again",
		"rateLimited": true,
		"retryAfter":  wait.String(),
	}
	if _, err := GetUserContinueOrTimeout(eCtx, prompt, requestParams, wait); err != nil {
		return RetryDecisionFail, err
	}
	return RetryDecisionRetry, nil
}

// rateLimitRetryWait returns the wait before the next automatic retry, or
// false once the schedule's automatic retries are used up. random must be
// uniform in [0, 1). The provider's hint is trusted on its own for a first
// rejection. Repeated rejections mean the hint underestimates the limit (e.g.
// a spend cap that reports a per-minute reset), so the scheduled backoff takes
// over with the hint as a floor, advancing one step per prior automatic retry.
// The hint is only ever extended, never shortened, so we don't retry before
// the provider says we can.
//
// hintDominates reports that the provider's hint set the wait, so the wait
// approximates when the limit actually resets rather than being our guess.
func rateLimitRetryWait(retryAfter time.Duration, priorAutomaticRetries int, random float64) (wait time.Duration, hintDominates bool, autoRetry bool) {
	var hinted time.Duration
	if retryAfter > 0 {
		hinted = retryAfter + scaleDuration(rateLimitHintJitterWindow, random)
		if priorAutomaticRetries == 0 {
			return hinted, true, true
		}
		priorAutomaticRetries--
	}
	backoff, ok := rateLimitBaseBackoff(priorAutomaticRetries)
	if !ok {
		return 0, false, false
	}
	jittered := scaleDuration(backoff, 1-rateLimitRetryJitter+2*rateLimitRetryJitter*random)
	if hinted >= jittered {
		return hinted, true, true
	}
	return jittered, false, true
}

func legacyRateLimitRetryWait(retryAfter time.Duration, priorConsecutive int) time.Duration {
	backoffSteps := priorConsecutive
	if retryAfter > 0 {
		backoffSteps--
	}
	if backoffSteps < 0 {
		return retryAfter
	}
	backoff := time.Hour
	// Shifting past the cap would overflow long before the loop could get
	// there, so only compute below it.
	if backoffSteps < 10 {
		backoff = min(rateLimitRetryInitialBackoff<<backoffSteps, time.Hour)
	}
	return max(retryAfter, backoff)
}

// rateLimitBaseBackoff returns the pre-jitter wait for the automatic retry
// after the given number of prior automatic retries, or false once the
// schedule is exhausted. Waits double from rateLimitRetryInitialBackoff,
// pausing at each plateau's ceiling for its retry count.
func rateLimitBaseBackoff(priorAutomaticRetries int) (time.Duration, bool) {
	backoff := rateLimitRetryInitialBackoff
	remaining := priorAutomaticRetries
	for _, plateau := range rateLimitBackoffPlateaus {
		for backoff < plateau.ceiling {
			if remaining == 0 {
				return backoff, true
			}
			remaining--
			backoff = min(backoff*2, plateau.ceiling)
		}
		if remaining < plateau.retries {
			return plateau.ceiling, true
		}
		remaining -= plateau.retries
		backoff *= 2
	}
	return 0, false
}

func scaleDuration(d time.Duration, factor float64) time.Duration {
	return time.Duration(float64(d) * factor)
}
