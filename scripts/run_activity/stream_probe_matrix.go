package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"sidekick/llm2"
	"sidekick/persisted_ai"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const probeFreshPrompt = "Use the available web search tool now to find the current Go release on go.dev. After the search completes, call advisor_guide with your findings using its declared schema. Do not answer from memory and do not call advisor_guide before completing web search."

type probeMatrixRun struct {
	Name       string           `json:"name"`
	Endpoint   string           `json:"endpoint"`
	AuthMode   string           `json:"authMode"`
	HTTPStatus int              `json:"httpStatus"`
	Trace      probeTraceReport `json:"trace"`
	Error      string           `json:"error,omitempty"`
}

// The command runs sequentially. Replacing DefaultTransport captures the
// provider's real request without exporting credential plumbing from llm2.
func withProbeTransport(transport *probeCaptureTransport, call func() error) error {
	original := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = original }()
	return call()
}

func captureProbeApplication(ctx context.Context, activities *persisted_ai.Llm2Activities, input persisted_ai.StreamInput, base http.RoundTripper) (*probeCapturedRequest, error) {
	capture := &probeCaptureTransport{base: base, captureOnly: true}
	err := withProbeTransport(capture, func() error {
		_, err := activities.Stream(ctx, input)
		return err
	})
	if !errors.Is(err, errProbeCaptured) || capture.calls != 1 || capture.request == nil {
		return nil, fmt.Errorf("expected exactly one unsent Responses request, got %d: %w", capture.calls, err)
	}
	return &probeCapturedRequest{
		URL: capture.request.URL.String(), Header: capture.request.Header.Clone(), Body: capture.requestBody,
	}, nil
}

func runProbeSDK(ctx context.Context, captured *probeCapturedRequest, prompt string, transport *probeCaptureTransport) error {
	params, err := buildProbeSDKParams(captured.Body, prompt)
	if err != nil {
		return err
	}
	opts := []option.RequestOption{
		option.WithBaseURL(strings.TrimSuffix(captured.URL, "/responses")),
		option.WithHTTPClient(&http.Client{Transport: transport}),
		option.WithMaxRetries(0),
	}
	for name, values := range captured.Header {
		if strings.EqualFold(name, "Content-Length") {
			continue
		}
		for _, value := range values {
			opts = append(opts, option.WithHeader(name, value))
		}
	}
	var extra []option.RequestOption
	if prompt == "" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(captured.Body, &fields); err != nil {
			return err
		}
		extra = append(extra, option.WithJSONSet("input", fields["input"]))
	}
	client := openai.NewClient(opts...)
	stream := client.Responses.NewStreaming(ctx, params, extra...)
	for stream.Next() {
	}
	return errors.Join(stream.Err(), stream.Close())
}

