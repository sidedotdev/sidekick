package dev

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/llm"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/sashabaranov/go-openai"
)

var recordDevPlanTool = llm.Tool{
	Name:        "record_dev_plan",
	Description: "Records a step-by-step software development plan to fulfill the specified requirements.",
	Parameters:  (&jsonschema.Reflector{ExpandedStruct: true}).Reflect(&DevPlan{}),
}

type DevPlan struct {
	Analysis  string    `json:"analysis" jsonschema:"description=High-level analysis of what the plan will require before defining the individual steps."`
	Steps     []DevStep `json:"steps" jsonschema:"description=The top-level steps that must be executed to fulfill the given requirements. This should be a pretty short list."`
	Complete  bool      `json:"is_planning_complete" jsonschema:"description=Is the plan itself complete - not the actual execution of the plan\\, but the plan itself. Should be written out AFTER the steps in the plan\\, since completion is hard to figure out before the plan is written out."`
	Learnings []string  `json:"learnings" jsonschema:"description=What was learned while developing the plan that will aid in execution? If the plan is not complete\\, what did we learn that will help us complete the rest of this plan?"`
}

func CleanSteps(steps []DevStep) []DevStep {
	cleanedSteps := []DevStep{}
	for _, step := range steps {
		if step.Type != "None" && step.Type != "none" && step.Type != "Skip" && step.Type != "skip" && step.Type != "" {
			cleanedSteps = append(cleanedSteps, step)
		}
	}
	return cleanedSteps
}

func ValidateAndCleanPlan(plan DevPlan) (DevPlan, error) {
	plan.Steps = CleanSteps(plan.Steps)
	return plan, nil
}

// TODO also add EstimatedDevStep struct, or do it in one go within DevStep with an StepSize field
type DevStep struct {
	StepNumber         string `json:"step_number" jsonschema:"description=Hierarchical step number in the plan\\, eg \"1\" for the first top-level step\\, and \"2.1\" for the first sub-step of the second top-level step"`
	Title              string `json:"title" jsonschema:"description=Summary of the step's purpose"`
	Definition         string `json:"definition" jsonschema:"description=Information about what to do in this step. Should be short\\, yet include enough initial context/information for someone to be able to start performing the step."`
	Type               string `json:"type" jsonschema:"enum=edit,description=\"edit\" means code or non-code plaintext files must be created\\, deleted and/or edited. searching for text or code context in the repo can also be done in the same step. \"other\" means that other actions than the standard ones are required\\, so this will ask for external help"`
	CompletionAnalysis string `json:"completion_analysis" jsonschema:"description=Brief analysis of the minimal checks that should be done to confirm the step was successfully completed"`
}

func (plan DevPlan) String() string {
	writer := &strings.Builder{}
	for i, step := range plan.Steps {
		if i > 0 {
			writer.WriteString("\n")
		}
		writer.WriteString(step.String())
	}
	return writer.String()
}

func (step DevStep) String() string {
	writer := &strings.Builder{}
	level := len(strings.Split(step.StepNumber, ".")) - 1
	writer.WriteString(strings.Repeat("  ", level))
	writer.WriteString(step.StepNumber)
	writer.WriteString(") ")
	writer.WriteString(step.Title)
	writer.WriteString("\n")
	writer.WriteString(strings.ReplaceAll(strings.Trim(step.Definition, "\n"), "\n", "\n"+strings.Repeat("  ", level+1)))
	return writer.String()
}

func BuildDevPlan(dCtx DevContext, requirements, planningPrompt string, reproduceIssue bool) (*DevPlan, error) {
	return RunSubflow(dCtx, "Build Dev Plan", func(_ domain.Subflow) (*DevPlan, error) {
		return buildDevPlanSubflow(dCtx, requirements, planningPrompt, reproduceIssue)
	})
}

