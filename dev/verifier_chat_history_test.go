package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func verifierTestText(text, contextType string) llm2.Message {
	block := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: text}
	if contextType != "" {
		persisted_ai.SetContextType(&block, contextType)
	}
	return llm2.Message{Role: llm2.RoleUser, Content: []llm2.ContentBlock{block}}
}

func verifierTestCall(id, name, arguments, result string) []llm2.Message {
	return []llm2.Message{
		{
			Role: llm2.RoleAssistant,
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeToolUse,
				ToolUse: &llm2.ToolUseBlock{
					Id: id, Name: name, Arguments: arguments,
				},
			}},
		},
		{
			Role: llm2.RoleUser,
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeToolResult,
				ToolResult: &llm2.ToolResultBlock{
					ToolCallId: id,
					Name:       name,
					Content:    llm2.TextContentBlocks(result),
				},
			}},
		},
	}
}

func TestVerifierChatHistorySelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		message     llm2.Message
		wantText    bool
		wantHuman   bool
		wantRecords int
	}{
		{
			name:    "initial instructions excluded",
			message: verifierTestText("original task", ContextTypeInitialInstructions),
		},
		{
			name: "ordinary assistant prose excluded",
			message: llm2.Message{
				Role: llm2.RoleAssistant, Content: llm2.TextContentBlocks("I finished everything"),
			},
		},
		{
			name:    "user role alone does not prove human origin",
			message: verifierTestText("automatically generated reminder", ""),
		},
		{
			name:      "human guidance protected",
			message:   verifierTestText("Do not change the public API", ContextTypeUserFeedback),
			wantText:  true,
			wantHuman: true,
		},
		{
			name:        "edit report included",
			message:     verifierTestText("Applied edit block 7", ContextTypeEditBlockReport),
			wantText:    true,
			wantRecords: 1,
		},
		{
			name:        "test report included",
			message:     verifierTestText("Three tests failed", ContextTypeTestResult),
			wantText:    true,
			wantRecords: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var index verifierChatHistoryIndex
			chatHistory := prepareVerifierMessages([]llm2.Message{tt.message}, &index, DefaultVerifierSettings(), "")
			text := chatHistory.Text
			for _, human := range chatHistory.HumanMessages {
				text += human.GetContentString()
			}
			if tt.wantText {
				assert.Contains(t, text, tt.message.GetContentString())
			} else {
				assert.NotContains(t, text, tt.message.GetContentString())
			}
			assert.Equal(t, tt.wantHuman, len(chatHistory.HumanMessages) > 0)
			assert.Len(t, chatHistory.Records, tt.wantRecords)
		})
	}
}

func TestVerifierChatHistoryRecentResultPreviews(t *testing.T) {
	t.Parallel()

	for _, previewLimit := range []int{0, 1000} {
		t.Run(fmt.Sprintf("preview limit %d", previewLimit), func(t *testing.T) {
			t.Parallel()
			var messages []llm2.Message
			for i := 1; i <= 22; i++ {
				messages = append(messages, verifierTestCall(
					fmt.Sprintf("call-%d", i), "read_file",
					fmt.Sprintf(`{"path":"file-%d.go"}`, i),
					fmt.Sprintf("result-%02d-", i)+strings.Repeat("x", 1200),
				)...)
			}
			settings := DefaultVerifierSettings()
			settings.ToolResultMaxChars = previewLimit
			settings.ChatHistoryMaxSize = 100000
			var index verifierChatHistoryIndex
			chatHistory := prepareVerifierMessages(messages, &index, settings, "")

			require.Len(t, chatHistory.Records, 22)
			for i := 1; i <= 22; i++ {
				assert.Contains(t, chatHistory.Text, fmt.Sprintf("file-%d.go", i))
			}
			assert.NotContains(t, chatHistory.Text, "result-01-")
			assert.NotContains(t, chatHistory.Text, "result-02-")
			if previewLimit == 0 {
				assert.NotContains(t, chatHistory.Text, "result-22-")
			} else {
				assert.Contains(t, chatHistory.Text, "result-03-")
				assert.Contains(t, chatHistory.Text, "result-22-")
				assert.NotContains(t, chatHistory.Text, strings.Repeat("x", 1000))
			}
			assert.Contains(t, chatHistory.Text, "expand")
		})
	}
}

