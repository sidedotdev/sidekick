package dev

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/llm"
	"sidekick/persisted_ai"

	"github.com/invopop/jsonschema"
	"github.com/sashabaranov/go-openai"
	"go.temporal.io/sdk/workflow"
)

var recordDevRequirementsTool = llm.Tool{
	Name:        "record_dev_requirements",
	Description: "Records functional and non-functional product requirements for software developers to consume.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&DevRequirements{}),
}

/* Represents low-level requirements for a software engineer to implement a feature or fix a bug etc */
type DevRequirements struct {
	Overview string `json:"overview" jsonschema:"description=Overview of the product requirements"`
	// Move UserRequirements to a higher-level ProductRequirements struct, which is expected to require multiple tasks, each of which will later have its own DevRequirements
	//UserRequirements []string `json:"user_facing_requirements" jsonschema:"description=List of all product requirements\\: A brief\\, unambiguous specification of the user-facing behavior of the product\\, with sufficient detail for a senior software engineer to start tech design and/or development\\, but no more. Does not specify implementation details or steps."`
	AcceptanceCriteria []string `json:"acceptance_criteria" jsonschema:"description=Ordered acceptance criteria\\, in markdown without headings and with a maximum of one level of nesting with an unordered list denoted by '    -'. Each acceptance criterium is a specific condition that must be met before the requirements are considered completely satisfied. This creates a brief\\, unambiguous specification\\, with sufficient detail for a senior software engineer to start development"`
	Complete           bool     `json:"are_requirements_complete" jsonschema:"description=Are the product requirements complete and all ambiguity resolved by them?"`
	Learnings          []string `json:"learnings" jsonschema:"description=List of learnings that will help the software engineer fulfill the requirements. It also contains learnings that will help us refine product requirements in the future."`
}

//type ProjectRequirement struct {
//	Requirement string `json:"requirement" jsonschema:"description=A single\\, specific requirement. Brief\\, unambiguous specification\\, with sufficient detail for a senior software engineer to start tech design and/or development"`
//	//Priority    string `json:"priority" jsonschema:"enum=P0,enum=P1,enum=P2,description=P0: non-negotiable i.e. must be done\\, P1: not blocking if it's hard to achieve\\, P2: nice to have"`
//}

var markdownListPattern = regexp.MustCompile(`^\s*(-|\d+\.|[a-z]\.|[iv]+\.)\s+`)

func (r DevRequirements) String() string {
	var criteria string
	var learnings string
	for _, ac := range r.AcceptanceCriteria {
		if markdownListPattern.MatchString(ac) {
			criteria += fmt.Sprintf("%s\n", ac)
		} else {
			criteria += fmt.Sprintf("- %s\n", ac)
		}
	}
	for _, learning := range r.Learnings {
		if markdownListPattern.MatchString(learning) {
			learnings += fmt.Sprintf("%s\n", learning)
		} else {
			learnings += fmt.Sprintf("- %s\n", learning)
		}
	}
	return fmt.Sprintf("#### Overview:\n%s\n\n#### Acceptance Criteria:\n%s\n\n#### Learnings:\n%s", r.Overview, criteria, learnings)
}

type buildDevRequirementsState struct {
	contextSizeExtension int
}

func BuildDevRequirements(dCtx DevContext, initialInfo InitialDevRequirementsInfo) (*DevRequirements, error) {
	return RunSubflow(dCtx, "dev_requirements", "Dev Requirements", func(_ domain.Subflow) (*DevRequirements, error) {
		return buildDevRequirementsSubflow(dCtx, initialInfo)
	})
}

func buildDevRequirementsSubflow(dCtx DevContext, initialInfo InitialDevRequirementsInfo) (*DevRequirements, error) {
	// Step 1: prepare code context + retrieve mission
	contextSizeExtension := 0
	if initialInfo.Context == "" {
		codeContext, fullCodeContext, err := PrepareInitialCodeContext(dCtx, initialInfo.Requirements, nil, nil)
		initialInfo.Context = codeContext
		contextSizeExtension = len(fullCodeContext) - len(codeContext)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare code context: %v", err)
		}
	}
	if initialInfo.Mission == "" {
		initialInfo.Mission = dCtx.RepoConfig.Mission
	}

	// Step 2: run the dev requirements loop
	chatHistory := &[]llm.ChatMessage{}
	addDevRequirementsPrompt(chatHistory, initialInfo)
	initialState := &buildDevRequirementsState{
		contextSizeExtension: contextSizeExtension,
	}

	feedbackIterations := 5
	v := workflow.GetVersion(dCtx, "dev-requirements-feedback-iterations", workflow.DefaultVersion, 1)
	if v == 1 {
		// TODO when tool calls are not finding things automatically, provide
		// better hints for how to find things after Nth iteration, before going
		// to human-based support. Eg fuzzy search or embedding search etc.
		// Maybe provide that as a tool or even run that tool automatically.
		feedbackIterations = 9
	}
	return LlmLoop(dCtx, chatHistory, buildDevRequirementsIteration, WithInitialState(initialState), WithFeedbackEvery(feedbackIterations))
}

