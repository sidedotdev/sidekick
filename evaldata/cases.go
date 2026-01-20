package evaldata

import (
	"sidekick/domain"
)

// SplitIntoCases splits a slice of flow actions into ordered cases.
// Each case ends at (and includes) an action with ActionType == "user_request.approve.merge".
// Actions are first sorted deterministically by Created asc, then Id asc.
// Returns an empty slice if there are no merge approval actions.
func SplitIntoCases(actions []domain.FlowAction) []Case {
	if len(actions) == 0 {
		return nil
	}

	sorted := SortFlowActions(actions)

	var cases []Case
	var currentActions []domain.FlowAction
	caseIndex := 0

	for _, action := range sorted {
		currentActions = append(currentActions, action)

		if action.ActionType == ActionTypeMergeApproval {
			cases = append(cases, Case{
				CaseId:    action.Id,
				CaseIndex: caseIndex,
				FlowId:    action.FlowId,
				Actions:   currentActions,
			})
			currentActions = nil
			caseIndex++
		}
	}

	// Any remaining actions after the last merge approval are not included
	// in a case, as cases must end with a merge approval boundary.

	return cases
}

// GetRankQuery extracts the rankQuery from a case's ranked_repo_summary action.
// Returns empty string if no such action exists or if rankQuery is not found.
func GetRankQuery(c Case) string {
	for _, action := range c.Actions {
		if action.ActionType == ActionTypeRankedRepoSummary {
			if rankQuery, ok := action.ActionParams["rankQuery"].(string); ok {
				return rankQuery
			}
		}
	}
	return ""
}

// GetMergeApprovalAction returns the merge approval action that ends the case.
// Returns nil if the case has no actions (should not happen for valid cases).
func GetMergeApprovalAction(c Case) *domain.FlowAction {
	if len(c.Actions) == 0 {
		return nil
	}
	lastAction := c.Actions[len(c.Actions)-1]
	if lastAction.ActionType == ActionTypeMergeApproval {
		return &lastAction
	}
	return nil
}
