package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

func tryParseStringAsJson(input string) interface{} {
	// Trim whitespace
	trimmed := strings.TrimSpace(input)

	// remove "</invoke>" and any characters after it with regex
	trimmed = strings.Split(trimmed, "</invoke>")[0]
	trimmed = strings.TrimSpace(trimmed)

	// Try to parse as JSON
	var parsed interface{}
	err := json.Unmarshal([]byte(trimmed), &parsed)
	if err != nil {
		return input
	}

	// check if map or array, only those can be interpreted as JSON
	switch parsed.(type) {
	case map[string]interface{}, []interface{}:
		return parsed
	default:
		return input
	}
}

func RepairJson(input string) string {
	return repairJson(input, false)
}

// RepairJsonFull applies every JSON repair pass, including recovery of fields
// whose values leaked into a preceding string value due to broken upstream
// tool-call/XML parsing. It is intended to run inside activities so the
// repaired result is persisted in workflow history. Deterministic workflow code
// keeps calling RepairJson, so replay of in-flight executions is unaffected
// until those executions drain and the workflow-side calls can be removed.
func RepairJsonFull(input string) string {
	return repairJson(input, true)
}

func repairJson(input string, recoverLeakedParams bool) string {
	// First escape newlines in JSON strings
	escaped := escapeNewLinesInJSON(input)

	// check if treating any string values in maps as json.RawMessage results in
	// an overall valid JSON structure. if so, that new json structure is what's returned
	escaped = tryParseStringsAsJsonRawMessages(escaped)

	// Parse the JSON structure
	var data interface{}
	if err := json.Unmarshal([]byte(escaped), &data); err != nil {
		return escaped // Return escaped string if not valid JSON
	}

	if recoverLeakedParams {
		// Recover fields whose values leaked into a preceding string value due
		// to broken upstream XML/tool-call parsing
		data = repairLeakedXmlParams(data)
	}

	// Process all string values in the structure
	processed := processJsonStrings(data)

	// Marshal back to JSON string
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(processed)
	if err != nil {
		return escaped // Return escaped string if marshaling fails
	}

	return strings.TrimSpace(buffer.String())
}

// leakedXmlJunkRe matches the point in a string value where a sibling field's
// value leaked in, e.g. `... </analysis> <parameter name="steps">`. The optional
// leading closing tag is stripped along with the parameter markup.
var leakedXmlJunkRe = regexp.MustCompile(`(?:</[a-zA-Z][^>]*>\s*)?<parameter name="`)

// leakedXmlParamRe matches the opening marker of a leaked parameter, capturing
// its name.
var leakedXmlParamRe = regexp.MustCompile(`<parameter name="([^"]+)">`)

// repairLeakedXmlParams walks a parsed JSON structure and, for any string value
// that contains leaked `<parameter name="...">` markup, truncates the string to
// its real content and promotes the leaked parameters to sibling fields.
func repairLeakedXmlParams(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(v))
		additions := make(map[string]interface{})
		for key, value := range v {
			processed := repairLeakedXmlParams(value)
			if s, ok := processed.(string); ok {
				if cleaned, params, found := extractLeakedXmlParams(s); found {
					result[key] = cleaned
					for name, val := range params {
						additions[name] = val
					}
					continue
				}
			}
			result[key] = processed
		}
		for name, val := range additions {
			if _, exists := result[name]; !exists {
				result[name] = val
			}
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, value := range v {
			result[i] = repairLeakedXmlParams(value)
		}
		return result
	default:
		return v
	}
}

// extractLeakedXmlParams splits a string value at the point where leaked
// `<parameter name="...">` markup begins. It returns the cleaned string content
// preceding the markup and a map of the leaked parameter names to their parsed
// values. found is false when no leaked markup is present.
func extractLeakedXmlParams(s string) (cleaned string, params map[string]interface{}, found bool) {
	loc := leakedXmlJunkRe.FindStringIndex(s)
	if loc == nil {
		return s, nil, false
	}
	cleaned = s[:loc[0]]
	rest := s[loc[0]:]

	matches := leakedXmlParamRe.FindAllStringSubmatchIndex(rest, -1)
	if len(matches) == 0 {
		return s, nil, false
	}

	params = make(map[string]interface{}, len(matches))
	for i, m := range matches {
		name := rest[m[2]:m[3]]
		valStart := m[1]
		valEnd := len(rest)
		if i+1 < len(matches) {
			valEnd = matches[i+1][0]
		}
		raw := strings.TrimSpace(rest[valStart:valEnd])
		raw = strings.TrimSuffix(raw, "</parameter>")
		raw = strings.TrimSpace(raw)

		var parsed interface{}
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			params[name] = parsed
		} else {
			params[name] = raw
		}
	}

	return cleaned, params, true
}