func buildDevRequirementsIteration(iteration *LlmIteration) (*DevRequirements, error) {
	state, ok := iteration.State.(*buildDevRequirementsState)
	if !ok {
		return nil, fmt.Errorf("Invalid llm iteration state type, expected *buildDevRequirementsState: %v", iteration.State)
	}

	maxLength := min(defaultMaxChatHistoryLength+state.contextSizeExtension, extendedMaxChatHistoryLength)
	ManageChatHistory(iteration.ExecCtx.Context, iteration.ChatHistory, maxLength)

	var chatResponse *llm.ChatMessageResponse
	var err error
	if v := workflow.GetVersion(iteration.ExecCtx, "dev-requirements-cleanup-cancel-internally", workflow.DefaultVersion, 1); v == 1 {
		chatResponse, err = generateDevRequirements(iteration.ExecCtx, iteration.ChatHistory)
	} else {
		// old version: new one does this in outer LlmLoop
		chatCtx := iteration.ExecCtx.WithCancelOnPause()
		chatResponse, err = generateDevRequirements(chatCtx, iteration.ChatHistory)
		if iteration.ExecCtx.GlobalState != nil && iteration.ExecCtx.GlobalState.Paused {
			return nil, nil // continue the loop: UserRequestIfPaused will handle the pause
		}
	}
	if err != nil {
		return nil, err
	}
	*iteration.ChatHistory = append(*iteration.ChatHistory, chatResponse.ChatMessage)

	if len(chatResponse.ToolCalls) > 0 {
		toolCall := chatResponse.ToolCalls[0] // parallel tool calls are not supported
		if toolCall.Name == recordDevRequirementsTool.Name {
			devReq, unmarshalErr := unmarshalDevRequirements(chatResponse.ToolCalls[0].Arguments)
			if unmarshalErr == nil && devReq.Complete {
				userResponse, approveErr := ApproveDevRequirements(iteration.ExecCtx, devReq)
				if approveErr != nil {
					return nil, fmt.Errorf("error approving dev requirements: %v", approveErr)
				}
				iteration.NumSinceLastFeedback = 0
				if userResponse.Approved != nil && *userResponse.Approved {
					return &devReq, nil // break the loop with the final result
				} else {
					feedback := "Requirements were not approved and therefore not recorded. Please try again, taking this feedback into account:\n\n" + userResponse.Content
					toolResponse := ToolCallResponseInfo{Response: feedback, FunctionName: toolCall.Name, ToolCallId: toolCall.Id}
					addToolCallResponse(iteration.ChatHistory, toolResponse)
				}
			} else if unmarshalErr != nil {
				toolResponse := ToolCallResponseInfo{Response: unmarshalErr.Error(), FunctionName: recordDevRequirementsTool.Name, ToolCallId: toolCall.Id, IsError: true}
				addToolCallResponse(iteration.ChatHistory, toolResponse)
			} else {
				toolResponse := ToolCallResponseInfo{Response: "Recorded partial requirements, but requirements are not complete yet based on the \"are_requirements_complete\" boolean field value being set to false. Do some more research or thinking or get help/input to complete the plan, as needed. Once the planning is complete, record the plan again in full.", FunctionName: recordDevPlanTool.Name, ToolCallId: toolCall.Id}
				addToolCallResponse(iteration.ChatHistory, toolResponse)
			}
		} else {
			var toolCallResult ToolCallResponseInfo
			toolCallResult, err = handleToolCall(iteration.ExecCtx, toolCall)
			if err != nil {
				return nil, fmt.Errorf("error handling tool call: %w", err)
			}

			addToolCallResponse(iteration.ChatHistory, toolCallResult)

			if len(toolCallResult.Response) > 5000 {
				state.contextSizeExtension += len(toolCallResult.Response) - 5000
			}

			if toolCall.Name == getHelpOrInputTool.Name {
				iteration.NumSinceLastFeedback = 0
			}
		}
	} else if chatResponse.StopReason == string(openai.FinishReasonStop) || chatResponse.StopReason == string(openai.FinishReasonToolCalls) {
		// TODO try to extract the dev requirements from the content in this case and treat it as if it was a tool call
		feedbackInfo := FeedbackInfo{Feedback: "Expected a tool call to record the dev requirements, but didn't get it. Embedding the json in the content is not sufficient. Please record the plan via the " + recordDevRequirementsTool.Name + " tool."}
		addDevRequirementsPrompt(iteration.ChatHistory, feedbackInfo)
	} else { // FIXME handle other stop reasons with more specific logic
		//return nil, fmt.Errorf("expected OpenAI chat completion finish reason to be stop or tool calls, got: %v", chatResponse.StopReason)
		// NOTE: we continue the loop instead of failing here so we can attempt to recover
		feedbackInfo := FeedbackInfo{Feedback: "Expected a tool call to record the dev requirements, but didn't get it. Embedding the json in the content is not sufficient. Please record the plan via the " + recordDevRequirementsTool.Name + " tool."}
		addDevRequirementsPrompt(iteration.ChatHistory, feedbackInfo)
	}

	if err != nil {
		return nil, err // break the loop with an error
	}

	return nil, nil // continue the loop
}

