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
	Description: "Records technical requirements for software developers to consume.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&DevRequirements{}),
}

/* Represents low-level requirements for a software engineer to implement a feature or fix a bug etc */
type DevRequirements struct {
	Overview string `json:"overview" jsonschema:"description=Overview of the requirements"`
	// Move UserRequirements to a higher-level ProductRequirements struct, which is expected to require multiple tasks, each of which will later have its own DevRequirements
	//UserRequirements []string `json:"user_facing_requirements" jsonschema:"description=List of all product requirements\\: A brief\\, unambiguous specification of the user-facing behavior of the product\\, with sufficient detail for a senior software engineer to start tech design and/or development\\, but no more. Does not specify implementation details or steps."`
	AcceptanceCriteria []string `json:"acceptance_criteria" jsonschema:"description=List of acceptance criteria\\, formatted with markdown including backticks for code references. Each criterium supports a single internal unordered list denoted by '    -'. Each acceptance criterium is a specific condition that must be met before the requirements are considered completely satisfied. This creates a brief\\, unambiguous specification\\, with sufficient detail for a senior software engineer to start development. Limit the length of this list to make it easier to review. Use declarative language reather than imperative."`
	Complete           bool     `json:"requirements_finalized" jsonschema:"description=Are the requirements complete\\, unambiguous and self-consistent?"`
	Learnings          []string `json:"learnings" jsonschema:"description=List of learnings about existing code that will help the software engineer fulfill the requirements. Does not specify approach to solution. It also contains learnings that will help us refine requirements in the future."`
}

// backcompat for rename of are_requirements_complete -> requirements_finalized
func (d *DevRequirements) UnmarshalJSON(data []byte) error {
	type Alias DevRequirements
	aux := &struct {
		*Alias
		CompleteOld *bool `json:"are_requirements_complete"`
		CompleteNew *bool `json:"requirements_finalized"`
	}{
		Alias: (*Alias)(d),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Priority: new field > old field
	if aux.CompleteNew != nil {
		d.Complete = *aux.CompleteNew
	} else if aux.CompleteOld != nil {
		d.Complete = *aux.CompleteOld
	}
	// If neither is present, Complete remains false (zero value)

	return nil
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
				toolResponse := ToolCallResponseInfo{Response: "Recorded partial requirements, but requirements are not finalized yet based on the \"requirements_finalized\" boolean field value being set to false. Do some more research or thinking or get help/input to finalize the requirements, as needed. Then record the finalized requirements again in full.", FunctionName: recordDevPlanTool.Name, ToolCallId: toolCall.Id}
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
	return Track(actionCtx, func(flowAction *domain.FlowAction) (*llm.ChatMessageResponse, error) {
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

Note: this context is not guaranteed to be comprehensive.

# MISSION

%s

# ROLE

You are assisting a user transform their software development task description
into a more complete one with less ambiguous requirements, while providing
clarity around the high-level implementation approach. Your job is to understand
the user's intent through context and questions for the user, and capture it
accurately in a brief, readable manner.

# OUTPUT

Very concise acceptance criteria for the user to review, and for software
developers to consume and use to implement a feature or fix a bug. The
requirements should be complete, unambiguous and self-consistent. Includes
detailed learnings that will help the engineers who will implement the
requirements get started faster.

The final requirements must include anything from the chat history that is
required to understand it out of context, because these requirements will be
used directly, without any of the associated chat history we had. The final
artifacts, both the acceptance criteria, and the learnings, must stand on their
own and must not lose any critical details provided in the task description.

# INSTRUCTIONS

Thinking step-by-step, analyze the task description. Retrieve additional context
if needed, and then ask questions to clarify if needed. Use it to create a set
of concise, yet reasonably comprehensive acceptance criteria, keeping in mind
the mission of the product and the context gathered so far.

Follow these steps precisely:

1. Determine what information is needed to finalize requirements.
2. Try to obtain that information yourself based on existing context and tools.
3. Before recording requirements, you must first write out a list of
assumptions, potential questions and decisions to confirm with the user to
clarify their intent and any aspects of the requirements or the high-level
implementation approach.
4. For each candidate question/decision, determine whether the answer to the
question is consequential. If the end result would be substantially different
based on the answer, the decision is consequential. If the decision relates to
the recommended high-level implementation approach, but other completely
different yet valid approaches exist, it's a consequential decision as well: the
user is the architect & technical designer whose vision you must incorporate.
5. If there are any consequential questions to ask the user, do so via the
`+getHelpOrInputTool.Name+` tool. It is possible but uncommon for there to be no
such questions.
6. Record the requirements, even if not yet completely finalized, using the
`+recordDevRequirementsTool.Name+` tool.

The `+getHelpOrInputTool.Name+` tool will allow you to ask the user for more
information, when required. If the task description is already very clear and
unambiguous in regards to both requirements and approach, you can skip this
step, but usually there are important details that are not mentioned in the
initial task and that can't be inferred with confidence that you will have to
ask about before finalizing requirements. You can ask several questions in one
go and should do so if multiple areas need clarification. Make your questions
concise.

You are able to also read code via the `+getRetrieveCodeContextTool().Name+`
tool to understand how to interpret technical requirements as well as understand
the status quo before suggesting changes that software engineers will need to
implement. Searching the repository for relevant text which may be strewn across
comments or READMEs, using the `+bulkSearchRepositoryTool.Name+` tool, may also
be helpful.

Consider how the different requirements interact with each other in the context
of the codebase. If the requirements clash with each other in any way, try to
resolve the conflict yourself after considering what option would be best, and
confirm your resolution with the user.

When you have enough information to produce unambiguous requirements, do so via
the `+recordDevRequirementsTool.Name+` tool, including any helpful learnings that
will help the engineers who will implement the requirements get started faster.
Existing related functionality that should be maintained as-is without changes
should be added to learnings rather than acceptance criteria.

If we already have a large amount of code context, but it's not enough for fully
comprehensive requirements, DO NOT ask for more. Instead, record a partial list
of requirements, with specific learnings from the process that will help you
continue where you left off. Include file names and names of functions etc that
are relevant so that we you can easily pick up right where you last left off.

If there are crucial details in the original task description below that need to
be kept, then copy them into either requirements or learnings. Things like code
snippets should almost always be copied verbatim into learnings for instance.
The original task description and any chat history will be wiped away with the
final new requirements you come up with, so make sure not to lose any essential
details, examples or code.

DO NOT make any consequential/top-level decisions on behalf of the user without
checking with them, especially if there are multiple potential options, unless
they tell you to choose on their behalf. When multiple good approaches exist and
the codebase does not already have a strong pattern that is obviously preferred,
definitely ask the user what they prefer.

# TASK DESCRIPTION

%s`, context, mission, requirements)
}
