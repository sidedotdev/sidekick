package flow_action

import (
	"fmt"
	"sidekick/common"
	"time"
)

const (
	rateLimitRetryInitialBackoff = time.Minute
	rateLimitRetryMaxBackoff     = time.Hour
)

// RateLimitFailureHandler races a scheduled automatic retry against the
// user's "Try Again" when an activity fails with a provider rate limit error.
// The automatic retry waits for the provider's own hint when it gives one and
// otherwise backs off exponentially from a minute up to an hour, indefinitely.
// Other failures go to Fallback. A handler instance tracks consecutive rate
// limits, so use one per retry loop.
type RateLimitFailureHandler struct {
	// Fallback handles failures that aren't rate limits. Defaults to
	// ContinuePromptFailureHandler.
	Fallback ActivityFailureHandler

	consecutiveRateLimits int
}

func (h *RateLimitFailureHandler) HandleActivityFailure(eCtx ExecContext, actionName string, activityErr error) (RetryDecision, error) {
	details, ok := common.AsProviderRateLimitError(activityErr)
	if !ok {
		h.consecutiveRateLimits = 0
		fallback := h.Fallback
		if fallback == nil {
			fallback = ContinuePromptFailureHandler{}
		}
		return fallback.HandleActivityFailure(eCtx, actionName, activityErr)
	}

	wait := rateLimitRetryWait(details.RetryAfter, h.consecutiveRateLimits)
	h.consecutiveRateLimits++

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

// rateLimitRetryWait trusts the provider's hint on its own for a first
// rejection. Repeated rejections mean the hint underestimates the limit (e.g.
// a spend cap that reports a per-minute reset), so exponential backoff takes
// over as a floor, growing by one step per prior consecutive rate limit.
func rateLimitRetryWait(retryAfter time.Duration, priorConsecutive int) time.Duration {
	backoffSteps := priorConsecutive
	if retryAfter > 0 {
		backoffSteps--
	}
	if backoffSteps < 0 {
		return retryAfter
	}
	backoff := rateLimitRetryMaxBackoff
	// Shifting past the cap would overflow long before the loop could get
	// there, so only compute below it.
	if backoffSteps < 10 {
		backoff = min(rateLimitRetryInitialBackoff<<backoffSteps, rateLimitRetryMaxBackoff)
	}
	return max(retryAfter, backoff)
}
