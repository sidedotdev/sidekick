package flow_action

import (
	"fmt"
	"maps"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/srv"
	"sidekick/utils"
	"slices"
	"strings"

	"go.temporal.io/sdk/workflow"
)

// SignalNameProviderAuthUpdated is broadcast to running flows whenever a
// provider credential is saved.
const SignalNameProviderAuthUpdated = "providerAuthUpdated"

// ProviderAuthUpdated records that a provider credential was saved. Its JSON
// shape matches provider_auth.CredentialUpdatedEvent so either can be sent.
type ProviderAuthUpdated struct {
	Provider  string                  `json:"provider"`
	ProfileId string                  `json:"profileId"`
	Method    string                  `json:"method"`
	AuthType  common.ProviderAuthType `json:"authType"`
}

// ProviderAuthResolution is how a provider_auth request was resolved: either
// by the user choosing a method or by a matching credential update elsewhere.
type ProviderAuthResolution struct {
	Method string `json:"method"`
	// AuthType is only known up front for external resolutions, which report
	// the auth type the saved credential serves.
	AuthType           common.ProviderAuthType `json:"authType,omitempty"`
	ResolvedExternally bool                    `json:"resolvedExternally"`
}

// GetUserProviderAuth issues a provider_auth request and waits until the user
// responds or a credential update accepted by match arrives.
func GetUserProviderAuth(eCtx ExecContext, req RequestForUser, match func(ProviderAuthUpdated) bool) (*ProviderAuthResolution, error) {
	if eCtx.DisableHumanInTheLoop {
		return nil, fmt.Errorf("can't get provider auth from user as human-in-the-loop process is disabled")
	}

	req.OriginWorkflowId = workflow.GetInfo(eCtx).WorkflowExecution.ID
	req.Subflow = eCtx.FlowScope.SubflowName
	req.RequestKind = RequestKindProviderAuth
	actionCtx := eCtx.NewActionContext("user_request.provider_auth")
	actionCtx.ActionParams = req.ActionParams()

	return TrackHuman(actionCtx, func(trackedActionCtx ActionContext, flowAction *domain.FlowAction) (*ProviderAuthResolution, error) {
		req.FlowActionId = flowAction.Id
		return awaitProviderAuth(trackedActionCtx.ExecContext, req, match)
	})
}

