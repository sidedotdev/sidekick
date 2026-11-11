package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"sidekick/llm2"
)

type probeHistoryEdits struct {
	Keep       *[]int `json:"keep"`
	KeepRanges string `json:"keepRanges"`
	Rename     []struct {
		Tool string `json:"tool"`
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"rename"`
	Replace []struct {
		Messages *[]int `json:"messages"`
		Old      string `json:"old"`
		New      string `json:"new"`
	} `json:"replace"`
	Inspect bool   `json:"inspect"`
	Match   string `json:"match"`
	Show    []int  `json:"show"`
}

func editProbeHistory(messages []llm2.Message, raw json.RawMessage) ([]llm2.Message, error) {
	var edits probeHistoryEdits
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&edits); err != nil {
		return nil, fmt.Errorf("decode probeEdits: %w", err)
	}
	data, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	var result []llm2.Message
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if edits.Inspect || edits.Match != "" || len(edits.Show) != 0 {
		if err := inspectProbeHistory(result, edits); err != nil {
			return nil, err
		}
	}
	for _, rename := range edits.Rename {
		if rename.Tool == "" || rename.From == "" || rename.To == "" || rename.From == rename.To {
			return nil, fmt.Errorf("argument rename requires a tool and distinct nonempty field names")
		}
		count := 0
		for i := range result {
			for j := range result[i].Content {
				tool := result[i].Content[j].ToolUse
				if tool == nil || tool.Name != rename.Tool {
					continue
				}
				var arguments map[string]json.RawMessage
				if err := json.Unmarshal([]byte(tool.Arguments), &arguments); err != nil {
					return nil, fmt.Errorf("decode %s arguments in message %d: %w", tool.Name, i, err)
				}
				value, exists := arguments[rename.From]
				if !exists {
					continue
				}
				if _, collision := arguments[rename.To]; collision {
					return nil, fmt.Errorf("message %d already has argument %q", i, rename.To)
				}
				delete(arguments, rename.From)
				arguments[rename.To] = value
				encoded, err := json.Marshal(arguments)
				if err != nil {
					return nil, err
				}
				tool.Arguments = string(encoded)
				count++
			}
		}
		if count == 0 {
			return nil, fmt.Errorf("argument rename for %s.%s made no changes", rename.Tool, rename.From)
		}
		fmt.Fprintf(os.Stderr, "Probe renamed %s.%s to %s in %d calls\n", rename.Tool, rename.From, rename.To, count)
	}
	for _, replacement := range edits.Replace {
		if replacement.Old == "" || replacement.Old == replacement.New {
			return nil, fmt.Errorf("text replacement requires distinct values and a nonempty old value")
		}
		selected, err := probeMessageSelection(replacement.Messages, len(result))
		if err != nil {
			return nil, err
		}
		count := 0
		for _, index := range selected {
			encoded, err := json.Marshal(result[index])
			if err != nil {
				return nil, err
			}
			var value any
			if err := json.Unmarshal(encoded, &value); err != nil {
				return nil, err
			}
			value = replaceProbeStrings(value, replacement.Old, replacement.New, &count)
			encoded, err = json.Marshal(value)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(encoded, &result[index]); err != nil {
				return nil, err
			}
		}
		if count == 0 {
			return nil, fmt.Errorf("text replacement %q made no changes", replacement.Old)
		}
		fmt.Fprintf(os.Stderr, "Probe replaced %q %d times\n", replacement.Old, count)
	}
	if edits.KeepRanges != "" {
		if edits.Keep != nil {
			return nil, fmt.Errorf("use either keep or keepRanges, not both")
		}
		var indices []int
		for _, part := range strings.Split(edits.KeepRanges, ",") {
			bounds := strings.Split(part, "-")
			first, err := strconv.Atoi(bounds[0])
			if err != nil || len(bounds) > 2 {
				return nil, fmt.Errorf("invalid message range %q", part)
			}
			last := first
			if len(bounds) == 2 {
				last, err = strconv.Atoi(bounds[1])
				if err != nil {
					return nil, fmt.Errorf("invalid message range %q", part)
				}
			}
			if first < 0 || last < first || last >= len(result) {
				return nil, fmt.Errorf("message range %q outside history of %d messages", part, len(result))
			}
			for i := first; i <= last; i++ {
				indices = append(indices, i)
			}
		}
		edits.Keep = &indices
	}
	selected, err := probeMessageSelection(edits.Keep, len(result))
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("probe history must not be empty")
	}
	kept := make([]llm2.Message, 0, len(selected))
	for _, index := range selected {
		kept = append(kept, result[index])
	}
	return kept, nil
}

func probeMessageSelection(indices *[]int, count int) ([]int, error) {
	if indices == nil {
		all := make([]int, count)
		for i := range all {
			all[i] = i
		}
		return all, nil
	}
	previous := -1
	for _, index := range *indices {
		if index < 0 || index >= count || index <= previous {
			return nil, fmt.Errorf("message indices must be increasing, unique, and within [0,%d)", count)
		}
		previous = index
	}
	return *indices, nil
}

func replaceProbeStrings(value any, old, replacement string, count *int) any {
	switch v := value.(type) {
	case string:
		*count += strings.Count(v, old)
		return strings.ReplaceAll(v, old, replacement)
	case []any:
		for i := range v {
			v[i] = replaceProbeStrings(v[i], old, replacement, count)
		}
	case map[string]any:
		for key, child := range v {
			v[key] = replaceProbeStrings(child, old, replacement, count)
		}
	}
	return value
}

func inspectProbeHistory(messages []llm2.Message, edits probeHistoryEdits) error {
	if _, err := probeMessageSelection(&edits.Show, len(messages)); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Probe history: %d messages\n", len(messages))
	for i, message := range messages {
		data, err := json.Marshal(message)
		if err != nil {
			return err
		}
		var labels []string
		for _, block := range message.Content {
			label := string(block.Type)
			if block.ToolUse != nil {
				label += ":" + block.ToolUse.Name
			}
			if block.ToolResult != nil {
				label += ":" + block.ToolResult.Name
			}
			labels = append(labels, label)
		}
		fmt.Fprintf(os.Stderr, "MESSAGE %d role=%s bytes=%d blocks=%s\n", i, message.Role, len(data), strings.Join(labels, ","))
		if edits.Match != "" {
			text := string(data)
			for start := 0; start < len(text); {
				offset := strings.Index(text[start:], edits.Match)
				if offset < 0 {
					break
				}
				offset += start
				left, right := max(0, offset-100), min(len(text), offset+len(edits.Match)+200)
				fmt.Fprintf(os.Stderr, "MATCH message=%d offset=%d %s\n", i, offset, text[left:right])
				start = offset + len(edits.Match)
			}
		}
		for _, shown := range edits.Show {
			if shown == i {
				pretty, err := json.MarshalIndent(message, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "SHOW %d\n%s\n", i, pretty)
			}
		}
	}
	return nil
}