func buildDevPlanSubflow(dCtx DevContext, requirements, planningPrompt string, reproduceIssue bool) (*DevPlan, error) {
	codeContext, fullCodeContext, err := PrepareInitialCodeContext(dCtx, requirements, nil, nil)
	contextSizeExtension := len(fullCodeContext) - len(codeContext)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare code context: %v", err)
	}

	var promptInfo PromptInfo = InitialPlanningInfo{
		CodeContext:    codeContext,
		Requirements:   requirements,
		PlanningPrompt: planningPrompt,
		ReproduceIssue: reproduceIssue,
	}

	chatHistory := &[]llm.ChatMessage{}
	i := 0

	maxIterations := 17
	repoConfig := dCtx.RepoConfig
	if repoConfig.MaxPlanningIterations > 0 {
		maxIterations = repoConfig.MaxPlanningIterations
	}

	maxIterationsBeforeFeedback := 5
	hasRevisedPerPlanningPrompt := false
	hasRevisedPerReproPrompt := false
	var devPlan DevPlan
	for {
		i = i + 1
		if i > maxIterations {
			return nil, ErrMaxAttemptsReached
		} else if i%maxIterationsBeforeFeedback == 0 && !devPlan.Complete {
			// TODO include the plan so far if any. If plan is empty (no learnings and no steps), use a different guidance context string.
			// TODO don't ask for feedback if the user was just asked via get_help_or_input
			guidanceContext := "The LLM has looped 5 times without finalizing a plan. Please provide guidance or just say \"continue\" if they are on track."
			requestParams := map[string]any{}
			feedbackInfo, err := GetUserFeedback(dCtx, promptInfo, guidanceContext, chatHistory, requestParams)
			if err != nil {
				return nil, fmt.Errorf("failed to get user feedback: %v", err)
			}

			// FIXME this replaces the existing promptInfo which could have
			// important info in it that hasn't yet made it to chat history.
			// let's instead switch to the pattern used in code_context.go where
			// we add to the chat history as soon as we get the feedback instead
			// of in the next iteration
			promptInfo = feedbackInfo
		}

		maxLength := min(defaultMaxChatHistoryLength+contextSizeExtension, extendedMaxChatHistoryLength)
		ManageChatHistory(dCtx, chatHistory, maxLength)
		planningInput := getPlanningInput(dCtx, chatHistory, promptInfo)
		chatResponse, err := TrackedToolChat(dCtx, "Generate Dev Plan", planningInput)
		if err != nil {
			return nil, fmt.Errorf("error executing OpenAI chat completion activity: %w", err)
		}

		*chatHistory = append(*chatHistory, chatResponse.ChatMessage)
		if len(chatResponse.ToolCalls) > 0 {
			// NOTE we have parallel tool calls disabled for now
			toolCall := chatResponse.ToolCalls[0]
			if toolCall.Name == recordDevPlanTool.Name {
				unvalidatedDevPlan, err := unmarshalPlan(toolCall.Arguments)
				if err != nil {
					fmt.Printf("error parsing plan: %v\n", err)
					promptInfo = ToolCallResponseInfo{Response: "Plan failed to be parsed and was NOT recorded: " + err.Error(), FunctionName: recordDevPlanTool.Name, TooCallId: toolCall.Id}
					continue
				}
				validatedDevPlan, err := ValidateAndCleanPlan(unvalidatedDevPlan)
				if err != nil {
					promptInfo = ToolCallResponseInfo{Response: "Plan failed to be parsed and was NOT recorded: " + err.Error(), FunctionName: recordDevPlanTool.Name, TooCallId: toolCall.Id}
					continue
				}
				devPlan = validatedDevPlan

				if devPlan.Complete {
					if planningPrompt != "" && !hasRevisedPerPlanningPrompt {
						hasRevisedPerPlanningPrompt = true
						promptInfo = ToolCallResponseInfo{Response: "List out all conditions/requirements in the following instructions. Then consider whether the plan meets each one, one by one. Once you have done that, then rewrite & record the plan as needed to ensure it meets all conditions/requirements.\n\nInstructions follow:\n\n" + planningPrompt, FunctionName: recordDevPlanTool.Name, TooCallId: toolCall.Id}
						continue
					}
					if reproduceIssue && !hasRevisedPerReproPrompt {
						hasRevisedPerReproPrompt = true
						promptInfo = ToolCallResponseInfo{Response: reviseReproPrompt, FunctionName: recordDevPlanTool.Name, TooCallId: toolCall.Id}
						continue
					}

					userResponse, err := ApproveDevPlan(dCtx, devPlan)
					if err != nil {
						return nil, fmt.Errorf("error executing OpenAI chat completion activity: %w", err)
					}
					if userResponse.Approved != nil && *userResponse.Approved {
						break
					} else {
						feedback := "Plan was not approved and therefore not recorded. Please continue planning by taking this feedback into account:\n\n" + userResponse.Content
						promptInfo = ToolCallResponseInfo{Response: feedback, FunctionName: toolCall.Name, TooCallId: toolCall.Id}
						// resetting i to the closest multiple so that we don't ask for feedback again immediately
						i = int(math.Round(float64(i)/float64(maxIterationsBeforeFeedback))) * maxIterationsBeforeFeedback
						continue
					}
				} else {
					// TODO add a ContinuePlanningInfo struct that implements PromptInfo
					promptInfo = ToolCallResponseInfo{Response: "Recorded plan progress, but the plan is not complete yet based on the \"is_planning_complete\" boolean field value being set to false. Do some more research or thinking or get help/input to complete the plan, as needed. Once the planning is complete, record the plan again in full.", FunctionName: recordDevPlanTool.Name, TooCallId: toolCall.Id}
				}
			} else {
				var toolCallResponseInfo ToolCallResponseInfo
				toolCallResponseInfo, err = handleToolCall(dCtx, chatResponse.ToolCalls[0])
				// dynamically adjust the context size extension based on the length of the response
				if len(toolCallResponseInfo.Response) > 5000 {
					contextSizeExtension += len(toolCallResponseInfo.Response) - 5000
				}
				promptInfo = toolCallResponseInfo
				if err != nil {
					return nil, fmt.Errorf("error handling tool call: %w", err)
				}
				continue
			}
		} else if chatResponse.StopReason == string(openai.FinishReasonStop) || chatResponse.StopReason == string(openai.FinishReasonToolCalls) {
			promptInfo = FeedbackInfo{Feedback: "Expected a tool call to record the plan, but didn't get it. Embedding the json in the content is not sufficient. Please record the plan via the " + recordDevPlanTool.Name + " tool."}
			continue
		} else {
			log.Printf("expected chat stream stop reason to be stop or tool_call, got: %v", chatResponse.StopReason)
			promptInfo = SkipInfo{}
			continue
		}

		if err != nil {
			return nil, err
		}
	}

	return &devPlan, nil
}

