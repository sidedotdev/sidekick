package common

import "encoding/json"

// JSONSchemaWithRequiredNullableOptionals converts optional object properties
// into required nullable properties for OpenAI-compatible function schemas.
func JSONSchemaWithRequiredNullableOptionals(schema any) (map[string]any, error) {
	if schema == nil {
		return map[string]any{}, nil
	}

	jsonBytes, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil, err
	}

	makeOptionalPropertiesRequiredAndNullable(result)
	return result, nil
}

func makeOptionalPropertiesRequiredAndNullable(schema map[string]any) {
	properties, hasProperties := schema["properties"].(map[string]any)
	if hasProperties {
		required, _ := schema["required"].([]any)
		requiredNames := make(map[string]struct{}, len(required))
		for _, name := range required {
			if name, ok := name.(string); ok {
				requiredNames[name] = struct{}{}
			}
		}

		for name, property := range properties {
			if propertySchema, ok := property.(map[string]any); ok {
				makeOptionalPropertiesRequiredAndNullable(propertySchema)
			}

			if _, isRequired := requiredNames[name]; isRequired {
				continue
			}

			properties[name] = map[string]any{
				"anyOf": []any{
					property,
					map[string]any{"type": "null"},
				},
			}
			required = append(required, name)
			requiredNames[name] = struct{}{}
		}

		schema["required"] = required
	}

	for _, value := range schema {
		makeOptionalPropertiesRequiredAndNullableInValue(value)
	}
}

func makeOptionalPropertiesRequiredAndNullableInValue(value any) {
	switch value := value.(type) {
	case map[string]any:
		makeOptionalPropertiesRequiredAndNullable(value)
	case []any:
		for _, item := range value {
			makeOptionalPropertiesRequiredAndNullableInValue(item)
		}
	}
}
