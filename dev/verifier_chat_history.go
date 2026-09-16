package dev

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_bash "github.com/tree-sitter/tree-sitter-bash/bindings/go"

	"sidekick/coding/permission"
	"sidekick/common"
	"sidekick/llm2"
	"sidekick/persisted_ai"
)

// The index retains identities, not payloads belonging to discarded history.
type verifierChatHistoryIndex struct {
	IDs  map[string]int `json:"ids"`
	Next int            `json:"next"`
}

type verifierChatHistoryRecord struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Request  string         `json:"request"`
	Result   string         `json:"result"`
	Messages []llm2.Message `json:"messages"`
	IsTool   bool           `json:"isTool"`
}

type verifierChatHistory struct {
	Text          string                      `json:"text"`
	Size          int                         `json:"size"`
	HumanMessages []llm2.Message              `json:"humanMessages"`
	Records       []verifierChatHistoryRecord `json:"records"`
	Allocated     int                         `json:"allocated"`
}

func (e verifierChatHistory) Lookup(id string) (verifierChatHistoryRecord, error) {
	if id == "" || strings.Trim(id, "0123456789abcdefABCDEF") != "" {
		return verifierChatHistoryRecord{}, fmt.Errorf("invalid chat history record ID %q", id)
	}
	position, err := strconv.ParseUint(id, 16, 63)
	if err != nil || position == 0 {
		return verifierChatHistoryRecord{}, fmt.Errorf("invalid chat history record ID %q", id)
	}
	canonical := fmt.Sprintf("%X", position)
	for _, record := range e.Records {
		if record.ID == canonical {
			return record, nil
		}
	}
	if position <= uint64(e.Allocated) {
		return verifierChatHistoryRecord{}, fmt.Errorf("chat history record %s is no longer available", canonical)
	}
	return verifierChatHistoryRecord{}, fmt.Errorf("unknown chat history record ID %s", canonical)
}

func (index *verifierChatHistoryIndex) identify(key string) string {
	if index.IDs == nil {
		index.IDs = make(map[string]int)
	}
	if strings.HasPrefix(key, "content:") {
		// Equal unkeyed content does not establish identity across history trimming.
		index.Next++
		return fmt.Sprintf("%X", index.Next)
	}
	position, ok := index.IDs[key]
	if !ok {
		index.Next++
		position = index.Next
		index.IDs[key] = position
	}
	return fmt.Sprintf("%X", position)
}

func verifierBlockIdentity(block llm2.ContentBlock) string {
	if block.ToolUse != nil && block.ToolUse.Id != "" {
		return "tool:" + block.ToolUse.Id
	}
	if key := persisted_ai.GetBlockKey(block); key != "" {
		return "block:" + key
	}
	if block.Id != "" {
		return "provider:" + block.Id
	}
	data, _ := json.Marshal(block)
	return fmt.Sprintf("content:%x", sha256.Sum256(data))
}