func TestVerifierChatHistoryProtectsHumanOutsideBudget(t *testing.T) {
	t.Parallel()

	human := verifierTestText(strings.Repeat("Keep compatibility. ", 500), ContextTypeUserFeedback)
	messages := []llm2.Message{
		verifierTestText(strings.Repeat("Applied an edit. ", 500), ContextTypeEditBlockReport),
		human,
	}
	messages = append(messages, verifierTestCall(
		"shell", "run_command", `{"command":"go test ./..."}`,
		"Command executed with exit status 1\n\nStdout:\n"+strings.Repeat("failure ", 500),
	)...)

	for _, budget := range []int{0, 200, 1000} {
		t.Run(fmt.Sprintf("budget %d", budget), func(t *testing.T) {
			t.Parallel()
			settings := DefaultVerifierSettings()
			settings.ChatHistoryMaxSize = budget
			var index verifierChatHistoryIndex
			chatHistory := prepareVerifierMessages(messages, &index, settings, "")
			require.Equal(t, []llm2.Message{human}, chatHistory.HumanMessages)
			assert.LessOrEqual(t, chatHistory.Size, budget)
			assert.Equal(t, len(chatHistory.Text), chatHistory.Size)
			assert.Equal(t, strings.Repeat("Applied an edit. ", 500), messages[0].GetContentString())
		})
	}
}

func TestVerifierChatHistoryStableHexIDsAndUnavailableRecords(t *testing.T) {
	t.Parallel()

	var messages []llm2.Message
	for i := 1; i <= 16; i++ {
		messages = append(messages, verifierTestCall(fmt.Sprintf("call-%d", i), "read_file", "{}", "ok")...)
	}
	var index verifierChatHistoryIndex
	first := prepareVerifierMessages(messages, &index, DefaultVerifierSettings(), "")
	require.Len(t, first.Records, 16)
	assert.Equal(t, "F", first.Records[14].ID)
	assert.Equal(t, "10", first.Records[15].ID)

	second := prepareVerifierMessages(messages[28:], &index, DefaultVerifierSettings(), "")
	require.Len(t, second.Records, 2)
	assert.Equal(t, "F", second.Records[0].ID)
	assert.Equal(t, "10", second.Records[1].ID)

	record, err := second.Lookup("0F")
	require.NoError(t, err)
	assert.Equal(t, "F", record.ID)

	_, err = second.Lookup("1")
	require.ErrorContains(t, err, "no longer available")
	for _, id := range []string{"0", "-1", "not-hex", "0xF", "FFFFFFFFFFFFFFFFFFFFFFFF"} {
		_, err := second.Lookup(id)
		require.Error(t, err, id)
	}

	next := append(messages[28:], verifierTestCall("new-call", "read_file", "{}", "new")...)
	third := prepareVerifierMessages(next, &index, DefaultVerifierSettings(), "")
	require.Len(t, third.Records, 3)
	assert.Equal(t, "11", third.Records[2].ID)
}

func TestVerifierChatHistoryReportsIgnorePreviewLimits(t *testing.T) {
	t.Parallel()

	settings := DefaultVerifierSettings()
	settings.ToolResultMaxChars = 0
	settings.RecentToolResultsCount = 0
	report := strings.Repeat("Applied edit successfully. ", 80)
	var index verifierChatHistoryIndex
	chatHistory := prepareVerifierMessages(
		[]llm2.Message{verifierTestText(report, ContextTypeEditBlockReport)},
		&index, settings, "",
	)
	assert.Contains(t, chatHistory.Text, report)
	require.Len(t, chatHistory.Records, 1)
}

