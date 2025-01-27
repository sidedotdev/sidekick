package dev

import (
	"fmt"
	"sidekick/common"
	"sidekick/llm"
	"strings"

	"github.com/invopop/jsonschema"
)

// InformationNeeds represents the structure for the information needs.
type InformationNeeds struct {
	Needs []string `json:"needs"` /* jsonschema:"description=The list of information\\, related keywords\\, concepts etc. that one must know in order to meet the requirements. A bullet point list in the form of an array of strings (without bullets)."` */
}

// defineInformationNeedsTool is the OpenAI function definition for identifying information needs.
var defineInformationNeedsTool = &llm.Tool{
	Name:        "define_information_needs",
	Description: "Defines the information needs based on the provided requirements and repository summary.",
	Parameters:  (&jsonschema.Reflector{ExpandedStruct: true}).Reflect(&InformationNeeds{}),
}

// TODO /gen use ForceToolCall instead of this function
func llmInputForIdentifyInformationNeeds(dCtx DevContext, chatHistory []llm.ChatMessage, prompt string) llm.ToolChatOptions {
	modelConfig := dCtx.GetModelConfig(common.QueryExpansionKey, 0, "small") // query expansion is an easy task

	chatHistory = append(chatHistory, llm.ChatMessage{
		Role:    "user",
		Content: prompt,
	})

	return llm.ToolChatOptions{
		Secrets: *dCtx.Secrets,
		Params: llm.ToolChatParams{
			Messages:    chatHistory,
			ModelConfig: modelConfig,
			// TODO /gen use a tool for this, after defining the tool more
			// specifically (not just a list of needs, but several different
			// aspects, eg questions to answer by reading code, related
			// concepts, key words to search the codebase, key file names from
			// the input)
			/*
				Tools: []*llm.Tool{ defineInformationNeeds },
				ToolChoice: llm.ToolChoice{
					Type:     llm.ToolChoiceTypeRequired,
				},
			*/
		},
	}
}

func IdentifyInformationNeeds(dCtx DevContext, chatHistory *[]llm.ChatMessage, repoSummary string, requirements string) (InformationNeeds, error) {
	prompt := fmt.Sprintf(`Repository Summary:

%s

What information related to the included repository summary is important to
following the directions below?:

#Start Directions
%s
#End Directions

Respond with only a list of information and concepts. Include in the list all
information and concepts necessary to answer the prompt, including those in the
repo summary and those which the repo summary does not contain. Your response
will be used to create an LLM embedding that will be used in a RAG to find the
appropriate files which are needed to answer the user prompt. There may be many
files not currently included which have more relevant information, so your
response must include the most important concepts and information required to
accurately answer the user prompt. It is okay if the list is long or short,
but err on the side of a longer list so the RAG has more information to work
with. If the requirements are referencing code, list specific class, function,
and variable names.
`, repoSummary, requirements)
	actionName := "Identify Information Needs"
	options := llmInputForIdentifyInformationNeeds(dCtx, *chatHistory, prompt)
	chatResponse, err := TrackedToolChat(dCtx, actionName, options)
	if err != nil {
		return InformationNeeds{}, err
	}

	return InformationNeeds{Needs: strings.Split(chatResponse.ChatMessage.Content, "\n")}, nil
}