func prepareVerifierMessages(messages []llm2.Message, index *verifierChatHistoryIndex, settings VerifierSettings, provider string) verifierChatHistory {
	messages = verifierFilterMessages(messages)
	chatHistory := verifierChatHistory{}
	results := make(map[string][]llm2.Message)
	for _, message := range messages {
		for _, block := range message.Content {
			if block.ToolResult != nil {
				id := block.ToolResult.ToolCallId
				results[id] = append(results[id], llm2.Message{Role: message.Role, Content: []llm2.ContentBlock{block}})
				for _, content := range block.ToolResult.Content {
					if persisted_ai.GetContextType(block) == ContextTypeUserFeedback ||
						persisted_ai.GetContextType(content) == ContextTypeUserFeedback {
						chatHistory.HumanMessages = append(chatHistory.HumanMessages, llm2.Message{
							Role: llm2.RoleUser, Content: []llm2.ContentBlock{content},
						})
					}
				}
			}
		}
	}
	occurrences := make(map[string]int)
	for _, message := range messages {
		for _, block := range message.Content {
			contextType := persisted_ai.GetContextType(block)
			if contextType == ContextTypeInitialInstructions {
				continue
			}
			original := llm2.Message{Role: message.Role, Content: []llm2.ContentBlock{block}}
			if contextType == ContextTypeUserFeedback {
				if block.ToolResult == nil {
					chatHistory.HumanMessages = append(chatHistory.HumanMessages, original)
				}
				continue
			}
			record := verifierChatHistoryRecord{Messages: []llm2.Message{original}}
			switch {
			case block.ToolUse != nil:
				record.IsTool = true
				record.Name = block.ToolUse.Name
				record.Request = block.ToolUse.Arguments
				record.Messages = append(record.Messages, results[block.ToolUse.Id]...)
				for _, result := range results[block.ToolUse.Id] {
					for _, resultBlock := range result.Content {
						for _, content := range resultBlock.ToolResult.Content {
							if persisted_ai.GetContextType(resultBlock) != ContextTypeUserFeedback &&
								persisted_ai.GetContextType(content) != ContextTypeUserFeedback {
								record.Result += content.Text
							}
						}
					}
				}
			case block.McpCall != nil:
				record.IsTool = true
				record.Name = block.McpCall.Server + "/" + block.McpCall.Tool
				record.Request = block.McpCall.Arguments
			case contextType == ContextTypeEditBlockReport ||
				contextType == ContextTypeTestResult ||
				contextType == ContextTypeSelfReviewFeedback ||
				contextType == ContextTypeSummary:
				record.Name = contextType
				record.Request = block.Text
			default:
				continue
			}
			if record.IsTool {
				record.Messages = []llm2.Message{message}
				for _, companion := range message.Content {
					if companion.ToolUse != nil {
						record.Messages = append(record.Messages, results[companion.ToolUse.Id]...)
					}
				}
			}
			key := verifierBlockIdentity(block)
			occurrences[key]++
			key = fmt.Sprintf("%s:%d", key, occurrences[key])
			record.ID = index.identify(key)
			chatHistory.Records = append(chatHistory.Records, record)
		}
	}
	chatHistory.Allocated = index.Next
	toolCount := 0
	for _, record := range chatHistory.Records {
		if record.IsTool {
			toolCount++
		}
	}
	displays := make([]string, len(chatHistory.Records))
	reducedRequests := make([]string, len(chatHistory.Records))
	heredocOmissions := make([]int, len(chatHistory.Records))
	reduceHeredocs := false
	render := func(fieldLimit int) {
		toolPosition := 0
		for i, record := range chatHistory.Records {
			result := ""
			if record.IsTool {
				toolPosition++
				if toolPosition > toolCount-max(0, settings.RecentToolResultsCount) && settings.ToolResultMaxChars > 0 {
					result = verifierShorten(record.Result, min(fieldLimit, settings.ToolResultMaxChars), record.ID)
				}
				if result == "" && record.Result != "" {
					result = verifierOmission(len(record.Result), record.ID)
				}
			}
			header := fmt.Sprintf("[%s] %s", record.ID, record.Name)
			if record.Name == "run_command" {
				if status := verifierExitStatus.FindString(record.Result); status != "" {
					header += " (" + status + ")"
				}
			}
			request := verifierShorten(record.Request, fieldLimit, record.ID)
			if reduceHeredocs && heredocOmissions[i] > 0 {
				request = verifierShortenWithOmissions(reducedRequests[i], fieldLimit, record.ID, heredocOmissions[i])
			}
			if fieldLimit <= 64 && record.Name == "run_command" {
				if invoked := verifierInvokedCommands(record.Request); invoked != "" {
					request = verifierShortenWithOmissions(invoked, fieldLimit, record.ID, max(0, len(record.Request)-len(invoked)))
				}
			}
			if request == "" && record.Request != "" {
				request = verifierOmission(len(record.Request), record.ID)
			}
			displays[i] = header + "\nRequest: " + request
			if result != "" {
				displays[i] += "\nResult: " + result
			} else if record.IsTool {
				displays[i] += "\nResult preview unavailable; expand " + record.ID
			}
		}
	}
	render(int(^uint(0) >> 1))
	measure := func() int {
		return persisted_ai.MeasureLlm2Messages(provider, []llm2.Message{{
			Role: llm2.RoleUser, Content: llm2.TextContentBlocks(strings.Join(displays, "\n\n")),
		}})
	}
	budget := max(0, settings.ChatHistoryMaxSize)
	if measure() > budget {
		reduceHeredocs = true
		for i, record := range chatHistory.Records {
			if record.Name == "run_command" {
				reducedRequests[i], heredocOmissions[i] = verifierRemoveHeredocs(record.Request)
			}
		}
		render(int(^uint(0) >> 1))
	}
	for limit := 2048; measure() > budget && limit >= 64; limit /= 2 {
		render(limit)
	}
	for i := 0; measure() > budget && i < len(displays); i++ {
		displays[i] = ""
	}
	var retained []string
	for _, display := range displays {
		if display != "" {
			retained = append(retained, display)
		}
	}
	chatHistory.Text = strings.Join(retained, "\n\n")
	chatHistory.Size = persisted_ai.MeasureLlm2Messages(provider, []llm2.Message{{
		Role: llm2.RoleUser, Content: llm2.TextContentBlocks(chatHistory.Text),
	}})
	return chatHistory
}

