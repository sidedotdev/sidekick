package llm

import (
	"bytes"
	"encoding/json"
	"errors"
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
		// Trim whitespace
		trimmed := strings.TrimSpace(str)

		// remove "</invoke>" and any characters after it with regex
		trimmed = strings.Split(trimmed, "</invoke>")[0]
		trimmed = strings.TrimSpace(trimmed)

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
