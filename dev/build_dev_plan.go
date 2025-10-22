package dev

import (
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/llm"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/sashabaranov/go-openai"
	"go.temporal.io/sdk/workflow"
)

type buildDevPlanState struct {
	contextSizeExtension        int
	hasRevisedPerPlanningPrompt bool
	hasRevisedPerReproPrompt    bool
	devPlan                     DevPlan
	planningPrompt              string
	reproduceIssue              bool
}

var recordDevPlanTool = llm.Tool{
	Name:        "record_dev_plan",
	Description: "Records a step-by-step software development plan to fulfill the specified requirements.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&DevPlan{}),
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
	Definition         string `json:"definition" jsonschema:"description=Information about what to do in this step formatted with markdown (without any headings). Should be short\\, yet include enough initial context/information for someone to be able to start performing the step."`
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
	if level == 0 {
		writer.WriteString("#### Step ")
		writer.WriteString(step.StepNumber)
		writer.WriteString(": ")
	} else {
		writer.WriteString(step.StepNumber)
		writer.WriteString(". ")
	}
	writer.WriteString(step.Title)
	writer.WriteString("\n")
	writer.WriteString(strings.ReplaceAll(strings.Trim(step.Definition, "\n"), "\n", "\n"+strings.Repeat("  ", level+1)))
	return writer.String()
}

func BuildDevPlan(dCtx DevContext, requirements, planningPrompt string, reproduceIssue bool) (*DevPlan, error) {
	return RunSubflow(dCtx, "dev_plan", "Build Dev Plan", func(_ domain.Subflow) (*DevPlan, error) {
		return buildDevPlanSubflow(dCtx, requirements, planningPrompt, reproduceIssue)
	})
}

func buildDevPlanSubflow(dCtx DevContext, requirements, planningPrompt string, reproduceIssue bool) (*DevPlan, error) {
	codeContext, fullCodeContext, err := PrepareInitialCodeContext(dCtx, requirements, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare code context: %w", err)
	}
	contextSizeExtension := len(fullCodeContext) - len(codeContext)

	chatHistory := &[]llm.ChatMessage{}
	addDevPlanPrompt(dCtx, chatHistory, InitialPlanningInfo{
		CodeContext:    codeContext,
		Requirements:   requirements,
		PlanningPrompt: planningPrompt,
		ReproduceIssue: reproduceIssue,
	})

	maxIterations := 17
	if dCtx.RepoConfig.MaxPlanningIterations > 0 {
		maxIterations = dCtx.RepoConfig.MaxPlanningIterations
	}

	initialState := &buildDevPlanState{
		contextSizeExtension:        contextSizeExtension,
		hasRevisedPerPlanningPrompt: false,
		hasRevisedPerReproPrompt:    false,
		planningPrompt:              planningPrompt,
		reproduceIssue:              reproduceIssue,
	}

	feedbackIterations := 5
	v := workflow.GetVersion(dCtx, "dev-planning-feedback-iterations", workflow.DefaultVersion, 1)
	if v == 1 {
		// TODO when tool calls are not finding things automatically, provide
		// better hints for how to find things after Nth iteration, before going
		// to human-based support. Eg fuzzy search or embedding search etc.
		// Maybe provide that as a tool or even run that tool automatically.
		feedbackIterations = 9
	}

	result, err := LlmLoop(
		dCtx,
		chatHistory,
		buildDevPlanIteration,
		WithInitialState(initialState),
		WithFeedbackEvery(feedbackIterations),
		WithMaxIterations(maxIterations),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to run LlmLoop: %w", err)
	}

	return result, nil
}

