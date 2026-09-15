package dev

import (
	"fmt"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/persisted_ai"

	"go.temporal.io/sdk/workflow"
)

type verifierSession struct {
	index   verifierChatHistoryIndex
	history *persisted_ai.ChatHistoryContainer
}

func (s *verifierSession) resetHistory() {
	s.history = nil
}

func (s *verifierSession) historyForReview(reuse bool, flowID, workspaceID string) *persisted_ai.ChatHistoryContainer {
	if !reuse {
		s.resetHistory()
	}
	if s.history != nil {
		return s.history
	}
	history := &persisted_ai.ChatHistoryContainer{
		History: persisted_ai.NewLlm2ChatHistory(flowID, workspaceID),
	}
	if reuse {
		s.history = history
	}
	return history
}

func reviewWithChatHistory(dCtx DevContext, info CheckWorkInfo, model common.ModelConfig, settings VerifierSettings) (CriteriaFulfillment, error) {
	return RunSubflow(dCtx, "verifier", "Verify Criteria", func(_ domain.Subflow) (CriteriaFulfillment, error) {
		return reviewWithChatHistorySubflow(dCtx, info, model, settings)
	})
}

func reviewWithChatHistorySubflow(dCtx DevContext, info CheckWorkInfo, model common.ModelConfig, settings VerifierSettings) (CriteriaFulfillment, error) {
	session := info.VerifierSession
	if session == nil {
		session = &verifierSession{}
	}
	flowID := workflow.GetInfo(dCtx).WorkflowExecution.ID
	var activities *VerifierHistoryActivities
	var prepared PrepareVerifierHistoryOutput
	err := workflow.ExecuteActivity(dCtx, activities.PrepareVerifierHistory, PrepareVerifierHistoryInput{
		ChatHistory: info.ChatHistory,
		FlowId:      flowID,
		WorkspaceId: dCtx.WorkspaceId,
		Index:       session.index,
		Settings:    settings,
		Provider:    model.Provider,
	}).Get(dCtx, &prepared)
	if err != nil {
		return CriteriaFulfillment{}, fmt.Errorf("failed to prepare review chat history: %w", err)
	}
	session.index = prepared.Index
	info.PreparedChatHistory = &prepared.PreparedChatHistory
	history := session.historyForReview(settings.ReuseHistory, flowID, dCtx.WorkspaceId)
	for _, message := range criteriaFulfillmentMessages(info, dCtx.RepoConfig.EditCode.Hints) {
		if err := AppendChatHistory(dCtx.ExecContext, history, &message); err != nil {
			session.resetHistory()
			return CriteriaFulfillment{}, err
		}
	}
	fulfillment, err := runVerifier(dCtx, info, model, history, prepared.PreparedChatHistory)
	if err != nil {
		// An interrupted turn may contain unresolved tool calls.
		session.resetHistory()
	}
	return fulfillment, err
}
