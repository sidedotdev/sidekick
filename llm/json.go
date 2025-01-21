package llm

import (
	"bytes"
	"encoding/json"
	"strings"
)

func tryParseStringAsJson(input string) interface{} {
	// Trim whitespace
	trimmed := strings.TrimSpace(input)

	// remove "</invoke>" and any characters after it with regex
	trimmed = strings.Split(trimmed, "</invoke>")[0]

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

// escapeNewLinesInJSON tries to repair JSON that has unescaped newlines by escaping them.
// It is robust against valid JSON escapes like `\"` and will only escape newlines inside strings.
// tryConvertStringsToRawJson attempts to convert string values in a JSON structure to raw JSON messages
// where possible, validating that the entire structure remains valid JSON after each conversion.
func tryConvertStringsToRawJson(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		// First pass: copy all values
		for key, value := range v {
			result[key] = tryConvertStringsToRawJson(value)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		// First pass: copy all values
		for i, value := range v {
			result[i] = tryConvertStringsToRawJson(value)
		}
		return result
	case string:
		// Try to parse the string as JSON
		var parsed interface{}
		if err := json.Unmarshal([]byte(v), &parsed); err != nil {
			return v
		}

		// Only convert if parsed result is an object or array
		switch parsed.(type) {
		case map[string]interface{}, []interface{}:
			return parsed
		default:
			return v
		}
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
