package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"sidekick"
	"sidekick/common"
	"sidekick/persisted_ai"
)

type streamProbeOptions struct {
	ToolChoice      string
	OmitTools       string
	Model           string
	ReasoningEffort string
	// ExtraBody is a JSON object merged over the request's extra body, letting
	// a probe add raw provider parameters the provider does not otherwise send.
	ExtraBody string
}

func prepareStreamProbe(invocation activityInvocation, options streamProbeOptions) (persisted_ai.StreamInput, error) {
	var input persisted_ai.StreamInput
	if invocation.Name != "Stream" || len(invocation.Args) != 1 {
		return input, fmt.Errorf("stream probe requires a Stream activity with exactly one input")
	}
	if err := json.Unmarshal(invocation.Args[0], &input); err != nil {
		return input, fmt.Errorf("decode Stream input: %w", err)
	}
	if input.ChatHistory == nil || input.ChatHistory.History == nil {
		return input, fmt.Errorf("stream probe requires chat history")
	}

	// An empty action ID suppresses publication and closure of the original
	// flow's event stream while retaining history hydration and authentication.
	input.FlowActionId = ""

	if options.ToolChoice != "" {
		switch options.ToolChoice {
		case string(common.ToolChoiceTypeAuto), string(common.ToolChoiceTypeRequired):
			input.Options.ToolChoice = common.ToolChoice{Type: common.ToolChoiceType(options.ToolChoice)}
		default:
			return input, fmt.Errorf("unsupported probe tool choice %q; use auto or required", options.ToolChoice)
		}
	}
	if options.OmitTools != "" {
		for _, name := range strings.Split(options.OmitTools, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				return input, fmt.Errorf("omitted tool name must not be empty")
			}
			found := false
			remaining := make([]*common.Tool, 0, len(input.Options.Tools))
			for _, tool := range input.Options.Tools {
				if tool != nil && (tool.Name == name || string(tool.Type) == name) {
					found = true
					continue
				}
				remaining = append(remaining, tool)
			}
			if !found {
				return input, fmt.Errorf("omitted tool %q not found in request", name)
			}
			input.Options.Tools = remaining
		}
	}
	if len(input.Options.Tools) == 0 &&
		(input.Options.ToolChoice.Type == common.ToolChoiceTypeRequired || input.Options.ToolChoice.Type == common.ToolChoiceTypeTool) {
		return input, fmt.Errorf("required tool choice cannot be used without tools")
	}
	if input.Options.ToolChoice.Type == common.ToolChoiceTypeTool {
		found := false
		for _, tool := range input.Options.Tools {
			if tool != nil && tool.Name == input.Options.ToolChoice.Name {
				found = true
				break
			}
		}
		if !found {
			return input, fmt.Errorf("selected tool %q is not available", input.Options.ToolChoice.Name)
		}
	}
	if options.Model != "" {
		input.Options.Model = options.Model
	}
	if options.ReasoningEffort != "" {
		input.Options.ReasoningEffort = options.ReasoningEffort
	}
	if options.ExtraBody != "" {
		var extra map[string]any
		if err := json.Unmarshal([]byte(options.ExtraBody), &extra); err != nil {
			return input, fmt.Errorf("probe extra body must be a JSON object: %w", err)
		}
		if input.Options.ExtraBody == nil {
			input.Options.ExtraBody = make(map[string]any, len(extra))
		}
		for key, value := range extra {
			input.Options.ExtraBody[key] = value
		}
	}
	return input, nil
}

func executeStreamProbe(invocation activityInvocation, options streamProbeOptions, timeout time.Duration) (json.RawMessage, error) {
	input, err := prepareStreamProbe(invocation, options)
	if err != nil {
		return nil, err
	}
	service, err := sidekick.GetService()
	if err != nil {
		return nil, fmt.Errorf("initialize stream probe storage: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	activities := persisted_ai.Llm2Activities{Storage: service}
	response, err := activities.Stream(ctx, input)
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
}