func buildDevPlanIteration(iteration *LlmIteration) (*DevPlan, error) {
	state, ok := iteration.State.(*buildDevPlanState)
	if !ok {
		return nil, fmt.Errorf("Invalid llm iteration state type, expected *buildDevPlanState: %v", iteration.State)
	}

	maxLength := min(defaultMaxChatHistoryLength+state.contextSizeExtension, extendedMaxChatHistoryLength)
	ManageChatHistory(iteration.ExecCtx, iteration.ChatHistory, maxLength)

	var chatResponse *llm.ChatMessageResponse
	var err error
	if v := workflow.GetVersion(iteration.ExecCtx, "dev-plan-cleanup-cancel-internally", workflow.DefaultVersion, 1); v == 1 {
		chatResponse, err = generateDevPlan(iteration.ExecCtx, iteration.ChatHistory)
	} else {
		// old version: new one does this in outer LlmLoop
		chatCtx := iteration.ExecCtx.WithCancelOnPause()
		chatResponse, err = generateDevPlan(chatCtx, iteration.ChatHistory)
		if iteration.ExecCtx.GlobalState != nil && iteration.ExecCtx.GlobalState.Paused {
			return nil, nil // continue the loop: UserRequestIfPaused will handle the pause
		}
	}
	if err != nil {
		return nil, fmt.Errorf("error generating dev plan: %w", err)
	}

	*iteration.ChatHistory = append(*iteration.ChatHistory, chatResponse.ChatMessage)

	if len(chatResponse.ToolCalls) > 0 {
		toolCall := chatResponse.ToolCalls[0]
		if toolCall.Name == recordDevPlanTool.Name {
			unvalidatedDevPlan, err := unmarshalPlan(toolCall.Arguments)
			if err != nil {
				addToolCallResponse(iteration.ChatHistory, ToolCallResponseInfo{
					Response:     "Please output a new plan: Plan failed to be parsed and was NOT recorded: " + err.Error(),
					FunctionName: recordDevPlanTool.Name,
					ToolCallId:   toolCall.Id,
					IsError:      true,
				})
				return nil, nil // continue the loop
			}

			validatedDevPlan, err := ValidateAndCleanPlan(unvalidatedDevPlan)
			if err != nil {
				addToolCallResponse(iteration.ChatHistory, ToolCallResponseInfo{
					Response:     "Please output a new plan: Plan failed validation and was NOT recorded: " + err.Error(),
					FunctionName: recordDevPlanTool.Name,
					ToolCallId:   toolCall.Id,
					IsError:      true,
				})
				return nil, nil // continue the loop
			}

			state.devPlan = validatedDevPlan
			if validatedDevPlan.Complete {
				if !state.hasRevisedPerPlanningPrompt && state.planningPrompt != "" {
					state.hasRevisedPerPlanningPrompt = true
					addToolCallResponse(iteration.ChatHistory, ToolCallResponseInfo{
						Response:     "List out all conditions/requirements in the following instructions. Then consider whether the plan meets each one, one by one. Once you have done that, then rewrite & record the plan as needed to ensure it meets all conditions/requirements.\n\nInstructions follow:\n\n" + state.planningPrompt,
						FunctionName: recordDevPlanTool.Name,
						ToolCallId:   toolCall.Id,
					})
					return nil, nil // continue the loop
				}

				if !state.hasRevisedPerReproPrompt && state.reproduceIssue {
					state.hasRevisedPerReproPrompt = true
					addToolCallResponse(iteration.ChatHistory, ToolCallResponseInfo{
						Response:     reviseReproPrompt,
						FunctionName: recordDevPlanTool.Name,
						ToolCallId:   toolCall.Id,
					})
					return nil, nil // continue the loop
				}

				userResponse, err := ApproveDevPlan(iteration.ExecCtx, validatedDevPlan)
				if err != nil {
					return nil, fmt.Errorf("error getting plan approval: %w", err)
				}

				v := workflow.GetVersion(iteration.ExecCtx, "dev-plan", workflow.DefaultVersion, 1)
				if v == 1 {
					iteration.NumSinceLastFeedback = 0
				}

				if userResponse.Approved != nil && *userResponse.Approved {
					return &validatedDevPlan, nil
				} else {
					addToolCallResponse(iteration.ChatHistory, ToolCallResponseInfo{
						Response:     "Plan was not approved and therefore not recorded. Please continue planning by taking this feedback into account:\n\n" + userResponse.Content,
						FunctionName: toolCall.Name,
						ToolCallId:   toolCall.Id,
					})
				}
			} else {
				addToolCallResponse(iteration.ChatHistory, ToolCallResponseInfo{
					Response:     "Recorded plan progress, but the plan is not complete yet based on the \"is_planning_complete\" boolean field value being set to false. Do some more research or thinking or get help/input to complete the plan, as needed. Once the planning is complete, record the plan again in full.",
					FunctionName: recordDevPlanTool.Name,
					ToolCallId:   toolCall.Id,
				})
			}
		} else {
			toolCallResponseInfo, err := handleToolCall(iteration.ExecCtx, toolCall)
			if err != nil {
				return nil, fmt.Errorf("error handling tool call: %w", err)
			}

			if len(toolCallResponseInfo.Response) > 5000 {
				state.contextSizeExtension += len(toolCallResponseInfo.Response) - 5000
			}

			addToolCallResponse(iteration.ChatHistory, toolCallResponseInfo)
			if toolCall.Name == getHelpOrInputTool.Name {
				iteration.NumSinceLastFeedback = 0
			}
		}
	} else if chatResponse.StopReason == string(openai.FinishReasonStop) || chatResponse.StopReason == string(openai.FinishReasonToolCalls) {
		addToolCallResponse(iteration.ChatHistory, ToolCallResponseInfo{
			Response:     "Expected a tool call to record the plan, but didn't get it. Embedding the json in the content is not sufficient. Please record the plan via the " + recordDevPlanTool.Name + " tool.",
			FunctionName: recordDevPlanTool.Name,
		})
	} else { // FIXME handle other stop reasons with more specific logic
		feedbackInfo := FeedbackInfo{Feedback: "Expected a tool call to record the dev requirements, but didn't get it. Embedding the json in the content is not sufficient. Please record the plan via the " + recordDevRequirementsTool.Name + " tool."}
		addDevRequirementsPrompt(iteration.ChatHistory, feedbackInfo)
	}

	return nil, nil // continue the loop
}