// check if treating any string values in maps as json.RawMessage results in an
// overall valid JSON structure. if so, that new json structure is what's returned.
// IMPORTANT: we can't just parse the string as json. the whole point of this is
// that it won't parse as json on its own, but the overall json message will
// parse once we remove the double quotes and unescape the double quotes within
// the string. if the overall message fails to parse after that, we can continue
// and try the next string, until all are exhausted.
func tryParseStringsAsJsonRawMessages(input string) string {
	err, strs := extractAllJsonStrings(input)
	if err != nil {
		return input // return escaped string if not valid JSON
	}

	// for each str, try to replace it in the input with a version that removes
	// the str's outer double quotes and unescapes double quotes within
	for _, str := range strs {
		// if the string doesn't contain one of these characters, skip it, as we
		// don't want to parse strings that should remain strings as-is
		if !strings.ContainsAny(str, `"{}[]`) {
			continue
		}

		// Trim whitespace
		trimmed := strings.TrimSpace(str)

		// remove "</invoke>" and any characters after it with regex
		trimmed = strings.Split(trimmed, "</invoke>")[0]

		// trim trailing commas (repeated within string)
		trimmed = strings.TrimSpace(trimmed)
		trimmed = strings.TrimRight(trimmed, ",")

		buffer := &bytes.Buffer{}
		encoder := json.NewEncoder(buffer)
		encoder.SetEscapeHTML(false)
		err := encoder.Encode(str)
		if err != nil {
			continue
		}
		jsonStr := strings.TrimSpace(buffer.String())
		maybeJson := strings.Replace(input, jsonStr, trimmed, 1)

		var data interface{}
		err = json.Unmarshal([]byte(maybeJson), &data)
		if err == nil { // if it parses as valid JSON, replace input with this
			input = maybeJson
			continue
		}

		if strings.HasSuffix(trimmed, "}") {
			trimmed = strings.TrimSuffix(trimmed, "}")
			maybeJson := strings.Replace(input, jsonStr, trimmed, 1)

			var data interface{}
			err := json.Unmarshal([]byte(maybeJson), &data)
			if err == nil { // if it parses as valid JSON, replace input with this
				input = maybeJson
			}
		}
	}

	return input
}

func extractAllJsonStrings(input string) (error, []string) {
	// Parse the JSON structure
	var data interface{}
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		return errors.New("invalid JSON"), nil
	}

	// Traverse the JSON structure to find all string values
	var strs []string
	var stack []interface{}
	stack = append(stack, data)
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch v := current.(type) {
		case string:
			strs = append(strs, v)
		case map[string]interface{}:
			for _, value := range v {
				stack = append(stack, value)
			}
		case []interface{}:
			for _, value := range v {
				stack = append(stack, value)
			}
		}
	}

	return nil, strs
}

// processJsonStrings walks through a JSON structure and attempts to parse string values as JSON
func processJsonStrings(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, value := range v {
			result[key] = processJsonStrings(value)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, value := range v {
			result[i] = processJsonStrings(value)
		}
		return result
	case string:
		return tryParseStringAsJson(v)
	default:
		return v
	}
}

func escapeNewLinesInJSON(input string) string {
	var inString, wasBackslash bool
	var result strings.Builder

	for i := 0; i < len(input); i++ {
		c := input[i]
		if c == '\\' && !wasBackslash {
			wasBackslash = true
			result.WriteByte(c)
			continue
		}
		if c == '"' && !wasBackslash {
			inString = !inString
			result.WriteByte(c)
			continue
		}
		if inString && !wasBackslash {
			if c == 'n' && i > 0 && input[i-1] == '\\' {
				result.WriteString("n")
			} else if c == '\n' {
				result.WriteString("\\n")
			} else if c == '\r' && i+1 < len(input) && input[i+1] == '\n' {
				result.WriteString("\\r\\n")
				i++ // skip the next character, which is the newline
			} else {
				result.WriteByte(c)
			}
		} else {
			result.WriteByte(c)
		}
		wasBackslash = false
	}
	return result.String()
}
