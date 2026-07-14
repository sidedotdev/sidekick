package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONSchemaWithRequiredNullableOptionals(t *testing.T) {
	t.Parallel()

	schema, err := JSONSchemaWithRequiredNullableOptionals(map[string]any{
		"type":     "object",
		"required": []string{"required_field"},
		"properties": map[string]any{
			"required_field": map[string]any{"type": "string"},
			"optional_field": map[string]any{"type": "string"},
			"nested": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"optional_nested_field": map[string]any{"type": "integer"},
				},
			},
		},
	})
	require.NoError(t, err)

	assert.ElementsMatch(t, []any{"required_field", "optional_field", "nested"}, schema["required"])

	properties := schema["properties"].(map[string]any)
	assert.Equal(t, map[string]any{"type": "string"}, properties["required_field"])

	optionalField := properties["optional_field"].(map[string]any)
	assert.Equal(t, []any{
		map[string]any{"type": "string"},
		map[string]any{"type": "null"},
	}, optionalField["anyOf"])

	nested := properties["nested"].(map[string]any)
	nestedAnyOf := nested["anyOf"].([]any)
	nestedSchema := nestedAnyOf[0].(map[string]any)
	assert.Equal(t, []any{"optional_nested_field"}, nestedSchema["required"])

	nestedProperties := nestedSchema["properties"].(map[string]any)
	optionalNestedField := nestedProperties["optional_nested_field"].(map[string]any)
	assert.Equal(t, []any{
		map[string]any{"type": "integer"},
		map[string]any{"type": "null"},
	}, optionalNestedField["anyOf"])
}
