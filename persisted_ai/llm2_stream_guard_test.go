package persisted_ai

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/llm2"
	"sidekick/secret_manager"

	"github.com/stretchr/testify/require"
)

// scriptedProvider emits a scripted sequence of events per attempt. Each
// attempt emits events until the context is cancelled, in which case it
// returns the context error like real providers do.
type scriptedProvider struct {
	attempts [][]llm2.Event
	requests []llm2.StreamRequest
	emitted  []int
}

func (p *scriptedProvider) Stream(ctx context.Context, request llm2.StreamRequest, eventChan chan<- llm2.Event) (*llm2.MessageResponse, error) {
	attempt := len(p.requests)
	p.requests = append(p.requests, request)
	p.emitted = append(p.emitted, 0)
	if attempt >= len(p.attempts) {
		return nil, errors.New("unexpected extra attempt")
	}
	for _, event := range p.attempts[attempt] {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case eventChan <- event:
			p.emitted[attempt]++
		}
	}
	return &llm2.MessageResponse{
		Output:     llm2.AccumulateEventsToMessage(p.attempts[attempt]),
		StopReason: "end_turn",
	}, nil
}

func toolUseStart(index int) llm2.Event {
	return llm2.Event{
		Type:  llm2.EventBlockStarted,
		Index: index,
		ContentBlock: &llm2.ContentBlock{
			Type:    llm2.ContentBlockTypeToolUse,
			ToolUse: &llm2.ToolUseBlock{Id: "call_1", Name: "get_symbol_definitions"},
		},
	}
}

func textDelta(index int, delta string) llm2.Event {
	return llm2.Event{Type: llm2.EventTextDelta, Index: index, Delta: delta}
}

// runawayToolCall streams a tool call whose arguments degenerate into far more
// whitespace than the limit, chunked the way a real stream would deliver it.
func runawayToolCall() []llm2.Event {
	events := []llm2.Event{
		toolUseStart(0),
		textDelta(0, `{"analysis":"context","requests":[{"file_path":"a.go","symbols":[{"name":"Foo},{"`),
	}
	chunk := strings.Repeat("\t", 64)
	for i := 0; i < (llm2.RunawayWhitespaceLimit*50)/len(chunk); i++ {
		events = append(events, textDelta(0, chunk))
	}
	events = append(events, llm2.Event{Type: llm2.EventBlockDone, Index: 0})
	return events
}

// runawayToolCallWithTrailingText streams a whitespace run that exceeds the
// limit but where every delta ends in a non-whitespace character, so detection
// must scan whole deltas rather than only their trailing whitespace.
func runawayToolCallWithTrailingText() []llm2.Event {
	events := []llm2.Event{
		toolUseStart(0),
		textDelta(0, `{"analysis":"context","requests":[{"file_path":"a.go","symbols":[{"name":"Foo},{"`),
	}
	events = append(events, textDelta(0, strings.Repeat(" ", llm2.RunawayWhitespaceLimit+1)+"x"))
	for i := 0; i < 50; i++ {
		events = append(events, textDelta(0, strings.Repeat("\t", 1024)+"y"))
	}
	events = append(events, llm2.Event{Type: llm2.EventBlockDone, Index: 0})
	return events
}

func healthyToolCall() []llm2.Event {
	return []llm2.Event{
		toolUseStart(0),
		textDelta(0, `{"analysis":"context","requests":[]}`),
		{Type: llm2.EventBlockDone, Index: 0},
	}
}

func drainEvents(t *testing.T, eventChan <-chan llm2.Event) func() []llm2.Event {
	t.Helper()
	var forwarded []llm2.Event
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range eventChan {
			forwarded = append(forwarded, event)
		}
	}()
	return func() []llm2.Event {
		<-done
		return forwarded
	}
}

