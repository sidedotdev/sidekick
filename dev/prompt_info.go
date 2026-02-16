package dev

import (
	"encoding/json"
	"fmt"
	"sidekick/llm2"
	"strings"
)

// PromptInfoContainer  is a wrapper for the PromptInfo interface that provides custom
// JSON marshaling and unmarshaling.
type PromptInfoContainer struct {
	PromptInfo PromptInfo
}

// MarshalJSON returns the JSON encoding of the PromptInfoContainer.
func (pic PromptInfoContainer) MarshalJSON() ([]byte, error) {
	// Marshal to type and actual data to handle unmarshaling to specific interface type
	return json.Marshal(struct {
		Type string
		Info PromptInfo
	}{
		Type: pic.PromptInfo.GetType(),
		Info: pic.PromptInfo,
	})
}

// UnmarshalJSON parses the JSON-encoded data and stores the result in the PromptInfoContainer.
func (pic *PromptInfoContainer) UnmarshalJSON(data []byte) error {
	var v struct {
		Type string
		Info json.RawMessage
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	switch v.Type {
	case "initial_dev_requirements":
		var info InitialDevRequirementsInfo
		if err := json.Unmarshal(v.Info, &info); err != nil {
			return err
		}
		pic.PromptInfo = info
	case "initial_code":
		var ici InitialCodeInfo
		if err := json.Unmarshal(v.Info, &ici); err != nil {
			return err
		}
		pic.PromptInfo = ici
	case "initial_plan":
		var ipi InitialPlanningInfo
		if err := json.Unmarshal(v.Info, &ipi); err != nil {
			return err
		}
		pic.PromptInfo = ipi
	case "initial_dev_step":
		var idsi InitialDevStepInfo
		if err := json.Unmarshal(v.Info, &idsi); err != nil {
			return err
		}
		pic.PromptInfo = idsi
	case "check_edit":
		var cwi CheckWorkInfo
		if err := json.Unmarshal(v.Info, &cwi); err != nil {
			return err
		}
		pic.PromptInfo = cwi
	case "skip":
		var si SkipInfo
		if err := json.Unmarshal(v.Info, &si); err != nil {
			return err
		}
		pic.PromptInfo = si
	case "feedback":
		var fi FeedbackInfo
		if err := json.Unmarshal(v.Info, &fi); err != nil {
			return err
		}
		pic.PromptInfo = fi
	case "tool_call_response":
		var fcri ToolCallResponseInfo
		if err := json.Unmarshal(v.Info, &fcri); err != nil {
			return err
		}
		pic.PromptInfo = fcri
	default:
		return fmt.Errorf("unknown PromptInfo type: %s", v.Type)
	}

	return nil
}

type PromptInfo interface {
	GetType() string
}

type DetermineCodeContextInfo struct {
	RepoSummary   string
	Requirements  string
	Needs         string
	PlanExecution *DevPlanExecution
	Step          *DevStep
}

// Implement the PromptInfo interface for InitialCodePrompt
func (p DetermineCodeContextInfo) GetType() string {
	return "determine_code_context"
}

type RefineCodeContextInfo struct {
	DetermineCodeContextInfo
	OriginalCodeContext        string
	OriginalCodeContextRequest *RequiredCodeContext
}

// Implement the PromptInfo interface for InitialCodePrompt
func (p RefineCodeContextInfo) GetType() string {
	return "refine_code_context"
}

type InitialDevRequirementsInfo struct {
	Mission      string
	Context      string
	Requirements string
}

// Implement the PromptInfo interface for InitialCodePrompt
func (p InitialDevRequirementsInfo) GetType() string {
	return "initial_dev_requirements"
}

type InitialCodeInfo struct {
	CodeContext  string
	Requirements string
}

// Implement the PromptInfo interface for InitialCodePrompt
func (p InitialCodeInfo) GetType() string {
	return "initial_code"
}

type InitialPlanningInfo struct {
	CodeContext    string
	Requirements   string
	PlanningPrompt string
	ReproduceIssue bool
}

// Implement the PromptInfo interface for InitialPlanningInfo
func (p InitialPlanningInfo) GetType() string {
	return "initial_plan"
}

type InitialDevStepInfo struct {
	CodeContext   string
	Requirements  string
	PlanExecution DevPlanExecution
	Step          DevStep
}

// Implement the PromptInfo interface for InitialDevStepInfo
func (p InitialDevStepInfo) GetType() string {
	return "initial_dev_step"
}

// TODO consider making this more generic:
// fields would just be "Context", "Criteria", "Work" and "AutoChecks"
// TODO consider having prompt template names separate from prompt info, or
// passing a customized prompt string in full even
type CheckWorkInfo struct {
	CodeContext        string
	Requirements       string
	PlanExecution      DevPlanExecution
	Step               DevStep
	Work               string
	AutoChecks         string
	LastReviewTreeHash string
}

// Implement the PromptInfo interface for InitialDevStepInfo
func (p CheckWorkInfo) GetType() string {
	return "check_edit"
}

type SkipInfo struct{}

// Implement the PromptInfo interface for SkipInfo
func (p SkipInfo) GetType() string {
	return "skip"
}

// TODO include info about type feedback, eg test feedback vs apply edit feedback vs code review feedback etc
type FeedbackInfo struct {
	Feedback string
	Type     string
}

const (
	FeedbackTypePause          = "pause"
	FeedbackTypeUserGuidance   = "user_guidance"
	FeedbackTypeEditBlockError = "edit_block_error"
	FeedbackTypeApplyError     = "apply_error"
	FeedbackTypeSystemError    = "system_error"
	FeedbackTypeTestFailure    = "test_failure"
	FeedbackTypeAutoReview     = "auto_review"
)

// Implement the PromptInfo interface for FeedbackInfo
func (p FeedbackInfo) GetType() string {
	return "feedback"
}

func renderGeneralFeedbackPrompt(feedback, feedbackType string) string {
	data := map[string]interface{}{
		"feedback":       feedback,
		"isPause":        feedbackType == FeedbackTypePause,
		"isUserGuidance": feedbackType == FeedbackTypeUserGuidance,
		"isSystemError":  feedbackType == FeedbackTypeSystemError,
	}
	return RenderPrompt(GeneralFeedback, data)
}

type ToolCallResponseInfo struct {
	FunctionName      string
	ToolCallId        string
	IsError           bool
	ToolResultContent []llm2.ContentBlock
}

// TextResponse returns the concatenated text from all text content blocks.
func (p ToolCallResponseInfo) TextResponse() string {
	var sb strings.Builder
	for _, cb := range p.ToolResultContent {
		if cb.Type == llm2.ContentBlockTypeText {
			sb.WriteString(cb.Text)
		}
	}
	return sb.String()
}

// Implement the PromptInfo interface for ToolCallResponseInfo
func (p ToolCallResponseInfo) GetType() string {
	return "tool_call_response"
}