var verifierExitStatus = regexp.MustCompile(`Command executed with exit status -?\d+`)

func verifierOmission(amount int, id string) string {
	return fmt.Sprintf("\n[%d bytes omitted; expand %s]", amount, id)
}

func verifierShorten(text string, limit int, id string) string {
	return verifierShortenWithOmissions(text, limit, id, 0)
}

func verifierShortenWithOmissions(text string, limit int, id string, previouslyOmitted int) string {
	notice := ""
	if previouslyOmitted > 0 {
		notice = verifierOmission(previouslyOmitted, id)
	}
	if len(text)+len(notice) <= limit {
		return text + notice
	}
	noticeSize := len(verifierOmission(previouslyOmitted+len(text), id))
	if limit < noticeSize+5 {
		return ""
	}
	available := limit - noticeSize - 5
	left := available / 2
	right := available - left
	for left > 0 && !utf8.RuneStart(text[left]) {
		left--
	}
	for right > 0 && !utf8.RuneStart(text[len(text)-right]) {
		right--
	}
	return text[:left] + "[...]" + text[len(text)-right:] +
		verifierOmission(previouslyOmitted+len(text)-left-right, id)
}

func verifierRemoveHeredocs(arguments string) (string, int) {
	var request map[string]json.RawMessage
	if json.Unmarshal([]byte(arguments), &request) != nil {
		return arguments, 0
	}
	var command string
	if json.Unmarshal(request["command"], &command) != nil {
		return arguments, 0
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if parser.SetLanguage(sitter.NewLanguage(unsafe.Pointer(tree_sitter_bash.Language()))) != nil {
		return arguments, 0
	}
	tree := parser.Parse([]byte(command), nil)
	if tree == nil {
		return arguments, 0
	}
	defer tree.Close()
	var spans [][2]uint
	var visit func(*sitter.Node)
	visit = func(node *sitter.Node) {
		if node.Kind() == "heredoc_body" {
			spans = append(spans, [2]uint{node.StartByte(), node.EndByte()})
			return
		}
		for i := uint(0); i < node.ChildCount(); i++ {
			visit(node.Child(i))
		}
	}
	visit(tree.RootNode())
	if len(spans) == 0 {
		return arguments, 0
	}
	omitted := 0
	for i := len(spans) - 1; i >= 0; i-- {
		span := spans[i]
		omitted += int(span[1] - span[0])
		command = command[:span[0]] + "[heredoc omitted]\n" + command[span[1]:]
	}
	request["command"], _ = json.Marshal(command)
	reduced, err := json.Marshal(request)
	if err != nil {
		return arguments, 0
	}
	return string(reduced), omitted
}

type VerifierHistoryActivities struct {
	Storage common.KeyValueStorage
}

type PrepareVerifierHistoryInput struct {
	ChatHistory *persisted_ai.ChatHistoryContainer `json:"chatHistory"`
	FlowId      string                             `json:"flowId"`
	WorkspaceId string                             `json:"workspaceId"`
	Index       verifierChatHistoryIndex           `json:"index"`
	Settings    VerifierSettings                   `json:"settings"`
	Provider    string                             `json:"provider"`
}

type PrepareVerifierHistoryOutput struct {
	PreparedChatHistory verifierChatHistory      `json:"preparedChatHistory"`
	Index               verifierChatHistoryIndex `json:"index"`
}

func (a *VerifierHistoryActivities) PrepareVerifierHistory(ctx context.Context, input PrepareVerifierHistoryInput) (PrepareVerifierHistoryOutput, error) {
	var messages []llm2.Message
	if input.ChatHistory != nil && input.ChatHistory.History != nil {
		history := input.ChatHistory.History.Clone()
		if structured, ok := history.(*persisted_ai.Llm2ChatHistory); ok {
			structured.SetFlowId(input.FlowId)
			structured.SetWorkspaceId(input.WorkspaceId)
		}
		if err := history.Hydrate(ctx, a.Storage); err != nil {
			return PrepareVerifierHistoryOutput{}, fmt.Errorf("prepare verifier history: %w", err)
		}
		for _, message := range history.Messages() {
			messages = append(messages, persisted_ai.MessageFromCommon(message))
		}
	}
	chatHistory := prepareVerifierMessages(messages, &input.Index, input.Settings, input.Provider)
	return PrepareVerifierHistoryOutput{PreparedChatHistory: chatHistory, Index: input.Index}, nil
}

func verifierInvokedCommands(arguments string) string {
	var request struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(arguments), &request) != nil {
		return ""
	}
	var names []string
	for _, command := range permission.ExtractCommands(request.Command) {
		fields := strings.Fields(command)
		if len(fields) > 0 {
			names = append(names, fields[0])
		}
	}
	return strings.Join(names, "; ")
}