func TestVerifierShortenSmallCaps(t *testing.T) {
	t.Parallel()

	original := strings.Repeat("café ", 30)
	for _, limit := range []int{0, 1, 5, 10, 30, 64} {
		t.Run(fmt.Sprintf("cap %d", limit), func(t *testing.T) {
			t.Parallel()
			shortened := verifierShorten(original, limit, "F")
			assert.LessOrEqual(t, len(shortened), limit)
		})
	}
}

func TestVerifierChatHistoryCompactsFieldsIndependently(t *testing.T) {
	t.Parallel()

	request := strings.Repeat("request-data ", 500)
	result := strings.Repeat("result-data ", 500)
	messages := verifierTestCall("large-call", "read_file", request, result)
	settings := DefaultVerifierSettings()
	settings.ChatHistoryMaxSize = 700
	settings.ToolResultMaxChars = 1000
	var index verifierChatHistoryIndex
	chatHistory := prepareVerifierMessages(messages, &index, settings, "")

	require.NotEmpty(t, chatHistory.Text)
	assert.LessOrEqual(t, chatHistory.Size, settings.ChatHistoryMaxSize)
	assert.True(t, strings.HasPrefix(chatHistory.Text, "[1] read_file\nRequest: "))
	parts := strings.Split(chatHistory.Text, "\nResult: ")
	require.Len(t, parts, 2, "compaction must preserve request and result labels")

	fields := []struct {
		rendered string
		original string
	}{
		{strings.TrimPrefix(parts[0], "[1] read_file\nRequest: "), request},
		{parts[1], result},
	}
	for _, field := range fields {
		require.Equal(t, 1, strings.Count(field.rendered, "bytes omitted; expand 1]"))
		noticeStart := strings.LastIndex(field.rendered, "\n[")
		require.GreaterOrEqual(t, noticeStart, 0)
		var omitted int
		_, err := fmt.Sscanf(field.rendered[noticeStart:], "\n[%d bytes omitted; expand 1]", &omitted)
		require.NoError(t, err)
		retained := strings.ReplaceAll(field.rendered[:noticeStart], "[...]", "")
		assert.Equal(t, len(field.original)-len(retained), omitted,
			"the notice must account for the original available field, not an intermediate preview")
	}
}

func TestVerifierChatHistoryAmbiguousUnkeyedReportsAfterTrimming(t *testing.T) {
	t.Parallel()

	report := verifierTestText("Applied the requested edit", ContextTypeEditBlockReport)
	var index verifierChatHistoryIndex
	first := prepareVerifierMessages([]llm2.Message{report, report}, &index, DefaultVerifierSettings(), "")
	require.Len(t, first.Records, 2)
	require.NotEqual(t, first.Records[0].ID, first.Records[1].ID)

	// Either occurrence could have survived; equal content cannot establish identity.
	second := prepareVerifierMessages([]llm2.Message{report}, &index, DefaultVerifierSettings(), "")
	require.Len(t, second.Records, 1)
	for _, previous := range first.Records {
		_, err := second.Lookup(previous.ID)
		require.ErrorContains(t, err, "no longer available")
		assert.NotEqual(t, previous.ID, second.Records[0].ID)
	}
}

func TestVerifierChatHistoryHeredocReduction(t *testing.T) {
	t.Parallel()

	for _, delimiter := range []string{"EOF", "'EOF'", `"EOF"`} {
		t.Run(delimiter, func(t *testing.T) {
			t.Parallel()
			body := strings.Repeat("payload line\n", 500)
			command := "cat <<" + delimiter + " > generated.txt\n" + body + "EOF\ngo test ./..."
			arguments := fmt.Sprintf(`{"command":%q}`, command)
			messages := verifierTestCall("heredoc", "run_command", arguments,
				"Command executed with exit status 0")
			settings := DefaultVerifierSettings()
			settings.ChatHistoryMaxSize = 1000
			var index verifierChatHistoryIndex
			chatHistory := prepareVerifierMessages(messages, &index, settings, "")
			assert.Contains(t, chatHistory.Text, "generated.txt")
			assert.Contains(t, chatHistory.Text, "go test ./...")
			assert.NotContains(t, chatHistory.Text, "payload line")
			assert.Contains(t, chatHistory.Text, "expand 1")
			assert.Contains(t, chatHistory.Text, "exit status 0")
			record, err := chatHistory.Lookup("1")
			require.NoError(t, err)
			assert.Equal(t, arguments, record.Request)
			assert.Equal(t, messages, record.Messages)
		})
	}
}

