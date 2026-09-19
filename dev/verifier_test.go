package dev

import (
	"testing"

	"sidekick/common"
	"sidekick/llm2"

	"github.com/stretchr/testify/require"
)

func TestVerifierExpansionBatch(t *testing.T) {
	t.Parallel()

	original := verifierTestCall("original", "read_image", `{"path":"plot.png"}`, "available result")
	original[0].Content[0].Signature = []byte("provider-signature")
	original[1].Content[0].ToolResult.Content = append(original[1].Content[0].ToolResult.Content,
		llm2.ContentBlock{
			Type:  llm2.ContentBlockTypeImage,
			Image: &llm2.ImageRef{Url: "data:image/png;base64,aW1hZ2U="},
		})
	report := verifierTestText("Applied edits successfully.", ContextTypeEditBlockReport)
	chatHistory := verifierChatHistory{
		Allocated: 16,
		Records: []verifierChatHistoryRecord{
			{ID: "F", Messages: original, IsTool: true},
			{ID: "10", Messages: []llm2.Message{report}},
		},
	}
	result, messages := expandVerifierToolCall(common.ToolCall{
		Id: "expand", Name: "expand_tool_call",
		Arguments: `{"ids":["0F","10","F","1","bogus"]}`,
	}, chatHistory)
	require.Equal(t, "expand", result.ToolCallId)
	require.Contains(t, result.TextContent(), "expanding")
	require.Contains(t, result.TextContent(), "no longer available")
	require.Contains(t, result.TextContent(), "invalid chat history record ID")
	require.Equal(t, append(original, report), messages)
}

func TestVerifierExpansionInvalidArguments(t *testing.T) {
	t.Parallel()

	for _, arguments := range []string{`{`, `{}`, `{"ids":[]}`, `{"ids":[1]}`} {
		t.Run(arguments, func(t *testing.T) {
			t.Parallel()
			result, messages := expandVerifierToolCall(common.ToolCall{
				Id: "expand", Name: "expand_tool_call", Arguments: arguments,
			}, verifierChatHistory{})
			require.True(t, result.IsError)
			require.NotEmpty(t, result.TextContent())
			require.Empty(t, messages)
		})
	}
}

func TestVerifierExpansionBuiltIn(t *testing.T) {
	t.Parallel()

	original := llm2.Message{
		Role: llm2.RoleAssistant,
		Content: []llm2.ContentBlock{
			{
				Id: "reasoning-reference", Type: llm2.ContentBlockTypeReasoning,
				Reasoning: &llm2.ReasoningBlock{EncryptedContent: "opaque"},
			},
			{
				Id: "builtin-reference", Type: llm2.ContentBlockTypeMcpCall,
				Signature: []byte("signature"),
				McpCall:   &llm2.McpCallBlock{Server: "provider", Tool: "search", Arguments: "{}"},
			},
		},
	}
	result, messages := expandVerifierToolCall(common.ToolCall{
		Id: "expand", Name: "expand_tool_call", Arguments: `{"ids":["1"]}`,
	}, verifierChatHistory{
		Allocated: 1,
		Records:   []verifierChatHistoryRecord{{ID: "1", Messages: []llm2.Message{original}, IsTool: true}},
	})
	require.False(t, result.IsError)
	require.Equal(t, "1: expanding", result.TextContent())
	require.Equal(t, []llm2.Message{original}, messages)
}

func TestVerifierLoopExpansionBeforeVerdict(t *testing.T) {
	t.Parallel()

	original := verifierTestCall("original", "run_command", `{"command":"go test ./..."}`, "exit status 0")
	chatHistory := verifierChatHistory{
		Allocated: 2,
		Records:   []verifierChatHistoryRecord{{ID: "2", Messages: original, IsTool: true}},
	}
	var appended []llm2.Message
	turn := 0
	next := func() (common.MessageResponse, error) {
		turn++
		var call common.ToolCall
		switch turn {
		case 1:
			call = common.ToolCall{Id: "missing", Name: "expand_tool_call", Arguments: `{"ids":["1"]}`}
		case 2:
			require.Contains(t, appended[0].Content[0].ToolResult.TextContent(), "no longer available")
			call = common.ToolCall{Id: "expand", Name: "expand_tool_call", Arguments: `{"ids":["02"]}`}
		case 3:
			require.Equal(t, original, appended[2:])
			call = common.ToolCall{Id: "invalid", Name: "determine_criteria_fulfillment", Arguments: `{`}
		case 4:
			require.True(t, appended[len(appended)-1].Content[0].ToolResult.IsError)
			call = common.ToolCall{Id: "verdict", Name: "determine_criteria_fulfillment",
				Arguments: `{"whatWasActuallyDone":"Fixed code.","analysis":"Checks passed.","isFulfilled":true}`}
		default:
			t.Fatal("loop continued after verdict")
		}
		message := llm2.Message{Role: llm2.RoleAssistant}
		message.SetToolCalls([]common.ToolCall{call})
		return &llm2.MessageResponse{Output: message}, nil
	}
	verdict, err := runVerifierLoop(chatHistory, next, func(message *llm2.Message) error {
		appended = append(appended, *message)
		return nil
	})
	require.NoError(t, err)
	require.True(t, verdict.IsFulfilled)
	require.Equal(t, "Fixed code.", verdict.WorkDescription)
	require.Equal(t, 4, turn)
	require.Equal(t, "Determination recorded.", appended[len(appended)-1].Content[0].ToolResult.TextContent())
}

func TestVerifierLoopOverlappingExpansionGroups(t *testing.T) {
	t.Parallel()

	group := verifierTestCall("original", "read_image", "{}", "image")
	group[0].Content[0].Id = "provider-reference"
	group[0].Content[0].Signature = []byte("provider-signature")
	chatHistory := verifierChatHistory{
		Allocated: 2,
		Records: []verifierChatHistoryRecord{
			{ID: "1", Messages: group},
			{ID: "2", Messages: group},
		},
	}
	var appended []llm2.Message
	turn := 0
	_, err := runVerifierLoop(chatHistory, func() (common.MessageResponse, error) {
		turn++
		message := llm2.Message{Role: llm2.RoleAssistant}
		if turn == 1 {
			message.SetToolCalls([]common.ToolCall{
				{Id: "expand-a", Name: "expand_tool_call", Arguments: `{"ids":["1"]}`},
				{Id: "expand-b", Name: "expand_tool_call", Arguments: `{"ids":["2"]}`},
			})
		} else {
			require.Equal(t, group, appended[2:])
			message.SetToolCalls([]common.ToolCall{{
				Id: "verdict", Name: "determine_criteria_fulfillment",
				Arguments: `{"isFulfilled":true}`,
			}})
		}
		return &llm2.MessageResponse{Output: message}, nil
	}, func(message *llm2.Message) error {
		appended = append(appended, *message)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, turn)
}
