package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"sidekick/persisted_ai"
)

// probeLiteHeader marks a Codex backend request as using the Responses Lite
// dialect over HTTP, matching Codex's add_responses_lite_header.
const probeLiteHeader = "x-openai-internal-codex-responses-lite"

const probeLiteSearchFunction = "search_web"

const probeLiteClientPrompt = "Call the search_web function now to find the current Go release on go.dev. After you receive its results, answer briefly in plain text with your findings. Do not answer from memory."

var probeLiteFunctionTool = json.RawMessage(`{"type":"function","name":"search_web","description":"Search the web. Returns result titles, URLs and snippets.","strict":false,"parameters":{"type":"object","properties":{"query":{"type":"string","description":"The search query."}},"required":["query"],"additionalProperties":false}}`)

var probeLiteHostedTool = json.RawMessage(`{"type":"web_search"}`)

type probeLiteControl struct {
	Name string
	// Lite sends the request in the Responses Lite dialect Codex uses for
	// models whose catalog entry sets use_responses_lite.
	Lite bool
	// ClientSearch declares a client-executed search function backed by
	// alpha/search instead of the hosted web_search tool.
	ClientSearch bool
}

// Controls are interleaved within each attempt so time-varying backend
// behavior affects every control equally.
var probeLiteControls = []probeLiteControl{
	{Name: "full-hosted"},
	{Name: "full-client", ClientSearch: true},
	{Name: "lite-hosted", Lite: true},
	{Name: "lite-client", Lite: true, ClientSearch: true},
}

type probeLiteTrace struct {
	Outcome           string           `json:"outcome"`
	ResponseID        string           `json:"responseId"`
	Model             string           `json:"model"`
	EventCount        int              `json:"eventCount"`
	LastEventType     string           `json:"lastEventType"`
	SearchesCompleted int              `json:"searchesCompleted"`
	MessagesCompleted int              `json:"messagesCompleted"`
	FunctionCalls     []probeTraceItem `json:"functionCalls,omitempty"`
	ErrorCode         string           `json:"errorCode,omitempty"`
	ErrorMessage      string           `json:"errorMessage,omitempty"`
}

type probeLiteStage struct {
	Request    string         `json:"request"`
	HTTPStatus int            `json:"httpStatus"`
	Trace      probeLiteTrace `json:"trace"`
	Error      string         `json:"error,omitempty"`
}

// probeLiteMaxSearchRounds bounds client-executed search rounds, since models
// may legitimately refine a query before answering.
const probeLiteMaxSearchRounds = 4

type probeLiteRun struct {
	Name          string                   `json:"name"`
	Control       string                   `json:"control"`
	Lite          bool                     `json:"lite"`
	ClientSearch  bool                     `json:"clientSearch"`
	Model         string                   `json:"model"`
	Outcome       string                   `json:"outcome"`
	Initial       probeLiteStage           `json:"initial"`
	AlphaSearches []probeAlphaSearchReport `json:"alphaSearches,omitempty"`
	FollowUps     []probeLiteStage         `json:"followUps,omitempty"`
	Error         string                   `json:"error,omitempty"`
}

type probeLiteReport struct {
	Endpoint string         `json:"endpoint"`
	AuthMode string         `json:"authMode"`
	Model    string         `json:"model"`
	Runs     []probeLiteRun `json:"runs"`
}

// buildProbeLiteBody mirrors Codex's build_responses_request: Lite requests
// omit top-level tools and instead prepend a developer additional_tools item,
// grouping function tools into the default "functions" namespace, and also
// disable parallel tool calls and request all-turn reasoning context.
func buildProbeLiteBody(model string, control probeLiteControl, followUp []json.RawMessage) ([]byte, error) {
	if model == "" {
		return nil, fmt.Errorf("lite probe requires a model")
	}
	prompt, tool := probeSearchOnlyPrompt, probeLiteHostedTool
	if control.ClientSearch {
		prompt, tool = probeLiteClientPrompt, probeLiteFunctionTool
	}
	user, err := json.Marshal(map[string]any{"role": "user", "content": prompt})
	if err != nil {
		return nil, err
	}
	input := []json.RawMessage{user}
	body := map[string]any{"model": model, "store": false, "stream": true, "tool_choice": "auto"}
	if control.Lite {
		declared := tool
		if control.ClientSearch {
			declared, err = json.Marshal(map[string]any{
				"type": "namespace", "name": "functions", "description": "", "tools": []json.RawMessage{tool},
			})
			if err != nil {
				return nil, err
			}
		}
		additional, err := json.Marshal(map[string]any{
			"type": "additional_tools", "role": "developer", "tools": []json.RawMessage{declared},
		})
		if err != nil {
			return nil, err
		}
		input = append([]json.RawMessage{additional}, input...)
		body["parallel_tool_calls"] = false
		body["reasoning"] = map[string]any{"context": "all_turns"}
	} else {
		body["tools"] = []json.RawMessage{tool}
	}
	body["input"] = append(input, followUp...)
	return json.Marshal(body)
}