func executeProbeMatrix(ctx context.Context, activities *persisted_ai.Llm2Activities, input persisted_ai.StreamInput, dir string) (json.RawMessage, error) {
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create new private probe directory: %w", err)
	}
	base := http.DefaultTransport
	stored, err := captureProbeApplication(ctx, activities, input, base)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(stored.Body, &fields); err != nil {
		return nil, err
	}
	var tools []struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(fields["tools"], &tools); err != nil {
		return nil, err
	}
	search, function := false, false
	for _, tool := range tools {
		builtinName, clientBuiltin := llm2.BuiltinToolNameFromClientName(tool.Name)
		search = search || tool.Type == "web_search" ||
			(tool.Type == "function" && clientBuiltin && builtinName == "web_search")
		function = function || (tool.Type == "function" && tool.Name == "advisor_guide")
	}
	if !search || !function {
		return nil, fmt.Errorf("matrix requires web search and advisor_guide")
	}

	freshInput := input
	history := persisted_ai.NewLlm2ChatHistory(input.FlowId, input.WorkspaceId)
	history.Append(&llm2.Message{Role: llm2.RoleUser, Content: llm2.TextContentBlocks(probeFreshPrompt)})
	freshInput.ChatHistory = &persisted_ai.ChatHistoryContainer{History: history}
	fresh, err := captureProbeApplication(ctx, activities, freshInput, base)
	if err != nil {
		return nil, err
	}
	var freshFields map[string]json.RawMessage
	if err := json.Unmarshal(fresh.Body, &freshFields); err != nil {
		return nil, err
	}
	fields["input"] = freshFields["input"]
	expectedFresh, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	if err := validateProbeRequest(expectedFresh, fresh.Body); err != nil {
		return nil, fmt.Errorf("fresh history changed fields other than input: %w", err)
	}
	if stored.URL != fresh.URL || stored.Header.Get("Authorization") != fresh.Header.Get("Authorization") ||
		stored.Header.Get("ChatGPT-Account-Id") != fresh.Header.Get("ChatGPT-Account-Id") {
		return nil, fmt.Errorf("endpoint or authentication changed during capture")
	}

	minimalInput := freshInput
	minimalInput.Options.Tools, err = minimalProbeTools()
	if err != nil {
		return nil, err
	}
	minimalInput.ToolNameMapping = nil
	minimal, err := captureProbeApplication(ctx, activities, minimalInput, base)
	if err != nil {
		return nil, err
	}
	var minimalFields map[string]json.RawMessage
	if err := json.Unmarshal(minimal.Body, &minimalFields); err != nil {
		return nil, err
	}
	freshFields["tools"] = minimalFields["tools"]
	expectedMinimal, err := json.Marshal(freshFields)
	if err != nil {
		return nil, err
	}
	if err := validateProbeRequest(expectedMinimal, minimal.Body); err != nil {
		return nil, fmt.Errorf("minimal tools changed fields other than tools: %w", err)
	}
	if minimal.URL != fresh.URL || minimal.Header.Get("Authorization") != fresh.Header.Get("Authorization") ||
		minimal.Header.Get("ChatGPT-Account-Id") != fresh.Header.Get("ChatGPT-Account-Id") {
		return nil, fmt.Errorf("minimal control changed endpoint or authentication")
	}

	const searchOnlyPrompt = "Use the available web search tool now to find the current Go release on go.dev. After the search completes, answer briefly in plain text with your findings. Do not answer from memory."
	searchOnlyInput := minimalInput
	searchOnlyInput.Options.Tools = minimalInput.Options.Tools[1:]
	searchHistory := persisted_ai.NewLlm2ChatHistory(input.FlowId, input.WorkspaceId)
	searchHistory.Append(&llm2.Message{Role: llm2.RoleUser, Content: llm2.TextContentBlocks(searchOnlyPrompt)})
	searchOnlyInput.ChatHistory = &persisted_ai.ChatHistoryContainer{History: searchHistory}
	searchOnly, err := captureProbeApplication(ctx, activities, searchOnlyInput, base)
	if err != nil {
		return nil, err
	}
	var searchFields map[string]json.RawMessage
	if err := json.Unmarshal(searchOnly.Body, &searchFields); err != nil {
		return nil, err
	}
	var searchTools []map[string]any
	if err := json.Unmarshal(searchFields["tools"], &searchTools); err != nil {
		return nil, err
	}
	if len(searchTools) != 1 {
		return nil, fmt.Errorf("search-only control must offer exactly one tool")
	}
	if _, err := probeHostedRequest(searchOnly); err != nil {
		return nil, fmt.Errorf("search-only control: %w", err)
	}
	minimalFields["input"] = searchFields["input"]
	minimalFields["tools"] = searchFields["tools"]
	expectedSearchOnly, err := json.Marshal(minimalFields)
	if err != nil {
		return nil, err
	}
	if err := validateProbeRequest(expectedSearchOnly, searchOnly.Body); err != nil {
		return nil, fmt.Errorf("search-only control changed fields other than input and tools: %w", err)
	}
	if searchOnly.URL != minimal.URL || searchOnly.Header.Get("Authorization") != minimal.Header.Get("Authorization") ||
		searchOnly.Header.Get("ChatGPT-Account-Id") != minimal.Header.Get("ChatGPT-Account-Id") {
		return nil, fmt.Errorf("search-only control changed endpoint or authentication")
	}
	providerStored, providerFresh, providerMinimal, providerSearchOnly := stored, fresh, minimal, searchOnly
	for _, captured := range []**probeCapturedRequest{&stored, &fresh, &minimal, &searchOnly} {
		*captured, err = probeHostedRequest(*captured)
		if err != nil {
			return nil, err
		}
	}
	searchAuto, err := probeRequestVariant(searchOnly, map[string]json.RawMessage{"tool_choice": json.RawMessage(`"auto"`)})
	if err != nil {
		return nil, err
	}
	var reasoning map[string]json.RawMessage
	if err := json.Unmarshal(searchFields["reasoning"], &reasoning); err != nil {
		return nil, err
	}
	if _, exists := reasoning["effort"]; !exists {
		return nil, fmt.Errorf("search-only effort control requires reasoning.effort")
	}
	reasoning["effort"] = json.RawMessage(`"medium"`)
	mediumReasoning, err := json.Marshal(reasoning)
	if err != nil {
		return nil, err
	}
	searchMedium, err := probeRequestVariant(searchOnly, map[string]json.RawMessage{"reasoning": mediumReasoning})
	if err != nil {
		return nil, err
	}
	searchDefaultReasoning, err := probeRequestVariant(searchOnly, map[string]json.RawMessage{"reasoning": nil})
	if err != nil {
		return nil, err
	}
	searchNoInclude, err := probeRequestVariant(searchOnly, map[string]json.RawMessage{"include": nil})
	if err != nil {
		return nil, err
	}
	searchNoParallel, err := probeRequestVariant(searchOnly, map[string]json.RawMessage{"parallel_tool_calls": nil})
	if err != nil {
		return nil, err
	}
	searchBare, err := probeRequestVariant(searchOnly, map[string]json.RawMessage{
		"reasoning": nil, "include": nil, "parallel_tool_calls": nil, "tool_choice": nil,
	})
	if err != nil {
		return nil, err
	}
	searchBareHeaders := *searchBare
	searchBareHeaders.Header = make(http.Header)
	for _, name := range []string{"Authorization", "ChatGPT-Account-Id", "OpenAI-Organization", "OpenAI-Project"} {
		for _, value := range searchBare.Header.Values(name) {
			searchBareHeaders.Header.Add(name, value)
		}
	}
	searchBareHeaders.Header.Set("Content-Type", "application/json")
	searchBareHeaders.Header.Set("Accept", "text/event-stream")

	var runs []probeMatrixRun
	var failures []error
	for _, control := range []struct {
		name     string
		input    persisted_ai.StreamInput
		captured *probeCapturedRequest
		prompt   string
		sdk      bool
	}{
		{"provider-stored-application", input, providerStored, "", false},
		{"hosted-stored-sdk", input, stored, "", true},
		{"provider-fresh-application", freshInput, providerFresh, probeFreshPrompt, false},
		{"hosted-fresh-sdk", freshInput, fresh, probeFreshPrompt, true},
		{"provider-minimal-application", minimalInput, providerMinimal, probeFreshPrompt, false},
		{"hosted-minimal-sdk", minimalInput, minimal, probeFreshPrompt, true},
		{"hosted-minimal-http", minimalInput, minimal, probeFreshPrompt, false},
		{"provider-search-only-application", searchOnlyInput, providerSearchOnly, searchOnlyPrompt, false},
		{"hosted-search-only-sdk", searchOnlyInput, searchOnly, searchOnlyPrompt, true},
		{"hosted-search-only-http", searchOnlyInput, searchOnly, searchOnlyPrompt, false},
		{"hosted-search-only-auto-http", searchOnlyInput, searchAuto, searchOnlyPrompt, false},
		{"hosted-search-only-medium-http", searchOnlyInput, searchMedium, searchOnlyPrompt, false},
		{"hosted-search-only-default-reasoning-http", searchOnlyInput, searchDefaultReasoning, searchOnlyPrompt, false},
		{"hosted-search-only-no-include-http", searchOnlyInput, searchNoInclude, searchOnlyPrompt, false},
		{"hosted-search-only-no-parallel-http", searchOnlyInput, searchNoParallel, searchOnlyPrompt, false},
		{"hosted-search-only-bare-http", searchOnlyInput, searchBare, searchOnlyPrompt, false},
		{"hosted-search-only-bare-headers-http", searchOnlyInput, &searchBareHeaders, searchOnlyPrompt, false},
	} {
		if err := os.WriteFile(filepath.Join(dir, control.name+".request.json"), control.captured.Body, 0o600); err != nil {
			return nil, err
		}
		providerControl := strings.HasPrefix(control.name, "provider-")
		transport := &probeCaptureTransport{base: base, expected: control.captured, allowContinuation: providerControl}
		var response *llm2.MessageResponse
		var runErr error
		if strings.HasSuffix(control.name, "-http") {
			runErr = runRawProbe(ctx, control.captured, transport)
		} else if control.name == "hosted-minimal-sdk" {
			runErr = runMinimalProbeSDK(ctx, control.captured, transport)
		} else if control.sdk {
			runErr = runProbeSDK(ctx, control.captured, control.prompt, transport)
		} else {
			runErr = withProbeTransport(transport, func() error {
				var err error
				response, err = activities.Stream(ctx, control.input)
				return err
			})
		}
		if err := os.WriteFile(filepath.Join(dir, control.name+".sse"), transport.responseBody.Bytes(), 0o600); err != nil {
			return nil, err
		}
		if len(transport.requestBody) > 0 {
			if err := os.WriteFile(filepath.Join(dir, control.name+".actual.request.json"), transport.requestBody, 0o600); err != nil {
				return nil, err
			}
		}
		for i, exchange := range transport.exchanges {
			prefix := filepath.Join(dir, fmt.Sprintf("%s.round-%02d", control.name, i+1))
			if err := os.WriteFile(prefix+".request.json", exchange.RequestBody, 0o600); err != nil {
				return nil, err
			}
			if err := os.WriteFile(prefix+".sse", exchange.ResponseBody.Bytes(), 0o600); err != nil {
				return nil, err
			}
		}
		functionName := "advisor_guide"
		if strings.Contains(control.name, "-search-only-") {
			functionName = ""
		}
		var trace probeTraceReport
		var traceErr error
		if providerControl {
			trace, traceErr = analyzeProbeApplication(transport, response, functionName)
		} else {
			trace, traceErr = analyzeProbeSSE(bytes.NewReader(transport.responseBody.Bytes()), functionName)
		}
		var expectedModel string
		if err := json.Unmarshal(fields["model"], &expectedModel); err != nil {
			return nil, err
		}
		if trace.Model != expectedModel {
			traceErr = errors.Join(traceErr, fmt.Errorf("response model %q differs from requested %q", trace.Model, expectedModel))
		}
		if (!providerControl && transport.calls != 1) || transport.calls == 0 || transport.statusCode != http.StatusOK {
			traceErr = errors.Join(traceErr, fmt.Errorf("unexpected response count or status: %d calls and status %d", transport.calls, transport.statusCode))
		}
		run := probeMatrixRun{
			Name: control.name, Endpoint: control.captured.URL, AuthMode: "api",
			HTTPStatus: transport.statusCode, Trace: trace,
		}
		if control.captured.Header.Get("ChatGPT-Account-Id") != "" {
			run.AuthMode = "subscription"
		}
		if combined := errors.Join(runErr, traceErr); combined != nil {
			run.Error = combined.Error()
			failures = append(failures, fmt.Errorf("%s: %w", control.name, combined))
		}
		runs = append(runs, run)
		report, err := json.MarshalIndent(runs, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, "report.json"), report, 0o600); err != nil {
			return nil, err
		}
		fmt.Fprintln(os.Stderr, string(report))
		if ctx.Err() != nil {
			return nil, errors.Join(ctx.Err(), errors.Join(failures...))
		}
	}
	report, err := json.Marshal(runs)
	return report, errors.Join(err, errors.Join(failures...))
}
