package llm

import (
	"errors"
	"sidekick/secret_manager"

	openai "github.com/sashabaranov/go-openai"
)

func BuildFuncCallInput(chatHistory *[]ChatMessage, mutateHistory bool, tool Tool, prompt string) ToolChatOptions {
	tempChatHistory := make([]ChatMessage, len(*chatHistory))
	copy(tempChatHistory, *chatHistory)

	if prompt != "" {
		newMessage := ChatMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: prompt,
		}

		tempChatHistory = append(tempChatHistory, newMessage)
		if mutateHistory {
			*chatHistory = tempChatHistory
		}
	}

	return ToolChatOptions{
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: &secret_manager.EnvSecretManager{},
		},
		Params: ToolChatParams{
			Messages: tempChatHistory,
			Tools: []*Tool{
				&tool,
			},
			ToolChoice: ToolChoice{
				Type: ToolChoiceTypeRequired,
			},
		},
	}
}

// Only listing the models one might use that have short context lengths
var ShortContextLengthByModel = map[string]int{
	openai.GPT4:     35000,
	openai.GPT40314: 35000,
	openai.GPT40613: 35000,
}

var ErrToolCallUnmarshal = errors.New("failed to unmarshal json, ensure schema is correctly being followed")
