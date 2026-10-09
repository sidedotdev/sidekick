// summarize_stream_results reads the verbose output of dump_workflow_events
// (filtered to the Stream activity) on stdin and prints, for every Stream
// activity attempt, the response's provider/model, stop reason and the ordered
// content block types, or the failure message for failed attempts. It also
// reports whether the matching scheduled input offered the web_search tool, so
// builtin block loss can be spotted at the activity boundary without printing
// prompts or secrets.
//
// Example:
//
//	go run ./scripts/dump_workflow_events -verbose -activity-type Stream <flow-id> \
//	  | go run ./scripts/summarize_stream_results
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"sidekick/llm2"
)

var (
	scheduledLine = regexp.MustCompile(`^(\d+) \S+ ActivityTaskScheduled Stream id=(\d+)`)
	completedLine = regexp.MustCompile(`^(\d+) \S+ ActivityTaskCompleted Stream scheduled=(\d+)`)
	failedLine    = regexp.MustCompile(`^(\d+) \S+ ActivityTaskFailed Stream scheduled=(\d+)`)
)

type streamInputSummary struct {
	Options struct {
		Tools []struct {
			Type string `json:"type"`
		} `json:"tools"`
	} `json:"Options"`
	FlowActionId string `json:"FlowActionId"`
}

func (s streamInputSummary) webSearchOffered() bool {
	for _, tool := range s.Options.Tools {
		if tool.Type == "web_search" {
			return true
		}
	}
	return false
}

type summaryTotals struct {
	scheduled, completed, failed int
	blockTypes                   map[string]int
}

func main() {
	if err := summarize(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// summarize fails loudly on any payload it cannot decode or correlate so a
// summary never silently under-reports what the history contains.
func summarize(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 256*1024*1024)

	inputs := map[string]streamInputSummary{}
	totals := summaryTotals{blockTypes: map[string]int{}}
	// Payload lines follow their lifecycle line, so only one of these is
	// pending at a time.
	pendingInput, pendingResult := "", ""

	for scanner.Scan() {
		line := scanner.Text()
		if m := scheduledLine.FindStringSubmatch(line); m != nil {
			if pendingInput != "" {
				return fmt.Errorf("scheduled=%s has no Input payload", pendingInput)
			}
			pendingInput = m[2]
			totals.scheduled++
			continue
		}
		if m := completedLine.FindStringSubmatch(line); m != nil {
			if pendingResult != "" {
				return fmt.Errorf("scheduled=%s has no Result payload", pendingResult)
			}
			if _, ok := inputs[m[2]]; !ok {
				return fmt.Errorf("completed scheduled=%s has no matching scheduled input", m[2])
			}
			pendingResult = m[2]
			continue
		}
		if m := failedLine.FindStringSubmatch(line); m != nil {
			input, ok := inputs[m[2]]
			if !ok {
				return fmt.Errorf("failed scheduled=%s has no matching scheduled input", m[2])
			}
			totals.failed++
			fmt.Fprintf(w, "scheduled=%s action=%s FAILED\n", m[2], input.FlowActionId)
			continue
		}

		trimmed := strings.TrimSpace(line)
		switch {
		case pendingInput != "" && strings.HasPrefix(trimmed, "Input[0]: "):
			var input streamInputSummary
			if err := json.Unmarshal([]byte(strings.TrimPrefix(trimmed, "Input[0]: ")), &input); err != nil {
				return fmt.Errorf("scheduled=%s: cannot parse input: %w", pendingInput, err)
			}
			if input.FlowActionId == "" {
				return fmt.Errorf("scheduled=%s: input has no FlowActionId; was dump_workflow_events run with -verbose?", pendingInput)
			}
			inputs[pendingInput] = input
			pendingInput = ""
		case pendingResult != "" && strings.HasPrefix(trimmed, "Result[0]: "):
			var response llm2.MessageResponse
			if err := json.Unmarshal([]byte(strings.TrimPrefix(trimmed, "Result[0]: ")), &response); err != nil {
				return fmt.Errorf("scheduled=%s: cannot parse result: %w", pendingResult, err)
			}
			if response.Output.Role == "" {
				return fmt.Errorf("scheduled=%s: result is not an llm2 MessageResponse", pendingResult)
			}
			input := inputs[pendingResult]
			var blocks []string
			for _, block := range response.Output.Content {
				desc := string(block.Type)
				if block.BuiltinToolUse != nil {
					desc += fmt.Sprintf("(%s %s)", block.BuiltinToolUse.Name, block.BuiltinToolUse.Status)
				}
				blocks = append(blocks, desc)
				totals.blockTypes[string(block.Type)]++
			}
			totals.completed++
			fmt.Fprintf(w, "scheduled=%s action=%s %s/%s webSearchOffered=%v stop=%s blocks=[%s]\n",
				pendingResult, input.FlowActionId, response.Provider, response.Model,
				input.webSearchOffered(), response.StopReason, strings.Join(blocks, ", "))
			pendingResult = ""
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read error: %w", err)
	}
	if pendingInput != "" {
		return fmt.Errorf("scheduled=%s has no Input payload", pendingInput)
	}
	if pendingResult != "" {
		return fmt.Errorf("scheduled=%s has no Result payload", pendingResult)
	}

	fmt.Fprintf(w, "\n%d scheduled, %d completed, %d failed; block type totals: %v\n",
		totals.scheduled, totals.completed, totals.failed, totals.blockTypes)
	return nil
}
