package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"sidekick/common"

	"github.com/invopop/jsonschema"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

const minimalProbeDescription = "Report the result of the web search."

func minimalProbeTools() ([]*common.Tool, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(`{"type":"object","properties":{"feedback":{"type":"string"}},"required":["feedback"],"additionalProperties":false}`), &schema); err != nil {
		return nil, err
	}
	return []*common.Tool{
		{Name: "advisor_guide", Description: minimalProbeDescription, Parameters: &schema},
		{Type: common.ToolTypeWebSearch},
	}, nil
}

func minimalProbeSDKTools() []responses.ToolUnionParam {
	return []responses.ToolUnionParam{
		{OfFunction: &responses.FunctionToolParam{
			Name: "advisor_guide", Description: openai.String(minimalProbeDescription),
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"feedback": map[string]any{"type": "string"}},
				"required":   []string{"feedback"}, "additionalProperties": false,
			},
		}},
		{OfWebSearch: &responses.WebSearchToolParam{Type: responses.WebSearchToolTypeWebSearch}},
	}
}

func runMinimalProbeSDK(ctx context.Context, captured *probeCapturedRequest, transport *probeCaptureTransport) error {
	params, err := buildProbeSDKParams(captured.Body, probeFreshPrompt)
	if err != nil {
		return err
	}
	params.Tools = minimalProbeSDKTools()
	opts := []option.RequestOption{
		option.WithBaseURL(captured.URL[:len(captured.URL)-len("/responses")]),
		option.WithHTTPClient(&http.Client{Transport: transport}),
		option.WithMaxRetries(0),
	}
	for name, values := range captured.Header {
		for _, value := range values {
			opts = append(opts, option.WithHeader(name, value))
		}
	}
	client := openai.NewClient(opts...)
	stream := client.Responses.NewStreaming(ctx, params)
	for stream.Next() {
	}
	return errors.Join(stream.Err(), stream.Close())
}

func runRawProbe(ctx context.Context, captured *probeCapturedRequest, transport *probeCaptureTransport) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, captured.URL, bytes.NewReader(captured.Body))
	if err != nil {
		return err
	}
	request.Header = captured.Header.Clone()
	response, err := transport.RoundTrip(request)
	if err != nil {
		return err
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	return errors.Join(readErr, response.Body.Close())
}

func probeRequestVariant(original *probeCapturedRequest, changes map[string]json.RawMessage) (*probeCapturedRequest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(original.Body, &fields); err != nil {
		return nil, err
	}
	for key, value := range changes {
		if _, exists := fields[key]; !exists {
			return nil, fmt.Errorf("variant field %q missing from original request", key)
		}
		if value == nil {
			delete(fields, key)
		} else {
			fields[key] = value
		}
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return &probeCapturedRequest{URL: original.URL, Header: original.Header.Clone(), Body: body}, nil
}
