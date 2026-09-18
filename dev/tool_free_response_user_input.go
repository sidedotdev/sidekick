package dev

import (
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sidekick/llm"
	"strings"
)

type toolFreeResponseUserInputState struct {
	emptyResponses int
	requestCount   int
}

func (state *toolFreeResponseUserInputState) convertToHelpRequest(message common.Message) (common.Message, error) {
	text := message.GetContentString()
	if len(message.GetToolCalls()) > 0 {
		state.emptyResponses = 0
		return message, nil
	}
	if strings.TrimSpace(text) == "" {
		state.emptyResponses++
		if state.emptyResponses < 3 {
			return message, nil
		}
		text = "The model repeatedly returned empty responses and could not continue. Please provide guidance on how to proceed."
	}
	state.emptyResponses = 0
	args, err := json.Marshal(GetHelpOrInputArguments{
		Requests: []HelpOrInputRequest{{
			Content: text,
			SelfHelp: SelfHelp{
				Analysis:              "No self-help is possible; user input is required to continue.",
				Tools:                 []string{},
				AlreadyAttemptedTools: []string{},
			},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("encoding generation help request: %w", err)
	}
	state.requestCount++
	return llm.ChatMessage{
		Role: "assistant",
		ToolCalls: []llm.ToolCall{{
			Id:        fmt.Sprintf("generation-help-%d", state.requestCount),
			Name:      getHelpOrInputTool.Name,
			Arguments: string(args),
		}},
	}, nil
}
