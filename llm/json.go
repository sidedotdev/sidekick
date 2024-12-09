package llm

import "strings"

func RepairJson(input string) string {
	return escapeNewLinesInJSON(input)
	// TODO repair rare case where nested json array/object is instead a json string itself, eg. {"key": "[{\"key\": \"value\"}]"}
}

// escapeNewLinesInJSON tries to repair JSON that has unescaped newlines by escaping them.
// It is robust against valid JSON escapes like `\"` and will only escape newlines inside strings.
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