func TestVerifierChatHistoryStructuredResultPreserved(t *testing.T) {
	t.Parallel()

	messages := verifierTestCall("image-call", "read_image", `{"path":"plot.png"}`, "image loaded")
	messages[1].Content[0].ToolResult.IsError = true
	persisted_ai.SetBlockKey(&messages[1].Content[0], "original-result-key")
	settings := DefaultVerifierSettings()
	settings.ToolResultMaxChars = 0
	var index verifierChatHistoryIndex
	chatHistory := prepareVerifierMessages(messages, &index, settings, "")
	record, err := chatHistory.Lookup("01")
	require.NoError(t, err)
	assert.Equal(t, messages, record.Messages)
	assert.NotContains(t, chatHistory.Text, "image loaded")
}

func TestVerifierChatHistoryPreservesProviderCompanionsAndImages(t *testing.T) {
	t.Parallel()

	assistant := llm2.Message{
		Role: llm2.RoleAssistant,
		Content: []llm2.ContentBlock{
			{
				Id:   "reasoning-reference",
				Type: llm2.ContentBlockTypeReasoning,
				Reasoning: &llm2.ReasoningBlock{
					EncryptedContent: "opaque-reasoning",
					Signature:        []byte("reasoning-signature"),
				},
			},
			{
				Type: llm2.ContentBlockTypeText,
				Text: "Ordinary assistant prose must not enter compact chat history.",
			},
			{
				Id:        "builtin-reference",
				Type:      llm2.ContentBlockTypeMcpCall,
				Signature: []byte("provider-signature"),
				McpCall: &llm2.McpCallBlock{
					Server: "provider", Tool: "search", Arguments: `{"query":"example"}`,
				},
			},
		},
	}
	messages := verifierTestCall("image-call", "read_image", `{"path":"plot.png"}`, "image loaded")
	messages[1].Content[0].ToolResult.Content = append(
		messages[1].Content[0].ToolResult.Content,
		llm2.ContentBlock{
			Type:  llm2.ContentBlockTypeImage,
			Image: &llm2.ImageRef{Url: "data:image/png;base64,aW1hZ2U="},
		},
	)
	messages = append(messages, assistant)
	var index verifierChatHistoryIndex
	chatHistory := prepareVerifierMessages(messages, &index, DefaultVerifierSettings(), "")
	require.Len(t, chatHistory.Records, 2)
	assert.Equal(t, messages[:2], chatHistory.Records[0].Messages)
	assert.Equal(t, []llm2.Message{{
		Role:    llm2.RoleAssistant,
		Content: []llm2.ContentBlock{assistant.Content[2]},
	}}, chatHistory.Records[1].Messages)
	assert.NotContains(t, chatHistory.Text, "Ordinary assistant prose")
	assert.NotContains(t, chatHistory.Text, "opaque-reasoning")
	assert.Contains(t, chatHistory.Text, "provider/search")
}

