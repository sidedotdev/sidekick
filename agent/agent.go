package agent

type Agent interface {
	// Agent interface methods
}

type AgentAction struct {
	Type    string
	TopicId string
	Data    interface{} // TODO like PromptInfo, allow different action types to have different data types
}
