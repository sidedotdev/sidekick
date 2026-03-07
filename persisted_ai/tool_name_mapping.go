package persisted_ai

import (
	"sidekick/common"
	"sidekick/llm2"
	"strings"
)

type ToolNameMappingConfig struct {
	Forward map[string]string `json:"forward,omitempty"`
	Reverse map[string]string `json:"reverse,omitempty"`
	Prefix  string            `json:"prefix,omitempty"`
}

func (c ToolNameMappingConfig) MapToolName(name string) string {
	if mappedName, ok := c.Forward[name]; ok {
		return mappedName
	}
	if c.Prefix == "" {
		return name
	}
	return c.Prefix + name
}

func (c ToolNameMappingConfig) ReverseMapToolName(name string) string {
	if mappedName, ok := c.Reverse[name]; ok {
		return mappedName
	}
	if c.Prefix != "" && strings.HasPrefix(name, c.Prefix) {
		return strings.TrimPrefix(name, c.Prefix)
	}
	return name
}

func mapOptionsToolNames(options llm2.Options, config *ToolNameMappingConfig) llm2.Options {
	if config == nil {
		return options
	}

	mappedOptions := options
	if len(options.Tools) > 0 {
		mappedOptions.Tools = make([]*common.Tool, len(options.Tools))
		for i, tool := range options.Tools {
			mappedOptions.Tools[i] = mapTool(tool, config)
		}
	}
	if options.ToolChoice.Type == common.ToolChoiceTypeTool {
		mappedOptions.ToolChoice = options.ToolChoice
		mappedOptions.ToolChoice.Name = mapToolName(options.ToolChoice.Name, config, false)
	}

	return mappedOptions
}

func mapMessagesToolNames(messages []llm2.Message, config *ToolNameMappingConfig) []llm2.Message {
	return mapMessages(messages, config, false)
}

func reverseMapMessageToolNames(message llm2.Message, config *ToolNameMappingConfig) llm2.Message {
	messages := mapMessages([]llm2.Message{message}, config, true)
	return messages[0]
}

func mapMessages(messages []llm2.Message, config *ToolNameMappingConfig, reverse bool) []llm2.Message {
	if len(messages) == 0 {
		return nil
	}

	mappedMessages := make([]llm2.Message, len(messages))
	for i, message := range messages {
		mappedMessages[i] = message
		mappedMessages[i].Content = mapContentBlocksToolNames(message.Content, config, reverse)
	}
	return mappedMessages
}

func mapContentBlocksToolNames(blocks []llm2.ContentBlock, config *ToolNameMappingConfig, reverse bool) []llm2.ContentBlock {
	if len(blocks) == 0 {
		return nil
	}

	mappedBlocks := make([]llm2.ContentBlock, len(blocks))
	for i, block := range blocks {
		mappedBlocks[i] = mapContentBlockToolNames(block, config, reverse)
	}
	return mappedBlocks
}

func mapContentBlockToolNames(block llm2.ContentBlock, config *ToolNameMappingConfig, reverse bool) llm2.ContentBlock {
	mappedBlock := block

	if block.ToolUse != nil {
		toolUse := *block.ToolUse
		toolUse.Name = mapToolName(toolUse.Name, config, reverse)
		mappedBlock.ToolUse = &toolUse
	}

	if block.ToolResult != nil {
		toolResult := *block.ToolResult
		toolResult.Name = mapToolName(toolResult.Name, config, reverse)
		toolResult.Content = mapContentBlocksToolNames(block.ToolResult.Content, config, reverse)
		mappedBlock.ToolResult = &toolResult
	}

	return mappedBlock
}

func mapTool(tool *common.Tool, config *ToolNameMappingConfig) *common.Tool {
	if tool == nil {
		return nil
	}

	mappedTool := *tool
	mappedTool.Name = mapToolName(tool.Name, config, false)
	return &mappedTool
}

func mapToolName(name string, config *ToolNameMappingConfig, reverse bool) string {
	if name == "" || config == nil {
		return name
	}

	var mappedName string
	if reverse {
		mappedName = config.ReverseMapToolName(name)
	} else {
		mappedName = config.MapToolName(name)
	}
	if mappedName == "" {
		return name
	}
	return mappedName
}
