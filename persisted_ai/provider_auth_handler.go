package persisted_ai

import (
	"fmt"
	"strings"
	"time"

	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/provider_auth"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const ProviderAuthScopeNote = "Applies to this flow only. To change the default, edit the provider config."

// ProviderAuthFailureHandler asks the user to fix broken provider credentials
// when an activity fails with a provider auth error, then retries with the
// chosen auth type. Other failures get the generic continue prompt.
type ProviderAuthFailureHandler struct{}

func (ProviderAuthFailureHandler) HandleActivityFailure(eCtx flow_action.ExecContext, actionName string, activityErr error) (flow_action.RetryDecision, error) {
	details, ok := common.AsProviderAuthError(activityErr)
	if !ok {
		return flow_action.ContinuePromptFailureHandler{}.HandleActivityFailure(eCtx, actionName, activityErr)
	}
	methods := provider_auth.MethodsForProvider(details.Provider)
	if len(methods) == 0 {
		return flow_action.ContinuePromptFailureHandler{}.HandleActivityFailure(eCtx, actionName, activityErr)
	}

	req := flow_action.RequestForUser{
		Content: providerAuthRequestContent(*details),
		RequestParams: map[string]any{
			"provider":  details.Provider,
			"profileId": common.NormalizeProfileId(details.ProfileId),
			"reason":    string(details.Reason),
			"authType":  string(details.AuthType),
			"methods":   methods,
			"note":      ProviderAuthScopeNote,
		},
	}
	resolution, err := flow_action.GetUserProviderAuth(eCtx, req, func(event flow_action.ProviderAuthUpdated) bool {
		return credentialUpdateMatches(*details, event)
	})
	if err != nil {
		return flow_action.RetryDecisionFail, err
	}

	authType := resolution.AuthType
	if !resolution.ResolvedExternally {
		authType = provider_auth.AuthTypeForMethod(provider_auth.Method(resolution.Method))
	}
	authType = common.NormalizeProviderAuthType(string(authType))
	eCtx.SetProviderAuthTypeOverride(details.Provider, authType)

	if details.Reason == common.ProviderAuthErrorReasonInvalidOAuth && authType == common.ProviderAuthTypeAPI {
		deleteInvalidOAuthCredential(eCtx, *details)
	}
	return flow_action.RetryDecisionRetry, nil
}

func providerAuthRequestContent(details common.ProviderAuthErrorDetails) string {
	subject := details.Provider
	if common.NormalizeProfileId(details.ProfileId) != common.DefaultProfileId {
		subject = fmt.Sprintf("%s (profile %s)", subject, details.ProfileId)
	}
	switch details.Reason {
	case common.ProviderAuthErrorReasonInvalidOAuth:
		return fmt.Sprintf("The OAuth credential for %s is invalid or expired.", subject)
	case common.ProviderAuthErrorReasonMissingAPIKey:
		return fmt.Sprintf("No API key found for %s.", subject)
	case common.ProviderAuthErrorReasonInvalidAPIKey:
		return fmt.Sprintf("The existing API key for %s was rejected.", subject)
	default:
		return fmt.Sprintf("Authentication failed for %s.", subject)
	}
}

func credentialUpdateMatches(details common.ProviderAuthErrorDetails, event flow_action.ProviderAuthUpdated) bool {
	return provider_auth.NormalizeProvider(event.Provider) == provider_auth.NormalizeProvider(details.Provider) &&
		strings.EqualFold(common.NormalizeProfileId(event.ProfileId), common.NormalizeProfileId(details.ProfileId))
}

// deleteInvalidOAuthCredential is best-effort: the auth type override already
// steers requests away from the OAuth credential, so leaving it behind only
// affects other flows that would hit the same failure anyway.
func deleteInvalidOAuthCredential(eCtx flow_action.ExecContext, details common.ProviderAuthErrorDetails) {
	ctx := workflow.WithActivityOptions(eCtx.Context, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	var pa *provider_auth.Activities
	input := provider_auth.DeleteOAuthCredentialInput{
		Provider:  details.Provider,
		ProfileId: common.NormalizeProfileId(details.ProfileId),
	}
	if err := workflow.ExecuteActivity(ctx, pa.DeleteOAuthCredential, input).Get(ctx, nil); err != nil {
		workflow.GetLogger(eCtx.Context).Warn("Failed to delete invalid OAuth credential",
			"provider", details.Provider,
			"profileId", input.ProfileId,
			"error", err)
	}
}