func analyzeProbeLiteSSE(reader io.Reader) (probeLiteTrace, error) {
	trace := probeLiteTrace{Outcome: "invalid"}
	items := make(map[string]probeTraceItem)
	created, terminal := false, false
	consume := func(eventName string, data []byte) error {
		if string(data) == "[DONE]" {
			if !terminal {
				return fmt.Errorf("DONE before terminal response event")
			}
			return nil
		}
		var event probeTraceEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return fmt.Errorf("decode SSE event: %w", err)
		}
		if event.Type == "" || (eventName != "" && eventName != event.Type) {
			return fmt.Errorf("SSE event type mismatch: %q / %q", eventName, event.Type)
		}
		// The codex backend follows an error event with response.failed.
		if terminal && (trace.LastEventType != "error" || event.Type != "response.failed") {
			return fmt.Errorf("event %s after terminal response event", event.Type)
		}
		trace.EventCount++
		trace.LastEventType = event.Type
		if event.Type == "keepalive" {
			return nil
		}
		if !created && event.Type != "response.created" {
			return fmt.Errorf("missing response.created before %s", event.Type)
		}
		switch event.Type {
		case "response.created":
			if created || event.Response.ID == "" || event.Response.Model == "" {
				return fmt.Errorf("invalid response.created")
			}
			created = true
			trace.ResponseID, trace.Model = event.Response.ID, event.Response.Model
		case "response.output_item.added":
			item := event.Item
			if item.ID == "" || item.Type == "" {
				return fmt.Errorf("output item missing id or type")
			}
			if _, exists := items[item.ID]; exists {
				return fmt.Errorf("duplicate output item %q", item.ID)
			}
			items[item.ID] = item
		case "response.output_item.done":
			item := event.Item
			prior, exists := items[item.ID]
			if !exists || prior.Type != item.Type {
				return fmt.Errorf("inconsistent completion for item %q", item.ID)
			}
			items[item.ID] = item
			switch item.Type {
			case "web_search_call":
				if item.Status == "completed" {
					trace.SearchesCompleted++
				}
			case "message":
				trace.MessagesCompleted++
			case "function_call":
				if item.CallID == "" || item.Name == "" || !validProbeArguments(item.Arguments) {
					return fmt.Errorf("function call %q missing call id, name or object arguments", item.ID)
				}
				trace.FunctionCalls = append(trace.FunctionCalls, item)
			}
		case "error":
			if event.Error != nil {
				event.Code, event.Message = event.Error.Code, event.Error.Message
			}
			if event.Code == "" || event.Message == "" {
				return fmt.Errorf("provider error missing code or message")
			}
			trace.ErrorCode, trace.ErrorMessage = event.Code, event.Message
			terminal = true
		case "response.failed":
			if event.Response.Error == nil {
				return fmt.Errorf("response.failed missing error")
			}
			if trace.ErrorCode == "" {
				trace.ErrorCode, trace.ErrorMessage = event.Response.Error.Code, event.Response.Error.Message
			}
			terminal = true
		case "response.completed", "response.incomplete":
			if event.Response.ID != trace.ResponseID {
				return fmt.Errorf("terminal response id does not match response.created")
			}
			terminal = true
			if event.Type != "response.completed" || event.Response.Status != "completed" {
				return fmt.Errorf("response did not complete: %s", event.Response.Status)
			}
		}
		return nil
	}
	if err := readProbeSSE(reader, consume); err != nil {
		return trace, err
	}
	if !created {
		return trace, fmt.Errorf("missing response.created")
	}
	if !terminal {
		return trace, fmt.Errorf("missing terminal response event")
	}
	if trace.ErrorCode != "" {
		trace.Outcome = "providerError"
		return trace, fmt.Errorf("%s: %s", trace.ErrorCode, trace.ErrorMessage)
	}
	trace.Outcome = "completed"
	return trace, nil
}