func unmarshalPlan(jsonStr string) (DevPlan, error) {
	var plan DevPlan
	err := json.Unmarshal([]byte(llm.RepairJson(jsonStr)), &plan)
	if err != nil {
		return DevPlan{}, fmt.Errorf("failed to unmarshal json for plan: %v", err)
	}
	return plan, nil
}

func getPlanningInput(dCtx DevContext, chatHistory *[]llm.ChatMessage, promptInfo PromptInfo) llm.ToolChatOptions {
	// TODO extract chat message building into a separate function
	var content string
	role := llm.ChatMessageRoleUser
	name := ""
	toolCallId := ""
	skip := false
	cacheControl := ""
	switch info := promptInfo.(type) {
	case InitialPlanningInfo:
		content = buildInitialRecordPlanPrompt(dCtx, info.CodeContext, info.Requirements, info.PlanningPrompt, info.ReproduceIssue)
		cacheControl = "ephemeral"
	case FeedbackInfo:
		content = info.Feedback
	case SkipInfo:
		skip = true
	case ToolCallResponseInfo:
		role = llm.ChatMessageRoleTool
		content = info.Response
		name = info.FunctionName
		toolCallId = info.TooCallId
	default:
		panic("Unsupported prompt type for planning: " + promptInfo.GetType())
	}

	if !skip {
		newMessage := llm.ChatMessage{
			Role:       role,
			Content:    content,
			Name:       name,
			ToolCallId: toolCallId,
			CacheControl: cacheControl,
		}
		*chatHistory = append(*chatHistory, newMessage)
	}

	tools := []*llm.Tool{
		&recordDevPlanTool,
		&bulkSearchRepositoryTool,
		getRetrieveCodeContextTool(),
		&bulkReadFileTool,
	}
	if !dCtx.RepoConfig.DisableHumanInTheLoop {
		tools = append(tools, &getHelpOrInputTool)
	}

	provider, modelConfig, _ := dCtx.GetToolChatConfig(common.PlanningKey, 0)

	return llm.ToolChatOptions{
		Secrets: *dCtx.Secrets,
		Params: llm.ToolChatParams{
			Messages: *chatHistory,
			Tools:    tools,
			ToolChoice: llm.ToolChoice{
				Type: llm.ToolChoiceTypeAuto,
			},
			Provider: provider,
			Model:    modelConfig.Model,
		},
	}
}

// TODO we should determine if the code context is too large programmatically
// instead of depending on the LLM's notion of "too large", which is bound to be
// extremely unreliable
func buildInitialRecordPlanPrompt(dCtx DevContext, codeContext, requirements, planningPrompt string, reproduceIssue bool) string {
	data := map[string]interface{}{
		"codeContext":            codeContext,
		"requirements":           requirements,
		"recordPlanFunctionName": recordDevPlanTool.Name,
		"planningPrompt":         planningPrompt,
		"reproducePrompt":        reproducePrompt,
		"reproduceIssue":         reproduceIssue,
	}
	if !dCtx.RepoConfig.DisableHumanInTheLoop {
		data["getHelpOrInputFunctionName"] = getHelpOrInputTool.Name
	}
	return RenderPrompt(RecordPlanInitial, data)
}

func ApproveDevPlan(dCtx DevContext, devPlan DevPlan) (*UserResponse, error) {
	req := RequestForUser{
		Content:       "Please approve or reject the development plan:\n\n" + devPlan.String() + "\n\nDo you approve this plan? If not, please provide feedback on what needs to be changed.",
		RequestParams: map[string]interface{}{"approveTag": "approve_plan", "rejectTag": "reject_plan"},
	}
	actionCtx := dCtx.NewActionContext("Approve Dev Plan")
	return GetUserApproval(actionCtx, req.Content, req.RequestParams)
}

// List out all conditions/requirements in the following instructions. Then
// consider whether the plan meets each one, one by one. Once you have done that,
// then rewrite & record the plan as needed to ensure it meets all
// conditions/requirements.
//
// Instructions follow
const reproducePrompt = `
I want you to determine the arrangement, action and assertion (ala AAA:
Arrange-Act-Assert) for a test that would reproduce the bug report: the
assertion should test the key behavior being reported, and the assertion should
be written such that it fails with the current buggy code, but will pass when
the code is fixed.`

const reviseReproPrompt = reproducePrompt + ` If you need to look up further code to determine the right
AAA to achieve this, do so before recording the plan again.

We want to reproduce the bug accurately before fixing it, so the plan should
include at least one step that creates a test that reproduces the issue. In that
step, describe the AAA in detail, and ensure you include the predicted failure of
the test prior to fixing the bug as part of the completion_analysis.
`