func awaitProviderAuth(eCtx ExecContext, req RequestForUser, match func(ProviderAuthUpdated) bool) (*ProviderAuthResolution, error) {
	ctx := eCtx.Context
	authUpdatedCh := workflow.GetSignalChannel(ctx, SignalNameProviderAuthUpdated)

	// Updates buffered before this request existed predate the failure being
	// resolved, so they must not satisfy it.
	var stale ProviderAuthUpdated
	for authUpdatedCh.ReceiveAsync(&stale) {
	}

	if err := announceUserRequest(eCtx, &req); err != nil {
		return nil, err
	}

	responseCh := workflow.GetSignalChannel(ctx, UserResponseSignalName(req.FlowActionId))
	var resolution *ProviderAuthResolution
	for resolution == nil {
		selector := workflow.NewNamedSelector(ctx, "providerAuthSelector")
		selector.AddReceive(responseCh, func(c workflow.ReceiveChannel, more bool) {
			var response UserResponse
			c.Receive(ctx, &response)
			resolution = &ProviderAuthResolution{Method: response.Choice}
		})
		selector.AddReceive(authUpdatedCh, func(c workflow.ReceiveChannel, more bool) {
			var event ProviderAuthUpdated
			c.Receive(ctx, &event)
			if match(event) {
				resolution = &ProviderAuthResolution{
					Method:             event.Method,
					AuthType:           event.AuthType,
					ResolvedExternally: true,
				}
			}
		})
		selector.AddReceive(ctx.Done(), func(c workflow.ReceiveChannel, more bool) {})
		selector.Select(ctx)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	// The complete flow action handler unpauses flows resolved by the user, but
	// nothing does so for external resolutions.
	if resolution.ResolvedExternally && !req.SkipPauseFlow {
		resumePausedFlow(eCtx, req.OriginWorkflowId)
	}
	signalSubtaskUnblocked(eCtx)

	return resolution, nil
}

// resumePausedFlow is best-effort: the credential is already fixed, so failing
// the flow over a stale status would be worse than leaving it paused.
func resumePausedFlow(eCtx ExecContext, flowId string) {
	if workflow.GetVersion(eCtx.Context, "pause-flow", workflow.DefaultVersion, 1) != 1 {
		return
	}
	logger := workflow.GetLogger(eCtx.Context)
	storageCtx := utils.WithStorageActivityOptions(eCtx.Context)
	var flow domain.Flow
	if err := workflow.ExecuteActivity(storageCtx, srv.Activities.GetFlow, eCtx.WorkspaceId, flowId).Get(storageCtx, &flow); err != nil {
		logger.Warn("Failed to get flow to resume after provider auth update", "flowId", flowId, "error", err)
		return
	}
	if flow.Status != domain.FlowStatusPaused {
		return
	}
	flow.Status = "in_progress"
	if err := workflow.ExecuteActivity(storageCtx, srv.Activities.PersistFlow, flow).Get(storageCtx, nil); err != nil {
		logger.Warn("Failed to resume flow after provider auth update", "flowId", flowId, "error", err)
	}
}

const GlobalStateKeyProviderAuthTypeOverrides = "providerAuthTypeOverrides"

var builtinProviderTypes = []string{
	string(common.AnthropicChatProvider),
	string(common.OpenaiChatProvider),
	string(common.GoogleChatProvider),
}

// SetProviderAuthTypeOverride makes all later requests in this flow use
// authType for the named provider, regardless of its configured auth type.
func (eCtx *ExecContext) SetProviderAuthTypeOverride(provider string, authType common.ProviderAuthType) {
	if eCtx.GlobalState == nil {
		eCtx.GlobalState = &GlobalState{}
	}
	overrides := eCtx.GetProviderAuthTypeOverrides()
	for name := range overrides {
		if strings.EqualFold(name, provider) {
			delete(overrides, name)
		}
	}
	overrides[provider] = authType
	eCtx.GlobalState.SetValue(GlobalStateKeyProviderAuthTypeOverrides, overrides)
}

// GetProviderAuthTypeOverrides returns a copy of the flow's auth type
// overrides keyed by provider name.
func (eCtx *ExecContext) GetProviderAuthTypeOverrides() map[string]common.ProviderAuthType {
	overrides := map[string]common.ProviderAuthType{}
	if eCtx.GlobalState == nil {
		return overrides
	}
	stored, _ := eCtx.GlobalState.GetValue(GlobalStateKeyProviderAuthTypeOverrides).(map[string]common.ProviderAuthType)
	maps.Copy(overrides, stored)
	return overrides
}

// ProviderAuthTypeOverride reports the flow's auth type override for a
// provider, matching names case-insensitively.
func (eCtx *ExecContext) ProviderAuthTypeOverride(provider string) (common.ProviderAuthType, bool) {
	for name, authType := range eCtx.GetProviderAuthTypeOverrides() {
		if strings.EqualFold(name, provider) {
			return authType, true
		}
	}
	return "", false
}

// ApplyProviderAuthOverrides returns a copy of providers with the flow's auth
// type overrides applied. Builtin providers used without explicit config are
// appended so the override still takes effect for them.
func (eCtx *ExecContext) ApplyProviderAuthOverrides(providers []common.ModelProviderPublicConfig) []common.ModelProviderPublicConfig {
	overrides := eCtx.GetProviderAuthTypeOverrides()
	if len(overrides) == 0 {
		return providers
	}

	result := slices.Clone(providers)
	applied := map[string]bool{}
	for i := range result {
		if authType, ok := eCtx.ProviderAuthTypeOverride(result[i].Name); ok {
			result[i].AuthType = authType
			applied[strings.ToLower(result[i].Name)] = true
		}
	}

	for _, name := range slices.Sorted(maps.Keys(overrides)) {
		providerType := strings.ToLower(name)
		if applied[providerType] || !slices.Contains(builtinProviderTypes, providerType) {
			continue
		}
		result = append(result, common.ModelProviderPublicConfig{
			Name:     name,
			Type:     providerType,
			AuthType: overrides[name],
		})
	}
	return result
}
