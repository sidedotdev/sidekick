package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

type probeTraceReport struct {
	Outcome            string `json:"outcome"`
	ResponseID         string `json:"responseId"`
	Model              string `json:"model"`
	EventCount         int    `json:"eventCount"`
	LastEventType      string `json:"lastEventType"`
	SearchesCompleted  int    `json:"searchesCompleted"`
	TransitionObserved bool   `json:"transitionObserved"`
	FunctionItemID     string `json:"functionItemId,omitempty"`
	ArgumentDeltaBytes int    `json:"argumentDeltaBytes"`
	ErrorCode          string `json:"errorCode,omitempty"`
	ErrorMessage       string `json:"errorMessage,omitempty"`
}

type probeTraceItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	CallID    string `json:"call_id,omitempty"`
	Status    string `json:"status"`
	Arguments string `json:"arguments"`
}

type probeTraceEvent struct {
	Type      string         `json:"type"`
	Item      probeTraceItem `json:"item"`
	ItemID    string         `json:"item_id"`
	Delta     string         `json:"delta"`
	Arguments string         `json:"arguments"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Error     *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Response struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Status string `json:"status"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

func analyzeProbeSSE(reader io.Reader, functionName string) (probeTraceReport, error) {
	report := probeTraceReport{Outcome: "invalid"}
	items := make(map[string]probeTraceItem)
	created, terminal, functionDone := false, false, false
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
		if terminal {
			if report.LastEventType != "error" || event.Type != "response.failed" ||
				event.Response.ID != report.ResponseID || event.Response.Status != "failed" ||
				event.Response.Error == nil || event.Response.Error.Code != report.ErrorCode ||
				event.Response.Error.Message != report.ErrorMessage {
				return fmt.Errorf("event after terminal response event")
			}
		}
		if event.Type == "" || (eventName != "" && eventName != event.Type) {
			return fmt.Errorf("SSE event type mismatch: %q / %q", eventName, event.Type)
		}
		report.EventCount++
		report.LastEventType = event.Type
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
			report.ResponseID = event.Response.ID
			report.Model = event.Response.Model
		case "response.output_item.added":
			item := event.Item
			if item.ID == "" || item.Type == "" {
				return fmt.Errorf("output item missing id or type")
			}
			if _, exists := items[item.ID]; exists {
				return fmt.Errorf("duplicate output item %q", item.ID)
			}
			if functionName == "" && item.Type == "function_call" {
				return fmt.Errorf("unexpected function in search-only response")
			}
			items[item.ID] = item
			if item.Type == "function_call" && item.Name == functionName && report.SearchesCompleted > 0 && report.FunctionItemID == "" {
				report.TransitionObserved = true
				report.FunctionItemID = item.ID
			}
		case "response.output_item.done":
			item := event.Item
			prior, exists := items[item.ID]
			if !exists {
				return fmt.Errorf("completion for unknown item %q", item.ID)
			}
			if prior.Type != item.Type || prior.Status == "completed" {
				return fmt.Errorf("inconsistent completion for item %q", item.ID)
			}
			if item.Type == "web_search_call" && item.Status == "completed" {
				report.SearchesCompleted++
			}
			if item.ID == report.FunctionItemID {
				if item.Name != functionName || item.Status != "completed" || !validProbeArguments(item.Arguments) {
					return fmt.Errorf("invalid completed function arguments or status")
				}
				functionDone = true
			}
			items[item.ID] = item
		case "response.function_call_arguments.delta", "response.function_call_arguments.done":
			item, exists := items[event.ItemID]
			if !exists || item.Type != "function_call" {
				return fmt.Errorf("arguments for unknown function item %q", event.ItemID)
			}
			if event.Type == "response.function_call_arguments.done" && !validProbeArguments(event.Arguments) {
				return fmt.Errorf("invalid function arguments")
			}
			if event.ItemID == report.FunctionItemID && event.Type == "response.function_call_arguments.delta" {
				report.ArgumentDeltaBytes += len(event.Delta)
			}
		case "error":
			if event.Error != nil {
				event.Code, event.Message = event.Error.Code, event.Error.Message
			}
			if event.Code == "" || event.Message == "" {
				return fmt.Errorf("provider error missing code or message")
			}
			report.ErrorCode, report.ErrorMessage = event.Code, event.Message
			terminal = true
		case "response.failed":
			if event.Response.Error == nil {
				return fmt.Errorf("response.failed missing error")
			}
			report.ErrorCode = event.Response.Error.Code
			report.ErrorMessage = event.Response.Error.Message
			terminal = true
		case "response.completed", "response.incomplete":
			if event.Response.ID != report.ResponseID {
				return fmt.Errorf("terminal response id does not match response.created")
			}
			if event.Type != "response.completed" || event.Response.Status != "completed" {
				return fmt.Errorf("response did not complete: %s", event.Response.Status)
			}
			terminal = true
		}
		return nil
	}
	if err := readProbeSSE(reader, consume); err != nil {
		return report, err
	}
	if !created {
		return report, fmt.Errorf("missing response.created")
	}
	if !terminal {
		return report, fmt.Errorf("missing terminal response event")
	}
	if report.ErrorCode != "" {
		report.Outcome = "providerError"
		return report, fmt.Errorf("%s: %s", report.ErrorCode, report.ErrorMessage)
	}
	if functionName == "" {
		if report.SearchesCompleted == 0 {
			report.Outcome = "inconclusive"
			return report, fmt.Errorf("no completed web search")
		}
		report.Outcome = "success"
		return report, nil
	}
	if !report.TransitionObserved {
		report.Outcome = "inconclusive"
		return report, fmt.Errorf("no completed web search followed by %s", functionName)
	}
	if !functionDone {
		return report, fmt.Errorf("response completed without completed function %s", functionName)
	}
	report.Outcome = "success"
	return report, nil
}

func validProbeArguments(arguments string) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal([]byte(arguments), &object) == nil && object != nil
}

func readProbeSSE(reader io.Reader, consume func(string, []byte) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 32*1024*1024)
	var data []string
	eventName := ""
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(data) > 0 {
				if err := consume(eventName, []byte(strings.Join(data, "\n"))); err != nil {
					return err
				}
			}
			data = nil
			eventName = ""
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventName = value
		case "data":
			data = append(data, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read SSE: %w", err)
	}
	if len(data) > 0 || eventName != "" {
		return fmt.Errorf("unterminated SSE frame")
	}
	return nil
}

func validateProbeRequest(expected, actual []byte) error {
	decode := func(data []byte) (map[string]any, error) {
		var object map[string]any
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.UseNumber()
		if err := decoder.Decode(&object); err != nil {
			return nil, err
		}
		if object == nil {
			return nil, fmt.Errorf("request must be a JSON object")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, fmt.Errorf("trailing request data")
		}
		return object, nil
	}
	want, err := decode(expected)
	if err != nil {
		return fmt.Errorf("decode expected request: %w", err)
	}
	got, err := decode(actual)
	if err != nil {
		return fmt.Errorf("decode actual request: %w", err)
	}
	if !reflect.DeepEqual(want, got) {
		var changed []string
		for key, value := range want {
			actualValue, exists := got[key]
			if !exists || !reflect.DeepEqual(value, actualValue) {
				changed = append(changed, key)
			}
		}
		for key := range got {
			if _, exists := want[key]; !exists {
				changed = append(changed, key)
			}
		}
		sort.Strings(changed)
		return fmt.Errorf("wire request differs from expected request in fields: %s", strings.Join(changed, ", "))
	}
	return nil
}
