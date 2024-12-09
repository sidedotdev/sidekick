package agent

import (
	"context"
)

type Agent interface {
	// PerformAction performs an action given a user intent
	PerformAction(ctx context.Context, action AgentAction, events chan<- Event)
}

type AgentAction struct {
	Type    string
	TopicId string
	Data    interface{} // TODO like PromptInfo, allow different action types to have different data types
}