func TestVerifierChatHistoryHeredocStaysRemovedDuringFurtherCompaction(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("HEREDOC_PAYLOAD\n", 500)
	command := "cat <<EOF > generated.txt\n" + body + "EOF\nprintf '%s' '" +
		strings.Repeat("long-argument ", 500) + "'\ngo test ./..."
	messages := verifierTestCall("heredoc-long", "run_command",
		fmt.Sprintf(`{"command":%q}`, command), "Command executed with exit status 0")
	messages = append(messages, verifierTestText(
		strings.Repeat("Automated report detail. ", 500), ContextTypeEditBlockReport,
	))
	settings := DefaultVerifierSettings()
	settings.ChatHistoryMaxSize = 900
	var index verifierChatHistoryIndex
	chatHistory := prepareVerifierMessages(messages, &index, settings, "")

	require.Contains(t, chatHistory.Text, "[1] run_command")
	assert.NotContains(t, chatHistory.Text, "HEREDOC_PAYLOAD")
	assert.Contains(t, chatHistory.Text, "go test ./...")
	assert.LessOrEqual(t, chatHistory.Size, settings.ChatHistoryMaxSize)
	request := strings.SplitN(strings.SplitN(chatHistory.Text, "\nRequest: ", 2)[1], "\nResult: ", 2)[0]
	assert.Equal(t, 1, strings.Count(request, "bytes omitted; expand 1]"))
	noticeStart := strings.LastIndex(request, "\n[")
	require.GreaterOrEqual(t, noticeStart, 0)
	var omitted int
	_, err := fmt.Sscanf(request[noticeStart:], "\n[%d bytes omitted; expand 1]", &omitted)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, omitted, len(body))
	assert.Equal(t, messages[0].Content[0].ToolUse.Arguments, chatHistory.Records[0].Request)
}

func TestPrepareVerifierHistoryCurrentReferences(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	storage := sqlite.NewTestSqliteStorage(t, "verifier_history")
	activities := VerifierHistoryActivities{Storage: storage}
	messages := verifierTestCall("persisted-call", "read_file", `{"path":"current.go"}`, "current content")
	history := persisted_ai.NewLlm2ChatHistory("flow", "workspace")
	for i := range messages {
		history.Append(&messages[i])
	}
	require.NoError(t, history.Persist(ctx, storage, persisted_ai.NewKsuidGenerator()))
	data, err := json.Marshal(&persisted_ai.ChatHistoryContainer{History: history})
	require.NoError(t, err)
	var current persisted_ai.ChatHistoryContainer
	require.NoError(t, json.Unmarshal(data, &current))

	first, err := activities.PrepareVerifierHistory(ctx, PrepareVerifierHistoryInput{
		ChatHistory: &current, FlowId: "flow", WorkspaceId: "workspace",
		Settings: DefaultVerifierSettings(),
	})
	require.NoError(t, err)
	require.Len(t, first.PreparedChatHistory.Records, 1)
	assert.Contains(t, first.PreparedChatHistory.Text, "current content")

	var missing persisted_ai.ChatHistoryContainer
	require.NoError(t, json.Unmarshal([]byte(`{"type":"llm2","refs":[{"role":"assistant","blockKeys":["missing-block"]}],"flowId":"flow","workspaceId":"workspace"}`), &missing))
	second, err := activities.PrepareVerifierHistory(ctx, PrepareVerifierHistoryInput{
		ChatHistory: &missing, FlowId: "flow", WorkspaceId: "workspace",
		Index: first.Index, Settings: DefaultVerifierSettings(),
	})
	require.NoError(t, err)
	assert.Empty(t, second.PreparedChatHistory.Records)
	_, err = second.PreparedChatHistory.Lookup(first.PreparedChatHistory.Records[0].ID)
	require.ErrorContains(t, err, "no longer available")
	assert.NotContains(t, second.PreparedChatHistory.Text, "current content")
}

