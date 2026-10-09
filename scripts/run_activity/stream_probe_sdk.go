package main

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

func buildProbeSDKParams(body []byte, freshPrompt string) (responses.ResponseNewParams, error) {
	var wire struct {
		Model             string                         `json:"model"`
		Stream            bool                           `json:"stream"`
		Store             *bool                          `json:"store"`
		ParallelToolCalls *bool                          `json:"parallel_tool_calls"`
		Temperature       *float64                       `json:"temperature"`
		MaxOutputTokens   *int64                         `json:"max_output_tokens"`
		ServiceTier       string                         `json:"service_tier"`
		Reasoning         shared.ReasoningParam          `json:"reasoning"`
		Include           []responses.ResponseIncludable `json:"include"`
		ToolChoice        string                         `json:"tool_choice"`
		Input             json.RawMessage                `json:"input"`
		Tools             []json.RawMessage              `json:"tools"`
	}
	var params responses.ResponseNewParams
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return params, fmt.Errorf("decode SDK control request: %w", err)
	}
	if wire.Model == "" || !wire.Stream {
		return params, fmt.Errorf("SDK control requires model and stream=true")
	}
	params.Model = openai.ChatModel(wire.Model)
	params.Reasoning = wire.Reasoning
	params.Include = wire.Include
	params.ServiceTier = responses.ResponseNewParamsServiceTier(wire.ServiceTier)
	if wire.Store != nil {
		params.Store = param.NewOpt(*wire.Store)
	}
	if wire.ParallelToolCalls != nil {
		params.ParallelToolCalls = param.NewOpt(*wire.ParallelToolCalls)
	}
	if wire.Temperature != nil {
		params.Temperature = param.NewOpt(*wire.Temperature)
	}
	if wire.MaxOutputTokens != nil {
		params.MaxOutputTokens = param.NewOpt(*wire.MaxOutputTokens)
	}
	switch wire.ToolChoice {
	case "":
	case "auto", "required", "none":
		params.ToolChoice.OfToolChoiceMode = param.NewOpt(responses.ToolChoiceOptions(wire.ToolChoice))
	default:
		return params, fmt.Errorf("unsupported SDK control tool choice %q", wire.ToolChoice)
	}
	for _, tool := range wire.Tools {
		var discriminator struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(tool, &discriminator); err != nil {
			return params, fmt.Errorf("decode control tool type: %w", err)
		}
		switch discriminator.Type {
		case "function":
			var function responses.FunctionToolParam
			if err := json.Unmarshal(tool, &function); err != nil {
				return params, fmt.Errorf("decode control function: %w", err)
			}
			params.Tools = append(params.Tools, responses.ToolUnionParam{OfFunction: &function})
		case "web_search":
			var search responses.WebSearchToolParam
			if err := json.Unmarshal(tool, &search); err != nil {
				return params, fmt.Errorf("decode control web search: %w", err)
			}
			params.Tools = append(params.Tools, responses.ToolUnionParam{OfWebSearch: &search})
		default:
			return params, fmt.Errorf("unsupported SDK control tool type %q", discriminator.Type)
		}
	}
	if freshPrompt != "" {
		params.Input.OfInputItemList = []responses.ResponseInputItemUnionParam{
			responses.ResponseInputItemParamOfMessage(freshPrompt, responses.EasyInputMessageRoleUser),
		}
	}
	return params, nil
}
