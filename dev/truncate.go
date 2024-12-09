package dev

import (
	"regexp"
	"sidekick/llm"
)

const truncationMarker = "[ . . . ]"

// newMessageTruncatedBetween can be used to shorten a single message by truncating everything between start and end patterns, if matched
func newMessageTruncatedBetween(message llm.ChatMessage, startPattern *regexp.Regexp, endPattern *regexp.Regexp) llm.ChatMessage {
	newContent := message.Content
	offset := 0
	for {
		if offset >= len(newContent) {
			break
		}
		startIndices := startPattern.FindIndex([]byte(newContent)[offset:])
		if startIndices == nil {
			break
		}
		// Adjust indices according to slice offset
		startIndices[0] += offset
		startIndices[1] += offset

		endIndices := endPattern.FindIndex([]byte(newContent[startIndices[1]:]))
		if endIndices == nil {
			break
		}
		// Adjust indices according to slice offset
		endIndices[0] += startIndices[1]
		endIndices[1] += startIndices[1]

		// Replace the content between start and end pattern with an explicit truncation marker
		newContent = newContent[:startIndices[1]] + truncationMarker + newContent[endIndices[0]:]
		offset = startIndices[1] + len(truncationMarker)
	}

	return llm.ChatMessage{
		Role:      message.Role,
		Content:   newContent,
		Name:      message.Name,
		ToolCalls: message.ToolCalls,
	}
}

func truncateBetweenLines(message *llm.ChatMessage, startLine string, endLine string) error {
	startPattern, err := regexp.Compile("(?m)^" + startLine + "\n")
	if err != nil {
		return err
	}
	endPattern, err := regexp.Compile("(?m)\n" + endLine)
	if err != nil {
		return err
	}

	truncateBetween(message, startPattern, endPattern)

	return nil
}

func truncateBetween(message *llm.ChatMessage, startPattern *regexp.Regexp, endPattern *regexp.Regexp) {
	truncatedMessage := newMessageTruncatedBetween(*message, startPattern, endPattern)
	message.Content = truncatedMessage.Content
}

// truncateAllBetweenLines can be used to shorten all messages by truncating lines between a start and end line, if matched
func truncateAllBetweenLines(messages *[]llm.ChatMessage, startLine string, endLine string) error {
	startPattern, err := regexp.Compile("(?m)^" + startLine + "\n")
	if err != nil {
		return err
	}
	endPattern, err := regexp.Compile("(?m)\n" + endLine)
	if err != nil {
		return err
	}

	truncateAllBetween(messages, startPattern, endPattern)

	return nil
}

// truncateAllBetween can be used to shorten all messages by truncating everything between start and end patterns, if matched
func truncateAllBetween(messages *[]llm.ChatMessage, startPattern *regexp.Regexp, endPattern *regexp.Regexp) {
	// Create a new slice of messages to hold the modified messages
	newMessages := make([]llm.ChatMessage, len(*messages))

	// Iterate over all messages and apply truncateBetween on them
	for i, message := range *messages {
		newMessages[i] = newMessageTruncatedBetween(message, startPattern, endPattern)
	}

	// Replace the original slice with the modified slice
	*messages = newMessages
}

// TODO: Add a new function to summarize messages more smartly (eg omit old
// edit blocks for example, or summarize using a smart summarization tool).
// Or smartly choose which messages to drop based on tagging of messages.
