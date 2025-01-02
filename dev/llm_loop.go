package dev

import (
	"fmt"
	"sidekick/llm"
)

var ErrPaused = fmt.Errorf("operation paused")

// LlmIteration represents a single iteration in the LLM loop.
type LlmIteration struct {
	LlmLoopConfig
	Num         int
	ExecCtx     DevContext
	ChatHistory *[]llm.ChatMessage
	State       interface{}
}

// Option is a functional option for configuring LlmLoop
type Option func(*LlmLoopConfig)

type LlmLoopConfig struct {
	maxIterations               int
	maxIterationsBeforeFeedback int
	initialState                interface{}
}

// WithMaxIterations sets the maximum number of iterations for the loop
func WithMaxIterations(max int) Option {
	return func(c *LlmLoopConfig) {
		c.maxIterations = max
	}
}

// WithFeedbackEvery sets the number of iterations before requesting feedback
func WithFeedbackEvery(max int) Option {
	return func(c *LlmLoopConfig) {
		c.maxIterationsBeforeFeedback = max
	}
}

// WithInitialState sets the initial state for the loop
func WithInitialState(initialState interface{}) Option {
	return func(c *LlmLoopConfig) {
		c.initialState = initialState
	}
}

// LlmLoop is a generic function that implements an interative loop for human-in-the-loop LLM invocations
func LlmLoop[T any](dCtx DevContext, chatHistory *[]llm.ChatMessage, loopFunc func(iteration *LlmIteration) (*T, error), opts ...Option) (*T, error) {
	config := &LlmLoopConfig{
		maxIterations:               17,
		maxIterationsBeforeFeedback: 3,
	}

	for _, opt := range opts {
		opt(config)
	}

	if chatHistory == nil {
		chatHistory = &[]llm.ChatMessage{}
	}

	iteration := &LlmIteration{
		LlmLoopConfig: *config,
		Num:           0,
		ExecCtx:       dCtx,
		ChatHistory:   chatHistory,
		State:         config.initialState,
	}

	iterationsSinceLastFeedback := 0

	for {
		iteration.Num++
		iterationsSinceLastFeedback++

		if iteration.Num > config.maxIterations {
			return nil, ErrMaxAttemptsReached
		}

		// Check for pause at the beginning of each iteration
		response, err := UserRequestIfPaused(dCtx, fmt.Sprintf("LlmLoop iteration %d", iteration.Num), nil)
		if err != nil {
			return nil, fmt.Errorf("error checking for pause: %v", err)
		}
		if response != nil && response.Content != "" {
			*iteration.ChatHistory = append(*iteration.ChatHistory, llm.ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("-- PAUSED --\n\nIMPORTANT: The user paused and provided the following guidance:\n\n%s", response.Content),
			})
			iterationsSinceLastFeedback = 0
		}

		// Get user feedback every N iterations
		if iterationsSinceLastFeedback >= config.maxIterationsBeforeFeedback {
			guidanceContext := fmt.Sprintf("The LLM has looped %d times without finalizing. Please provide guidance or just say \"continue\" if they are on track.", iteration.Num)
			userResponse, err := GetUserGuidance(dCtx, guidanceContext, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to get user feedback: %v", err)
			}

			// Add feedback to chat history
			*iteration.ChatHistory = append(*iteration.ChatHistory, llm.ChatMessage{
				Role:    "user",
				Content: userResponse.Content,
			})

			iterationsSinceLastFeedback = 0
		}

		// Use WithCancelOnPause for long-running operations
		cancelCtx := dCtx.WithCancelOnPause()
		result, err := loopFunc(&LlmIteration{
			LlmLoopConfig: iteration.LlmLoopConfig,
			Num:           iteration.Num,
			ExecCtx:       cancelCtx,
			ChatHistory:   iteration.ChatHistory,
			State:         iteration.State,
		})

		if err != nil {
			if err == ErrPaused {
				continue
			}
			return nil, err
		}

		if result != nil {
			return result, nil
		}

		// Check if paused after each iteration
		if dCtx.GlobalState != nil && dCtx.GlobalState.Paused {
			continue
		}

		// I think we want the loopFunc to have full control over managing chat history
		// ManageChatHistory(flowCtx.WorkflowContext, iteration.ChatHistory, defaultMaxChatHistoryLength);
	}
}
