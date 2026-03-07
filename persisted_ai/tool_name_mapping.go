package persisted_ai

import (
	"sidekick/common"
	"sidekick/llm2"
)

type ToolNameMapper interface {
	MapToolName(name string) string
	ReverseMapToolName(name string) string
}

func mapOptionsToolNames(options llm2.Options, mapper *ToolNameMapper) llm2.Options {
	if mapper == nil {
		return options
	}

	mappedOptions := options
	if len(options.Tools) > 0 {
		mappedOptions.Tools = make([]*common.Tool, len(options.Tools))
		for i, tool := range options.Tools {
			mappedOptions.Tools[i] = mapTool(tool, mapper)
		}
	}
	if options.ToolChoice.Type == common.ToolChoiceTypeTool {
		mappedOptions.ToolChoice = options.ToolChoice
		mappedOptions.ToolChoice.Name = mapToolName(options.ToolChoice.Name, mapper, false)
	}

	return mappedOptions
}

func mapMessagesToolNames(messages []llm2.Message, mapper *ToolNameMapper) []llm2.Message {
	return mapMessagesWithToolNameMapper(messages, mapper, false)
}

func reverseMapMessageToolNames(message llm2.Message, mapper *ToolNameMapper) llm2.Message {
	messages := mapMessagesWithToolNameMapper([]llm2.Message{message}, mapper, true)
	return messages[0]
}

func mapMessagesWithToolNameMapper(messages []llm2.Message, mapper *ToolNameMapper, reverse bool) []llm2.Message {
	if len(messages) == 0 {
		return nil
	}

	mappedMessages := make([]llm2.Message, len(messages))
	for i, message := range messages {
		mappedMessages[i] = message
		mappedMessages[i].Content = mapContentBlocksToolNames(message.Content, mapper, reverse)
	}
	return mappedMessages
}

func mapContentBlocksToolNames(blocks []llm2.ContentBlock, mapper *ToolNameMapper, reverse bool) []llm2.ContentBlock {
	if len(blocks) == 0 {
		return nil
	}

	mappedBlocks := make([]llm2.ContentBlock, len(blocks))
	for i, block := range blocks {
		mappedBlocks[i] = mapContentBlockToolNames(block, mapper, reverse)
	}
	return mappedBlocks
}

func mapContentBlockToolNames(block llm2.ContentBlock, mapper *ToolNameMapper, reverse bool) llm2.ContentBlock {
	mappedBlock := block

	if block.ToolUse != nil {
		toolUse := *block.ToolUse
		toolUse.Name = mapToolName(toolUse.Name, mapper, reverse)
		mappedBlock.ToolUse = &toolUse
	}

	if block.ToolResult != nil {
		toolResult := *block.ToolResult
		toolResult.Name = mapToolName(toolResult.Name, mapper, reverse)
		toolResult.Content = mapContentBlocksToolNames(block.ToolResult.Content, mapper, reverse)
		mappedBlock.ToolResult = &toolResult
	}

	return mappedBlock
}

func mapTool(tool *common.Tool, mapper *ToolNameMapper) *common.Tool {
	if tool == nil {
		return nil
	}

	mappedTool := *tool
	mappedTool.Name = mapToolName(tool.Name, mapper, false)
	return &mappedTool
}

func mapToolName(name string, mapper *ToolNameMapper, reverse bool) string {
	if name == "" {
		return name
	}

	resolvedMapper := resolveToolNameMapper(mapper)
	if resolvedMapper == nil {
		return name
	}

	var mappedName string
	if reverse {
		mappedName = resolvedMapper.ReverseMapToolName(name)
	} else {
		mappedName = resolvedMapper.MapToolName(name)
	}
	if mappedName == "" {
		return name
	}
	return mappedName
}

func resolveToolNameMapper(mapper *ToolNameMapper) ToolNameMapper {
	if mapper == nil {
		return nil
	}
	return *mapper
}
