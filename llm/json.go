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
	var data interface{}
	// Unmarshal input JSON into a generic interface
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		return input
	}
	
	// Recursively repair string values in maps
	repaired, changed := repairValue(data)
	if changed {
		buffer := &bytes.Buffer{}
		encoder := json.NewEncoder(buffer)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(repaired); err == nil {
			return strings.TrimSpace(buffer.String())
		}
	}
	return input
}

type repairResult struct {
	newValue    interface{}
	extraFields map[string]interface{}
}

func repairValue(v interface{}) (interface{}, bool) {
	changed := false
	switch val := v.(type) {
	case map[string]interface{}:
		newMap := make(map[string]interface{})
		for k, v2 := range val {
			newVal, c := repairValue(v2)
			if c {
				changed = true
			}
			if rr, ok := newVal.(repairResult); ok {
				newMap[k] = rr.newValue
				for ek, ev := range rr.extraFields {
					newMap[ek] = ev
				}
				changed = true
			} else {
				newMap[k] = newVal
			}
		}
		return newMap, changed
	case []interface{}:
		newArr := make([]interface{}, len(val))
		for i, elem := range val {
			newElem, c := repairValue(elem)
			if c {
				changed = true
			}
			newArr[i] = newElem
		}
		return newArr, changed
	case string:
		trimmed := strings.TrimSpace(val)
		if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") {
			d := json.NewDecoder(strings.NewReader(val))
			var first interface{}
			if err := d.Decode(&first); err != nil {
				return val, false
			}
			offset := d.InputOffset()
			rest := strings.TrimSpace(val[offset:])
			if rest == "" {
				// Entire string is valid JSON; use the decoded value.
				return first, true
			}
			if strings.HasPrefix(rest, ",") {
				// Remove the comma and try to parse the extra part as an object to merge.
				rest = strings.TrimSpace(rest[1:])
				wrapped := "{" + rest + "}"
				var extra map[string]interface{}
				if err := json.Unmarshal([]byte(wrapped), &extra); err != nil {
					return val, false
				}
				return repairResult{newValue: first, extraFields: extra}, true
			}
		}
		return val, false
	default:
		return v, false
	}
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
