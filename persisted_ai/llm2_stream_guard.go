package persisted_ai

import (
	"context"
	"errors"

	"sidekick/llm2"

	"github.com/rs/zerolog/log"
)

// streamWithRetry calls attemptGuardedStream and retries once on runaway
// whitespace, explaining the failure in the retry prompt. A repeated failure
// returns partial output and the error; other outcomes pass through unchanged.
func streamWithRetry(ctx context.Context, provider llm2.Provider, request llm2.StreamRequest, eventChan chan<- llm2.Event) (*llm2.MessageResponse, error) {
	response, partial, err := attemptGuardedStream(ctx, provider, request, eventChan)
	var runaway *llm2.RunawayWhitespaceError
	if !errors.As(err, &runaway) {
		return response, err
	}

	log.Warn().Err(err).Msg("aborted llm stream on runaway whitespace, retrying with modified prompt")
	eventChan <- llm2.Event{
		Type:  llm2.EventSummaryTextDelta,
		Delta: "\nSidekick: aborted response after runaway whitespace output. Retrying with modified prompt...\n",
	}

	request.Messages = appendRunawayRetryMessages(request.Messages, partial)
	response, partial, err = attemptGuardedStream(ctx, provider, request, eventChan)
	if errors.As(err, &runaway) {
		return &llm2.MessageResponse{
			Model:  request.Options.ModelConfig.Model,
			Output: partial,
		}, err
	}
	return response, err
}

// attemptGuardedStream runs one provider stream without retries, forwarding
// events and cancelling on runaway whitespace. It assembles partial output
// for the resulting *llm2.RunawayWhitespaceError because providers return no
// response for a cancelled stream.
func attemptGuardedStream(ctx context.Context, provider llm2.Provider, request llm2.StreamRequest, eventChan chan<- llm2.Event) (*llm2.MessageResponse, llm2.Message, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	guardedChan := make(chan llm2.Event, 10)
	detector := llm2.NewRunawayWhitespaceDetector(llm2.RunawayWhitespaceLimit)
	var events []llm2.Event
	var runawayErr error

	forwardDone := make(chan struct{})
	go func() {
		defer close(forwardDone)
		for event := range guardedChan {
			// Once aborted, the provider may still flush a few events; they
			// are neither useful to show nor part of the assembled output.
			if runawayErr != nil {
				continue
			}
			events = append(events, event)
			eventChan <- event
			if err := detector.Observe(event); err != nil {
				runawayErr = err
				cancel()
			}
		}
	}()

	response, err := provider.Stream(streamCtx, request, guardedChan)
	close(guardedChan)
	<-forwardDone

	if runawayErr != nil {
		return nil, llm2.AccumulateEventsToMessage(events), runawayErr
	}
	return response, llm2.Message{}, err
}

// appendRunawayRetryMessages records the aborted attempt in the prompt so the
// model sees why it is being asked again. Only text from the partial output
// is replayed: tool_use blocks are omitted because providers reject tool calls
// without a corresponding result.
func appendRunawayRetryMessages(messages []llm2.Message, partial llm2.Message) []llm2.Message {
	assistantText := "(error: response aborted after emitting an unbounded run of whitespace)"
	if text := partialResponseText(partial); text != "" {
		assistantText = text + "\n\n" + assistantText
	}
	retryMessages := make([]llm2.Message, 0, len(messages)+2)
	retryMessages = append(retryMessages, messages...)
	return append(retryMessages,
		llm2.Message{
			Role:    llm2.RoleAssistant,
			Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: assistantText}},
		},
		llm2.Message{
			Role: llm2.RoleUser,
			Content: []llm2.ContentBlock{{
				Type: llm2.ContentBlockTypeText,
				Text: "Your previous response was aborted because it degenerated into a run of whitespace with no content. Please respond again, completing the response (including any tool call arguments) without runaway whitespace.",
			}},
		},
	)
}

func partialResponseText(partial llm2.Message) string {
	text := ""
	for _, block := range partial.Content {
		if block.Type == llm2.ContentBlockTypeText && block.Text != "" {
			if text != "" {
				text += "\n"
			}
			text += block.Text
		}
	}
	return text
}