func generateDevRequirements(dCtx DevContext, chatHistory *[]llm.ChatMessage) (*llm.ChatMessageResponse, error) {
	tools := []*llm.Tool{
		&recordDevRequirementsTool,
		getRetrieveCodeContextTool(),
		&bulkSearchRepositoryTool,
		&bulkReadFileTool,
	}
	if !dCtx.RepoConfig.DisableHumanInTheLoop {
		tools = append(tools, &getHelpOrInputTool)
	}

	// random order of tools to avoid bias in the LLM's use of the tools
	// NOTE: disabled shuffling as it can mess with cache hit rate
	/*
		rand.Shuffle(len(tools), func(i, j int) {
			tools[i], tools[j] = tools[j], tools[i]
		})
	*/

	modelConfig := dCtx.GetModelConfig(common.PlanningKey, 0, "default")

	options := llm.ToolChatOptions{
		Secrets: *dCtx.Secrets,
		Params: llm.ToolChatParams{
			Messages: *chatHistory,
			Tools:    tools,
			ToolChoice: llm.ToolChoice{
				Type: llm.ToolChoiceTypeAuto, // TODO test with llm.ToolChoiceTypeRequired
			},
			ModelConfig: modelConfig,
		},
	}
	return TrackedToolChat(dCtx, "dev_requirements", options)
}

func TrackedToolChat(dCtx DevContext, actionType string, options llm.ToolChatOptions) (*llm.ChatMessageResponse, error) {
	actionCtx := dCtx.NewActionContext("generate." + actionType)
	actionCtx.ActionParams = options.ActionParams()
	return Track(actionCtx, func(flowAction domain.FlowAction) (*llm.ChatMessageResponse, error) {
		if options.Params.Provider == "" {
			options.Params.ModelConfig = dCtx.GetModelConfig(common.DefaultKey, 0, "default")
		}
		flowId := workflow.GetInfo(dCtx).WorkflowExecution.ID
		chatStreamOptions := persisted_ai.ChatStreamOptions{
			ToolChatOptions: options,
			WorkspaceId:     dCtx.WorkspaceId,
			FlowId:          flowId,
			FlowActionId:    flowAction.Id,
		}
		var chatResponse llm.ChatMessageResponse
		var la *persisted_ai.LlmActivities // use a nil struct pointer to call activities that are part of a structure
		err := PerformWithUserRetry(actionCtx.WithLlmHeartbeatCtx(), la.ChatStream, &chatResponse, chatStreamOptions)
		if err != nil {
			return nil, fmt.Errorf("error during tracked tool chat action '%s': %v", actionType, err)
		}

		return &chatResponse, nil
	})
}

func unmarshalDevRequirements(jsonStr string) (DevRequirements, error) {
	var devRequirements DevRequirements
	err := json.Unmarshal([]byte(llm.RepairJson(jsonStr)), &devRequirements)
	if err != nil {
		return DevRequirements{}, fmt.Errorf("failed to unmarshal dev requirements json: %v", err)
	}
	return devRequirements, nil
}

func addDevRequirementsPrompt(chatHistory *[]llm.ChatMessage, promptInfo PromptInfo) {
	var content string
	role := llm.ChatMessageRoleUser
	cacheControl := ""
	switch info := promptInfo.(type) {
	case InitialDevRequirementsInfo:
		content = getInitialDevRequirementsPrompt(info.Mission, info.Context, info.Requirements)
		cacheControl = "ephemeral"
	case FeedbackInfo:
		content = info.Feedback
	case ToolCallResponseInfo:
		addToolCallResponse(chatHistory, info)
		return
	default:
		panic("Unsupported prompt type for dev requirements: " + promptInfo.GetType())
	}
	*chatHistory = append(*chatHistory, llm.ChatMessage{
		Role:         role,
		Content:      content,
		CacheControl: cacheControl,
	})
}