func TestVerifierChatHistoryLateCommandFallback(t *testing.T) {
	t.Parallel()

	command := "printf '" + strings.Repeat("large argument ", 500) + "'; go test ./..."
	messages := verifierTestCall("long-command", "run_command",
		fmt.Sprintf(`{"command":%q}`, command), "Command executed with exit status 0")
	settings := DefaultVerifierSettings()
	settings.ChatHistoryMaxSize = 180
	var index verifierChatHistoryIndex
	chatHistory := prepareVerifierMessages(messages, &index, settings, "")
	require.Contains(t, chatHistory.Text, "[1] run_command")
	assert.Contains(t, chatHistory.Text, "printf")
	assert.Contains(t, chatHistory.Text, "go")
	assert.NotContains(t, chatHistory.Text, "large argument")
	assert.Contains(t, chatHistory.Text, "expand 1")
	assert.LessOrEqual(t, chatHistory.Size, 180)
}

func TestVerifierChatHistoryHumanToolResultOutsideBudget(t *testing.T) {
	t.Parallel()

	messages := verifierTestCall("help", "get_help_or_input", "{}", "Keep the public API unchanged.")
	persisted_ai.SetContextType(&messages[1].Content[0].ToolResult.Content[0], ContextTypeUserFeedback)
	settings := DefaultVerifierSettings()
	settings.ToolResultMaxChars = 0
	settings.ChatHistoryMaxSize = 0
	var index verifierChatHistoryIndex
	chatHistory := prepareVerifierMessages(messages, &index, settings, "")
	require.Len(t, chatHistory.HumanMessages, 1)
	assert.Equal(t, "Keep the public API unchanged.", chatHistory.HumanMessages[0].GetContentString())
	assert.Empty(t, chatHistory.Text)
	require.Len(t, chatHistory.Records, 1)
	assert.Equal(t, messages, chatHistory.Records[0].Messages)
}

func TestVerifierChatHistoryOuterHumanToolMarkerNotDuplicated(t *testing.T) {
	t.Parallel()

	messages := verifierTestCall("help", "get_help_or_input", "{}", "Keep compatibility.")
	persisted_ai.SetContextType(&messages[1].Content[0], ContextTypeUserFeedback)
	persisted_ai.SetContextType(&messages[1].Content[0].ToolResult.Content[0], ContextTypeUserFeedback)
	var index verifierChatHistoryIndex
	chatHistory := prepareVerifierMessages(messages, &index, DefaultVerifierSettings(), "")
	require.Len(t, chatHistory.HumanMessages, 1)
	assert.Equal(t, "Keep compatibility.", chatHistory.HumanMessages[0].GetContentString())
}

func TestVerifierChatHistoryOuterOnlyHumanToolMarker(t *testing.T) {
	t.Parallel()

	messages := verifierTestCall("help", "get_help_or_input", "{}", "Preserve compatibility.")
	persisted_ai.SetContextType(&messages[1].Content[0], ContextTypeUserFeedback)
	settings := DefaultVerifierSettings()
	settings.ChatHistoryMaxSize = 0
	var index verifierChatHistoryIndex
	chatHistory := prepareVerifierMessages(messages, &index, settings, "")
	require.Len(t, chatHistory.HumanMessages, 1)
	require.Len(t, chatHistory.HumanMessages[0].Content, 1)
	assert.Nil(t, chatHistory.HumanMessages[0].Content[0].ToolResult)
	assert.Equal(t, "Preserve compatibility.", chatHistory.HumanMessages[0].GetContentString())
	assert.Equal(t, "Preserve compatibility.", chatHistory.Records[0].Result)
	assert.Empty(t, chatHistory.Text)
}

