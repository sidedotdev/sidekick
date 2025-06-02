package dev

import (
	"context"
	"errors"
	"fmt"
	"sidekick/llm"

	"go.temporal.io/sdk/workflow"
)

// LlmIteration represents a single iteration in the LLM loop.
type LlmIteration struct {
	LlmLoopConfig
	Num                  int
	NumSinceLastFeedback int
	ExecCtx              DevContext
	ChatHistory          *[]llm.ChatMessage
	State                interface{}
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
		LlmLoopConfig:        *config,
		Num:                  0,
		NumSinceLastFeedback: 0,
		ExecCtx:              dCtx,
		ChatHistory:          chatHistory,
		State:                config.initialState,
	}

	for {
		iteration.Num++
		iteration.NumSinceLastFeedback++
		// Use WithCancelOnPause for long-running operations, ensuring a fresh context for each iteration.
		iteration.ExecCtx = dCtx.WithCancelOnPause()

		v := workflow.GetVersion(dCtx, "no-max-unless-disabled-human", workflow.DefaultVersion, 1)
		if iteration.Num > config.maxIterations && (v == 0 || dCtx.RepoConfig.DisableHumanInTheLoop) {
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
			iteration.NumSinceLastFeedback = 0
		}

		// Get user feedback every N iterations
		if iteration.NumSinceLastFeedback >= config.maxIterationsBeforeFeedback {
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

			iteration.NumSinceLastFeedback = 0
		}

		result, err := loopFunc(iteration)

		if err != nil {
			// If loopFunc was canceled due to a system pause, continue the loop.
			// UserRequestIfPaused at the next iteration's start will handle the pause.
			if dCtx.GlobalState != nil && dCtx.GlobalState.Paused && errors.Is(err, context.Canceled) {
				if v >= 1 {
					// ensure we don't break due to max iterations in this case
					iteration.Num--
					iteration.NumSinceLastFeedback--
				}
				continue
			}
			// Otherwise, it's a genuine error from loopFunc.
			return nil, err
		}

		if result != nil {
			return result, nil
		}
	}
}
