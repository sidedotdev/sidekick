package dev

import (
	"encoding/json"
	"fmt"

	"sidekick/llm"
	"sidekick/llm2"
)

func devRequirementsToolHandlers(iteration *LlmIteration, state *buildDevRequirementsState, recordedReqs **DevRequirements) map[string]func(DevContext, llm.ToolCall) (llm2.ToolResultBlock, error) {
	handlers := map[string]func(DevContext, llm.ToolCall) (llm2.ToolResultBlock, error){
		recordDevRequirementsTool.Name: func(dCtx DevContext, tc llm.ToolCall) (llm2.ToolResultBlock, error) {
			devReq, unmarshalErr := unmarshalDevRequirements(tc.Arguments)
			if unmarshalErr != nil {
				return llm2.ToolResultBlock{Content: llm2.TextContentBlocks(unmarshalErr.Error()), Name: tc.Name, ToolCallId: tc.Id, IsError: true}, nil
			}
			state.devRequirements = devReq
			if devReq.Complete {
				userResponse, approveErr := ApproveDevRequirements(dCtx, devReq)
				if approveErr != nil {
					return llm2.ToolResultBlock{}, fmt.Errorf("error approving dev requirements: %v", approveErr)
				}
				iteration.resetAfterFeedback()
				if userResponse.Approved != nil && *userResponse.Approved {
					*recordedReqs = &devReq
					return llm2.ToolResultBlock{Content: llm2.TextContentBlocks("Requirements recorded and approved."), Name: tc.Name, ToolCallId: tc.Id}, nil
				} else {
					feedback := fmt.Sprintf("Requirements were not approved. Current state:\n%s\n\nPlease try again, taking this feedback into account:\n\n%s", devReq.String(), userResponse.Content)
					result := llm2.ToolResultBlock{Content: llm2.TextContentBlocks(feedback), Name: tc.Name, ToolCallId: tc.Id}
					return deliverableApprovalFeedback(dCtx, result, "Requirements recorded but not approved. The user has feedback below.", userResponse.Content), nil
				}
			} else {
				return llm2.ToolResultBlock{Content: llm2.TextContentBlocks("Recorded partial requirements, but requirements are not finalized yet based on the \"requirements_finalized\" boolean field value being set to false. Do some more research or thinking to finalize the requirements, as needed. If you need more details or clarification from the user, use the " + getHelpOrInputTool.Name + " tool. Then record the finalized requirements again in full, or use update_dev_requirements to make incremental changes."), Name: tc.Name, ToolCallId: tc.Id}, nil
			}
		},
		updateDevRequirementsTool.Name: func(dCtx DevContext, tc llm.ToolCall) (llm2.ToolResultBlock, error) {
			var update DevRequirementsUpdate
			if err := json.Unmarshal([]byte(llm.RepairJson(tc.Arguments)), &update); err != nil {
				return llm2.ToolResultBlock{Content: llm2.TextContentBlocks(fmt.Sprintf("failed to unmarshal update: %v", err)), Name: tc.Name, ToolCallId: tc.Id, IsError: true}, nil
			}

			updatedReqs, err := applyDevRequirementsUpdates(state.devRequirements, update)
			if err != nil {
				return llm2.ToolResultBlock{Content: llm2.TextContentBlocks(fmt.Sprintf("failed to apply updates: %v", err)), Name: tc.Name, ToolCallId: tc.Id, IsError: true}, nil
			}
			state.devRequirements = updatedReqs

			if updatedReqs.Complete {
				userResponse, approveErr := ApproveDevRequirements(dCtx, updatedReqs)
				if approveErr != nil {
					return llm2.ToolResultBlock{}, fmt.Errorf("error approving dev requirements: %v", approveErr)
				}
				iteration.resetAfterFeedback()
				if userResponse.Approved != nil && *userResponse.Approved {
					*recordedReqs = &updatedReqs
					return llm2.ToolResultBlock{Content: llm2.TextContentBlocks("Requirements updated and approved."), Name: tc.Name, ToolCallId: tc.Id}, nil
				} else {
					feedback := fmt.Sprintf("Requirements updated but not approved. Current state:\n%s\n\nPlease try again, taking this feedback into account:\n\n%s", updatedReqs.String(), userResponse.Content)
					result := llm2.ToolResultBlock{Content: llm2.TextContentBlocks(feedback), Name: tc.Name, ToolCallId: tc.Id}
					return deliverableApprovalFeedback(dCtx, result, "Requirements updated but not approved. The user has feedback below.", userResponse.Content), nil
				}
			}

			text := fmt.Sprintf("Requirements updated successfully. Current state:\n%s", updatedReqs.String())
			if deliverableHistoryEnabled(dCtx.ExecContext) {
				text = "Requirements updated successfully."
			}
			return llm2.ToolResultBlock{Content: llm2.TextContentBlocks(text), Name: tc.Name, ToolCallId: tc.Id}, nil
		},
	}
	return deliverableToolHandlers(handlers, "dev_requirements", func() any { return state.devRequirements })
}