// probeAlphaSearchToolOutput renders alpha/search results as the function
// output text returned to the model.
func probeAlphaSearchToolOutput(body []byte) (string, error) {
	var response struct {
		Output  string `json:"output"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("decode alpha search response: %w", err)
	}
	if len(response.Results) > 0 {
		data, err := json.Marshal(response.Results)
		return string(data), err
	}
	if response.Output != "" {
		return response.Output, nil
	}
	return "", fmt.Errorf("alpha search returned neither output nor results")
}

func probeFunctionCallFollowUp(call probeTraceItem, output string) ([]json.RawMessage, error) {
	callItem, err := json.Marshal(map[string]any{
		"type": "function_call", "call_id": call.CallID, "name": call.Name, "arguments": call.Arguments,
	})
	if err != nil {
		return nil, err
	}
	outputItem, err := json.Marshal(map[string]any{
		"type": "function_call_output", "call_id": call.CallID, "output": output,
	})
	if err != nil {
		return nil, err
	}
	return []json.RawMessage{callItem, outputItem}, nil
}

// runProbeLiteStage reports request failures in the returned stage; the error
// is reserved for local failures that should abort the probe.
func runProbeLiteStage(ctx context.Context, base http.RoundTripper, captured *probeCapturedRequest, lite bool, model string, body []byte, dir, name string) (probeLiteStage, error) {
	headers := probeMinimalHeaders(captured.Header, "text/event-stream")
	if lite {
		headers.Set(probeLiteHeader, "true")
	}
	request := &probeCapturedRequest{URL: captured.URL, Header: headers, Body: body}
	if err := os.WriteFile(filepath.Join(dir, name+".request.json"), body, 0o600); err != nil {
		return probeLiteStage{}, err
	}
	transport := &probeCaptureTransport{base: base, expected: request}
	runErr := runRawProbe(ctx, request, transport)
	responseBody := transport.responseBody.Bytes()
	if err := os.WriteFile(filepath.Join(dir, name+".sse"), responseBody, 0o600); err != nil {
		return probeLiteStage{}, err
	}

	stage := probeLiteStage{Request: name + ".request.json", HTTPStatus: transport.statusCode}
	var traceErr error
	switch {
	case transport.calls != 1:
		traceErr = fmt.Errorf("expected one HTTP call, got %d", transport.calls)
	case transport.statusCode != http.StatusOK:
		stage.Trace.Outcome = "httpError"
		traceErr = fmt.Errorf("HTTP %d: %s", transport.statusCode, truncateProbeText(responseBody, 500))
	default:
		stage.Trace, traceErr = analyzeProbeLiteSSE(bytes.NewReader(responseBody))
		if traceErr == nil && !strings.HasPrefix(stage.Trace.Model, model) {
			traceErr = fmt.Errorf("response model %q does not match requested %q", stage.Trace.Model, model)
		}
	}
	if combined := errors.Join(runErr, traceErr); combined != nil {
		stage.Error = combined.Error()
	}
	return stage, nil
}

func runProbeLiteControl(ctx context.Context, base http.RoundTripper, captured *probeCapturedRequest, model string, control probeLiteControl, attempt int, dir string) (probeLiteRun, error) {
	name := fmt.Sprintf("%s-%s-%d", control.Name, model, attempt)
	run := probeLiteRun{
		Name: name, Control: control.Name, Lite: control.Lite, ClientSearch: control.ClientSearch,
		Model: model, Outcome: "failed",
	}
	body, err := buildProbeLiteBody(model, control, nil)
	if err != nil {
		return run, err
	}
	run.Initial, err = runProbeLiteStage(ctx, base, captured, control.Lite, model, body, dir, name)
	if err != nil {
		return run, err
	}
	if run.Initial.Error != "" {
		run.Error = run.Initial.Error
		return run, nil
	}
	trace := run.Initial.Trace

	if !control.ClientSearch {
		switch {
		case trace.SearchesCompleted == 0:
			run.Outcome, run.Error = "inconclusive", "no completed hosted web search"
		case trace.MessagesCompleted == 0:
			run.Error = "no answer message after hosted web search"
		default:
			run.Outcome = "success"
		}
		return run, nil
	}

	if len(trace.FunctionCalls) == 0 {
		run.Outcome, run.Error = "inconclusive", "model did not call "+probeLiteSearchFunction
		return run, nil
	}
	var followUp []json.RawMessage
	for round := 1; ; round++ {
		if trace.SearchesCompleted > 0 {
			run.Error = "hosted web search ran although only a client search function was declared"
			return run, nil
		}
		if len(trace.FunctionCalls) == 0 {
			if trace.MessagesCompleted == 0 {
				run.Error = "follow-up completed without an answer message"
				return run, nil
			}
			run.Outcome = "success"
			return run, nil
		}
		if round > probeLiteMaxSearchRounds {
			run.Outcome = "inconclusive"
			run.Error = fmt.Sprintf("model still searching after %d rounds", probeLiteMaxSearchRounds)
			return run, nil
		}
		for i, call := range trace.FunctionCalls {
			if call.Name != probeLiteSearchFunction {
				run.Error = fmt.Sprintf("unexpected function call %q", call.Name)
				return run, nil
			}
			var arguments struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal([]byte(call.Arguments), &arguments); err != nil || strings.TrimSpace(arguments.Query) == "" {
				run.Error = "search function call missing query"
				return run, nil
			}
			alpha, alphaBody, err := runProbeAlphaSearch(ctx, base, captured, model, arguments.Query, fmt.Sprintf("%s-alpha-%d-%d", name, round, i), dir)
			if err != nil {
				return run, err
			}
			run.AlphaSearches = append(run.AlphaSearches, alpha)
			if alpha.Error != "" {
				run.Error = "alpha search: " + alpha.Error
				return run, nil
			}
			output, err := probeAlphaSearchToolOutput(alphaBody)
			if err != nil {
				run.Error = err.Error()
				return run, nil
			}
			items, err := probeFunctionCallFollowUp(call, output)
			if err != nil {
				return run, err
			}
			followUp = append(followUp, items...)
		}

		body, err = buildProbeLiteBody(model, control, followUp)
		if err != nil {
			return run, err
		}
		stage, err := runProbeLiteStage(ctx, base, captured, control.Lite, model, body, dir, fmt.Sprintf("%s-followup-%d", name, round))
		if err != nil {
			return run, err
		}
		run.FollowUps = append(run.FollowUps, stage)
		if stage.Error != "" {
			run.Error = fmt.Sprintf("follow-up %d: %s", round, stage.Error)
			return run, nil
		}
		trace = stage.Trace
	}
}

// executeLiteSearchProbe compares hosted and client-executed web search in
// both the ordinary and Responses Lite request dialects, holding endpoint,
// credentials and model fixed.
func executeLiteSearchProbe(ctx context.Context, activities *persisted_ai.Llm2Activities, input persisted_ai.StreamInput, dir, model string, repeats int) (json.RawMessage, error) {
	if repeats < 0 {
		return nil, fmt.Errorf("lite probe repeats must not be negative")
	}
	if repeats == 0 {
		repeats = 2
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create new private probe directory: %w", err)
	}
	base := http.DefaultTransport
	captured, err := captureProbeSearchOnly(ctx, activities, input, base)
	if err != nil {
		return nil, err
	}
	if captured.Header.Get("ChatGPT-Account-Id") == "" {
		return nil, fmt.Errorf("lite probe requires OpenAI subscription auth on the codex backend, got endpoint %s", captured.URL)
	}
	if model == "" {
		var fields struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(captured.Body, &fields); err != nil || fields.Model == "" {
			return nil, fmt.Errorf("captured request has no model: %w", err)
		}
		model = fields.Model
	}

	report := probeLiteReport{Endpoint: captured.URL, AuthMode: "subscription", Model: model}
	writeReport := func() error {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "report.json"), data, 0o600)
	}
	var failures []error
	for attempt := 1; attempt <= repeats; attempt++ {
		for _, control := range probeLiteControls {
			run, err := runProbeLiteControl(ctx, base, captured, model, control, attempt, dir)
			if err != nil {
				return nil, errors.Join(err, errors.Join(failures...))
			}
			report.Runs = append(report.Runs, run)
			fmt.Fprintf(os.Stderr, "%s\t%s\t%s\n", run.Name, run.Outcome, run.Error)
			if run.Outcome != "success" {
				failures = append(failures, fmt.Errorf("%s: %s: %s", run.Name, run.Outcome, run.Error))
			}
			if err := writeReport(); err != nil {
				return nil, err
			}
			if ctx.Err() != nil {
				return nil, errors.Join(ctx.Err(), errors.Join(failures...))
			}
		}
	}
	data, err := json.Marshal(report)
	return data, errors.Join(err, errors.Join(failures...))
}