func verifierFilterMessages(messages []llm2.Message) []llm2.Message {
	filtered := make([]llm2.Message, 0, len(messages))
	for _, message := range messages {
		content := make([]llm2.ContentBlock, 0, len(message.Content))
		for _, block := range message.Content {
			if message.Role == llm2.RoleAssistant {
				switch block.Type {
				case llm2.ContentBlockTypeToolUse, llm2.ContentBlockTypeToolResult,
					llm2.ContentBlockTypeMcpCall, llm2.ContentBlockTypeBuiltinToolUse,
					llm2.ContentBlockTypeBuiltinToolResult:
				default:
					continue
				}
			}
			if block.ToolUse != nil {
				call := *block.ToolUse
				call.Arguments = verifierFilterArguments(call.Arguments)
				block.ToolUse = &call
			}
			if block.McpCall != nil {
				call := *block.McpCall
				call.Arguments = verifierFilterArguments(call.Arguments)
				block.McpCall = &call
			}
			if block.BuiltinToolUse != nil {
				call := *block.BuiltinToolUse
				call.Arguments = verifierFilterArguments(call.Arguments)
				block.BuiltinToolUse = &call
			}
			content = append(content, block)
		}
		if len(content) > 0 {
			filtered = append(filtered, llm2.Message{Role: message.Role, Content: content})
		}
	}
	return filtered
}

func verifierFilterArguments(arguments string) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(arguments), &fields) != nil {
		return arguments
	}
	if _, ok := fields["analysis"]; !ok {
		return arguments
	}
	delete(fields, "analysis")
	filtered, err := json.Marshal(fields)
	if err != nil {
		return arguments
	}
	return string(filtered)
}