func unmarshalPlan(jsonStr string) (DevPlan, error) {
	var plan DevPlan
	err := json.Unmarshal([]byte(llm.RepairJson(jsonStr)), &plan)
	if err != nil {
		return DevPlan{}, fmt.Errorf("failed to unmarshal json for plan: %v", err)
	}
	return plan, nil
}

func generateDevPlan(dCtx DevContext, chatHistory *[]llm.ChatMessage) (*llm.ChatMessageResponse, error) {
	tools := []*llm.Tool{
		&recordDevPlanTool,
		getRetrieveCodeContextTool(),
		&bulkSearchRepositoryTool,
		&bulkReadFileTool,
	}
	if !dCtx.RepoConfig.DisableHumanInTheLoop {
		tools = append(tools, &getHelpOrInputTool)
	}

	modelConfig := dCtx.GetModelConfig(common.PlanningKey, 0, "default")

	chatOptions := llm.ToolChatOptions{
		Secrets: *dCtx.Secrets,
		Params: llm.ToolChatParams{
			Messages: *chatHistory,
			Tools:    tools,
			ToolChoice: llm.ToolChoice{
				Type: llm.ToolChoiceTypeAuto,
			},
			ModelConfig: modelConfig,
		},
	}

	return TrackedToolChat(dCtx, "dev_plan", chatOptions)
}

// TODO we should determine if the code context is too large programmatically
// instead of depending on the LLM's notion of "too large", which is bound to be
// extremely unreliable
func renderInitialRecordPlanPrompt(dCtx DevContext, codeContext, requirements, planningPrompt string, reproduceIssue bool) string {
	data := map[string]interface{}{
		"codeContext":            codeContext,
		"requirements":           requirements,
		"recordPlanFunctionName": recordDevPlanTool.Name,
		"planningPrompt":         planningPrompt,
		"reproducePrompt":        reproducePrompt,
		"reproduceIssue":         reproduceIssue,
		"editCodeHints":          dCtx.RepoConfig.EditCode.Hints,
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
	return GetUserApproval(dCtx, "dev_plan", req.Content, req.RequestParams)
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

func addDevPlanPrompt(dCtx DevContext, chatHistory *[]llm.ChatMessage, promptInfo PromptInfo) {
	var content string
	role := llm.ChatMessageRoleUser
	cacheControl := ""
	contextType := ""
	switch info := promptInfo.(type) {
	case InitialPlanningInfo:
		content = renderInitialRecordPlanPrompt(dCtx, info.CodeContext, info.Requirements, info.PlanningPrompt, info.ReproduceIssue)
		cacheControl = "ephemeral"
		contextType = ContextTypeInitialInstructions
	case FeedbackInfo:
		content = info.Feedback
	case ToolCallResponseInfo:
		addToolCallResponse(chatHistory, info)
		return
	default:
		panic("Unsupported prompt type for dev plan: " + promptInfo.GetType())
	}
	*chatHistory = append(*chatHistory, llm.ChatMessage{
		Role:         role,
		Content:      content,
		CacheControl: cacheControl,
		ContextType:  contextType,
	})
}