func TestStreamWithRetry_RetriesOnceAndSucceeds(t *testing.T) {
	t.Parallel()
	provider := &scriptedProvider{attempts: [][]llm2.Event{runawayToolCall(), healthyToolCall()}}
	eventChan := make(chan llm2.Event, 10)
	collect := drainEvents(t, eventChan)

	originalMessages := []llm2.Message{{Role: llm2.RoleUser, Content: llm2.TextContentBlocks("hi")}}
	response, err := streamWithRetry(context.Background(), provider, llm2.StreamRequest{Messages: originalMessages}, eventChan)
	close(eventChan)
	forwarded := collect()

	require.NoError(t, err)
	require.NotNil(t, response)
	require.Len(t, response.Output.Content, 1)
	require.Equal(t, `{"analysis":"context","requests":[]}`, response.Output.Content[0].ToolUse.Arguments)

	require.Len(t, provider.requests, 2)
	// The first attempt must be cut off well before the model finishes
	// spewing whitespace.
	require.Less(t, provider.emitted[0], len(provider.attempts[0])/2)

	retryMessages := provider.requests[1].Messages
	require.Len(t, retryMessages, len(originalMessages)+2)
	require.Equal(t, originalMessages, retryMessages[:len(originalMessages)])
	require.Equal(t, llm2.RoleAssistant, retryMessages[len(originalMessages)].Role)
	require.Equal(t, llm2.RoleUser, retryMessages[len(originalMessages)+1].Role)
	for _, msg := range retryMessages[len(originalMessages):] {
		for _, block := range msg.Content {
			require.Equal(t, llm2.ContentBlockTypeText, block.Type)
		}
	}
	require.Contains(t, retryMessages[len(originalMessages)+1].Content[0].Text, "whitespace")

	var noticed bool
	for _, event := range forwarded {
		if event.Type == llm2.EventSummaryTextDelta && strings.Contains(event.Delta, "Retrying") {
			noticed = true
		}
	}
	require.True(t, noticed, "expected a retry notice to be streamed")
}

func TestStreamWithRetry_FailsWithPartialOutputOnRepeat(t *testing.T) {
	t.Parallel()
	provider := &scriptedProvider{attempts: [][]llm2.Event{runawayToolCall(), runawayToolCall()}}
	eventChan := make(chan llm2.Event, 10)
	collect := drainEvents(t, eventChan)

	response, err := streamWithRetry(context.Background(), provider, llm2.StreamRequest{}, eventChan)
	close(eventChan)
	collect()

	var runaway *llm2.RunawayWhitespaceError
	require.ErrorAs(t, err, &runaway)
	require.Len(t, provider.requests, 2)
	require.Less(t, provider.emitted[1], len(provider.attempts[1])/2)

	require.NotNil(t, response)
	require.Len(t, response.Output.Content, 1)
	toolUse := response.Output.Content[0].ToolUse
	require.NotNil(t, toolUse)
	require.Equal(t, "get_symbol_definitions", toolUse.Name)
	require.True(t, strings.HasPrefix(toolUse.Arguments, `{"analysis":"context"`))
	require.Less(t, len(toolUse.Arguments), llm2.RunawayWhitespaceLimit*2)
}

func TestStreamWithRetry_DetectsRunawayEndingInText(t *testing.T) {
	t.Parallel()
	provider := &scriptedProvider{attempts: [][]llm2.Event{runawayToolCallWithTrailingText(), healthyToolCall()}}
	eventChan := make(chan llm2.Event, 10)
	collect := drainEvents(t, eventChan)

	response, err := streamWithRetry(context.Background(), provider, llm2.StreamRequest{}, eventChan)
	close(eventChan)
	collect()

	require.NoError(t, err)
	require.Len(t, provider.requests, 2)
	// The oversized run is entirely within the third event, so the provider
	// should be cancelled long before it finishes the script; a handful of
	// extra events may land in the guard's buffer before cancellation lands.
	require.Less(t, provider.emitted[0], len(provider.attempts[0])/2)
	require.Equal(t, `{"analysis":"context","requests":[]}`, response.Output.Content[0].ToolUse.Arguments)
}

