package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChatMessage_ImplementsMessage(t *testing.T) {
	var _ Message = ChatMessage{}
}

func TestChatMessage_GetRole(t *testing.T) {
	tests := []struct {
		name     string
		role     ChatMessageRole
		expected string
	}{
		{"user role", ChatMessageRoleUser, "user"},
		{"assistant role", ChatMessageRoleAssistant, "assistant"},
		{"system role", ChatMessageRoleSystem, "system"},
		{"tool role", ChatMessageRoleTool, "tool"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := ChatMessage{Role: tt.role}
			assert.Equal(t, tt.expected, msg.GetRole())
		})
	}
}

func TestChatMessage_GetContentString(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{"empty content", "", ""},
		{"simple content", "Hello, world!", "Hello, world!"},
		{"multiline content", "Line 1\nLine 2\nLine 3", "Line 1\nLine 2\nLine 3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := ChatMessage{Content: tt.content}
			assert.Equal(t, tt.expected, msg.GetContentString())
		})
	}
}