func addToolCallResponse(chatHistory *[]llm.ChatMessage, info ToolCallResponseInfo) {
	*chatHistory = append(*chatHistory, llm.ChatMessage{
		Role:       llm.ChatMessageRoleTool,
		Content:    info.Response,
		Name:       info.FunctionName,
		ToolCallId: info.ToolCallId,
		IsError:    info.IsError,
	})
}

func ApproveDevRequirements(dCtx DevContext, devReq DevRequirements) (*UserResponse, error) {
	// Create a RequestForUser struct for approval request
	req := RequestForUser{
		Content:       "Please approve or reject these requirements:\n\n" + devReq.String() + "\n\nDo you approve these requirements? If not, please provide feedback on what needs to be changed.",
		RequestParams: map[string]interface{}{"approveTag": "approve_plan", "rejectTag": "reject_plan"},
	}
	return GetUserApproval(dCtx, "dev_requirements", req.Content, req.RequestParams)
}

// TODO we should determine if the context is too large programmatically instead
// of depending on the LLM's notion of "large", which is bound to be
// extremely unreliable
// TODO /gen move these prompts into the prompts mustache format and create
// prompt render functions
func getInitialDevRequirementsPrompt(mission, context, requirements string) string {
	return fmt.Sprintf(`
# CONTEXT

%s

# MISSION

%s

# ROLE

You are a principal product manager.

# OUTPUT

You are to create a set of low-level product requirements for software
developers to consume and use to implement a feature or fix a bug. The
requirements should be complete and unambiguous, and should include learnings
that will help the engineers who will implement the requirements get started
faster.

Don't refer to anything from the chat history that would not make sense out of
context, because these requirements will be used directly, without any of the
associate chat history we had. The final artifact, both the acceptance criteria,
and the learnings, must stand on their own.

# INSTRUCTIONS

Thinking step-by-step, analyze the request. Use it to create a set of low-level
product requirements, keeping in mind the mission of the product and the context
gathered so far.

Steps:

1. Determine areas that require greater clarity.
2. For each area, try to get clarity yourself with available context and tools.
3. Think out loud about questions you would ask the requester. If those
questions are not already answered in the request or via context/tools, ask
them.
4. Consider whether the requirements are now complete, unambiguous and consistent.
5. Record the requirements, even if not yet complete, using the
`+recordDevRequirementsTool.Name+` tool.

The `+getHelpOrInputTool.Name+` tool will allow you to ask the requester and
your colleagues for more information, when required. If the request is already
very clear and unambiguous, you can skip this step, but typically there are
important details that are not mentioned in the initial request that you will
have to ask about before you record a complete set of requirements. You can ask
several questions in one go and should do so if multiple areas need
clarification.

As an ex-software developer, you are able to also read code via the
`+getRetrieveCodeContextTool().Name+` tool to understand how to interpret
technical requirements as well as understand the status quo before suggesting
changes that software engineers will need to implement. Searching the repository
for relevant text which may be strewn across comments or READMEs, using the `+
		bulkSearchRepositoryTool.Name+` tool, may also be helpful.

Consider how the different requirements interact with each other in the context
of the codebase. If the requirements clash with each other in any way, try to
resolve the conflict yourself after considering what option would be best, and
confirm your resolution with the user.

When you have enough information to produce unambiguous requirements, do so via
the `+recordDevRequirementsTool.Name+` tool, including any helpful learnings that
will help the engineers who will implement the requirements get started faster.
Relevant functionality that should be maintained as-is without changes should be
added to learnings rather than acceptance criteria.

If we already have a large amount of code context, but it's not enough to finish
writing the requirements, DO NOT ask for more. Instead, output a partially
complete list of requirements, with specific learnings from the process that
will help you continue where you left off, without having all the original
context again. Include file names and names of functions etc that are relevant
so that we you can easily pick up right where you last left off. Don't include
learnings that we already had recorded in the last incomplete product dev
requirements, if there were any, but add new ones if any.

If there are crucial details in the original request below that need to be kept,
then copy them into either requirements or learnings. Things like code snippets
should be copied verbatim into learnings for instance. The original request and
any chat history will be wiped away with the final new requirements you come up
with, so make sure not to lose any essential details.

# REQUEST

%s`, context, mission, requirements)
}