func TestStreamWithRetry_PassesThroughHealthyStream(t *testing.T) {
	t.Parallel()
	provider := &scriptedProvider{attempts: [][]llm2.Event{healthyToolCall()}}
	eventChan := make(chan llm2.Event, 10)
	collect := drainEvents(t, eventChan)

	response, err := streamWithRetry(context.Background(), provider, llm2.StreamRequest{}, eventChan)
	close(eventChan)
	forwarded := collect()

	require.NoError(t, err)
	require.Len(t, provider.requests, 1)
	require.Equal(t, healthyToolCall(), forwarded)
	require.Equal(t, "end_turn", response.StopReason)
}

func TestStreamWithRetry_PassesThroughProviderError(t *testing.T) {
	t.Parallel()
	provider := &scriptedProvider{}
	eventChan := make(chan llm2.Event, 10)
	collect := drainEvents(t, eventChan)

	response, err := streamWithRetry(context.Background(), provider, llm2.StreamRequest{}, eventChan)
	close(eventChan)
	collect()

	require.EqualError(t, err, "unexpected extra attempt")
	require.Nil(t, response)
	require.Len(t, provider.requests, 1)
}

func newRunawayStreamActivityInput(t *testing.T) StreamInput {
	t.Helper()
	history := NewLlm2ChatHistory("flow-runaway", "workspace-runaway")
	history.Append(&llm2.Message{Role: llm2.RoleUser, Content: llm2.TextContentBlocks("Look up Service.SaveConfig")})
	return StreamInput{
		Options:     llm2.Options{ModelConfig: common.ModelConfig{Provider: "scripted", Model: "scripted-model"}},
		Secrets:     secret_manager.SecretManagerContainer{SecretManager: secret_manager.MockSecretManager{}},
		ChatHistory: &ChatHistoryContainer{History: history},
		WorkspaceId: "workspace-runaway",
		FlowId:      "flow-runaway",
	}
}

func TestStreamActivity_RunawayWhitespaceRetriesOnce(t *testing.T) {
	t.Parallel()
	provider := &scriptedProvider{attempts: [][]llm2.Event{runawayToolCall(), healthyToolCall()}}
	la := &Llm2Activities{
		providerFactory: func(common.ModelConfig, []common.ModelProviderPublicConfig) (llm2.Provider, error) {
			return provider, nil
		},
	}

	response, err := la.Stream(context.Background(), newRunawayStreamActivityInput(t))

	require.NoError(t, err)
	require.NotNil(t, response)
	require.Equal(t, "scripted", response.Provider)
	require.Len(t, response.Output.Content, 1)
	require.Equal(t, `{"analysis":"context","requests":[]}`, response.Output.Content[0].ToolUse.Arguments)

	require.Len(t, provider.requests, 2)
	require.Less(t, provider.emitted[0], len(provider.attempts[0])/2, "first attempt should be cancelled early")
	require.Len(t, provider.requests[0].Messages, 1)
	require.Len(t, provider.requests[1].Messages, 3)
}

func TestStreamActivity_RepeatedRunawayWhitespaceFailsWithPartialOutput(t *testing.T) {
	t.Parallel()
	provider := &scriptedProvider{attempts: [][]llm2.Event{runawayToolCall(), runawayToolCall()}}
	la := &Llm2Activities{
		providerFactory: func(common.ModelConfig, []common.ModelProviderPublicConfig) (llm2.Provider, error) {
			return provider, nil
		},
	}

	response, err := la.Stream(context.Background(), newRunawayStreamActivityInput(t))

	var runaway *llm2.RunawayWhitespaceError
	require.ErrorAs(t, err, &runaway)
	require.Len(t, provider.requests, 2)
	for attempt := range provider.attempts {
		require.Less(t, provider.emitted[attempt], len(provider.attempts[attempt])/2, "attempt %d should be cancelled early", attempt)
	}

	require.NotNil(t, response)
	require.Equal(t, "scripted", response.Provider)
	require.Len(t, response.Output.Content, 1)
	toolUse := response.Output.Content[0].ToolUse
	require.NotNil(t, toolUse)
	require.Equal(t, "get_symbol_definitions", toolUse.Name)
	require.Less(t, len(toolUse.Arguments), llm2.RunawayWhitespaceLimit*2, "partial output must not carry the runaway whitespace")
}