func TestVerifierChatHistoryFiltersAssistantCommentary(t *testing.T) {
	t.Parallel()

	for _, arguments := range []string{
		`{"analysis":"PRIVATE OPINION","command":"echo ok","nested":{"analysis":"preserve"},"number":9007199254740993}`,
		`{"command":"echo ok"}`,
		`{invalid`,
		`null`,
	} {
		t.Run(arguments, func(t *testing.T) {
			t.Parallel()

			messages := verifierTestCall("call", "run_command", arguments, "command output")
			messages[0].Content = append(messages[0].Content, llm2.ContentBlock{
				Type: llm2.ContentBlockTypeText, Text: "ASSISTANT OPINION",
			})
			before, err := json.Marshal(messages)
			require.NoError(t, err)

			var index verifierChatHistoryIndex
			history := prepareVerifierMessages(messages, &index, DefaultVerifierSettings(), "")
			require.Len(t, history.Records, 1)
			record := history.Records[0]
			require.Len(t, record.Messages, 2)
			require.Len(t, record.Messages[0].Content, 1)
			actual := record.Messages[0].Content[0].ToolUse.Arguments
			if strings.Contains(arguments, "PRIVATE OPINION") {
				assert.JSONEq(t, `{"command":"echo ok","nested":{"analysis":"preserve"},"number":9007199254740993}`, actual)
			} else {
				assert.Equal(t, arguments, actual)
			}
			assert.Equal(t, actual, record.Request)
			assert.Equal(t, messages[1], record.Messages[1])
			encoded, err := json.Marshal(history)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "PRIVATE OPINION")
			assert.NotContains(t, string(encoded), "ASSISTANT OPINION")
			after, err := json.Marshal(messages)
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after))
		})
	}
}

func TestVerifierChatHistoryHelpResultPreview(t *testing.T) {
	t.Parallel()

	for _, marker := range []string{"none", "inner", "outer", "both"} {
		t.Run(marker, func(t *testing.T) {
			t.Parallel()
			answer := "Keep the Git configuration scoped to this repository."
			messages := verifierTestCall("help", "get_help_or_input", "{}", answer)
			if marker == "inner" || marker == "both" {
				persisted_ai.SetContextType(&messages[1].Content[0].ToolResult.Content[0], ContextTypeUserFeedback)
			}
			if marker == "outer" || marker == "both" {
				persisted_ai.SetContextType(&messages[1].Content[0], ContextTypeUserFeedback)
			}
			messages = append(messages, verifierTestCall("read", "read_file", "{}", "ordinary result")...)
			settings := DefaultVerifierSettings()
			settings.RecentToolResultsCount = 0
			settings.ToolResultMaxChars = 0
			var index verifierChatHistoryIndex
			history := prepareVerifierMessages(messages, &index, settings, "")

			assert.Contains(t, history.Text, "Result: "+answer)
			assert.NotContains(t, history.Text, "Result preview unavailable; expand 1")
			assert.NotContains(t, history.Text, "ordinary result")
			record, err := history.Lookup("1")
			require.NoError(t, err)
			assert.Equal(t, messages[:2], record.Messages)
			if marker == "none" {
				assert.Empty(t, history.HumanMessages)
			} else {
				require.Len(t, history.HumanMessages, 1)
				assert.Equal(t, answer, history.HumanMessages[0].GetContentString())
			}
		})
	}
}

func TestVerifierChatHistoryHelpResultPreviewLimit(t *testing.T) {
	t.Parallel()

	answer := "Start of answer. " + strings.Repeat("important guidance ", 200) + " End of answer."
	messages := verifierTestCall("help", "get_help_or_input", "{}", answer)
	persisted_ai.SetContextType(&messages[1].Content[0].ToolResult.Content[0], ContextTypeUserFeedback)
	var index verifierChatHistoryIndex
	history := prepareVerifierMessages(messages, &index, DefaultVerifierSettings(), "")

	parts := strings.SplitN(history.Text, "\nResult: ", 2)
	require.Len(t, parts, 2)
	assert.Contains(t, parts[1], "Start of answer.")
	assert.Contains(t, parts[1], "End of answer.")
	assert.Contains(t, parts[1], "bytes omitted; expand 1")
	assert.LessOrEqual(t, len(parts[1]), 2000)
	assert.Greater(t, len(parts[1]), 1000)
	require.Len(t, history.HumanMessages, 1)
	assert.Equal(t, answer, history.HumanMessages[0].GetContentString())
	record, err := history.Lookup("1")
	require.NoError(t, err)
	assert.Equal(t, messages, record.Messages)
}

func TestVerifierChatHistoryHelpResultConfiguredLimit(t *testing.T) {
	t.Parallel()

	for _, limit := range []int{0, 300, 3000} {
		t.Run(fmt.Sprintf("limit %d", limit), func(t *testing.T) {
			t.Parallel()
			answer := "Answer begins. " + strings.Repeat("guidance ", 500) + " Answer ends."
			messages := verifierTestCall("help", "get_help_or_input", "{}", answer)
			persisted_ai.SetContextType(&messages[1].Content[0].ToolResult.Content[0], ContextTypeUserFeedback)
			settings := DefaultVerifierSettings()
			settings.HelpResultMaxChars = limit
			var index verifierChatHistoryIndex
			history := prepareVerifierMessages(messages, &index, settings, "")
			parts := strings.SplitN(history.Text, "\nResult: ", 2)
			require.Len(t, parts, 2)
			assert.Contains(t, parts[1], "bytes omitted; expand 1")
			if limit == 0 {
				assert.NotContains(t, parts[1], "Answer begins.")
			} else {
				assert.Contains(t, parts[1], "Answer begins.")
				assert.Contains(t, parts[1], "Answer ends.")
				assert.LessOrEqual(t, len(parts[1]), limit)
				assert.Greater(t, len(parts[1]), limit-20)
			}
			require.Len(t, history.HumanMessages, 1)
			assert.Equal(t, answer, history.HumanMessages[0].GetContentString())

			settings.ChatHistoryMaxSize = 250
			history = prepareVerifierMessages(messages, &index, settings, "")
			assert.LessOrEqual(t, history.Size, 250)
			require.Len(t, history.HumanMessages, 1)
			assert.Equal(t, answer, history.HumanMessages[0].GetContentString())
		})
	}
}

func TestVerifierChatHistoryDefaultExcludesAutoReview(t *testing.T) {
	t.Parallel()

	messages := []llm2.Message{
		verifierTestText("previous automatic evaluation", ContextTypeAutoReviewFeedback),
		verifierTestText("applied edits", ContextTypeEditBlockReport),
		verifierTestText("workflow test results", ContextTypeTestResult),
		verifierTestText("existing summary", ContextTypeSummary),
	}
	var index verifierChatHistoryIndex
	history := prepareVerifierMessages(messages, &index, DefaultVerifierSettings(), "")

	require.Len(t, history.Records, 3)
	assert.NotContains(t, history.Text, "previous automatic evaluation")
	assert.Empty(t, history.HumanMessages)
	for _, record := range history.Records {
		assert.NotEqual(t, ContextTypeAutoReviewFeedback, record.Name)
	}
	assert.Contains(t, history.Text, "applied edits")
	assert.Contains(t, history.Text, "workflow test results")
	assert.Contains(t, history.Text, "existing summary")
}

func TestVerifierChatHistoryConfiguredReports(t *testing.T) {
	t.Parallel()
	for _, contextTypes := range [][]string{
		{},
		{ContextTypeAutoReviewFeedback},
		{ContextTypeEditBlockReport, ContextTypeTestResult, ContextTypeAutoReviewFeedback, ContextTypeSummary},
	} {
		t.Run(fmt.Sprint(contextTypes), func(t *testing.T) {
			t.Parallel()
			settings := DefaultVerifierSettings()
			settings.ContextTypes = contextTypes
			var messages []llm2.Message
			for _, marker := range []string{
				ContextTypeEditBlockReport, ContextTypeTestResult, ContextTypeAutoReviewFeedback, ContextTypeSummary,
			} {
				messages = append(messages, verifierTestText(marker, marker))
			}
			var index verifierChatHistoryIndex
			history := prepareVerifierMessages(messages, &index, settings, "")
			var names []string
			for _, record := range history.Records {
				names = append(names, record.Name)
			}
			require.ElementsMatch(t, contextTypes, names)
		})
	}
}
