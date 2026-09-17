package scripting

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/iancoleman/strcase"
)

// Snapshot detaches configuration data without invoking Go methods. Struct
// fields use JSON or TOML names in camelCase; dictionary keys remain unchanged.
// Callers must supply only non-secret context, never resolved credentials.
func Snapshot(value any) (any, error) {
	return snapshot(reflect.ValueOf(value), 0)
}

func snapshot(v reflect.Value, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("script data is cyclic or too deeply nested")
	}
	if !v.IsValid() {
		return nil, nil
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return nil, nil
		}
		return snapshot(v.Elem(), depth+1)
	case reflect.Bool:
		return v.Bool(), nil
	case reflect.String:
		return v.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint(), nil
	case reflect.Float32, reflect.Float64:
		return v.Float(), nil
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil, nil
		}
		items := make([]any, v.Len())
		for i := range items {
			item, err := snapshot(v.Index(i), depth+1)
			if err != nil {
				return nil, err
			}
			items[i] = item
		}
		return items, nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return nil, errors.New("script data requires string dictionary keys")
		}
		if v.IsNil() {
			return nil, nil
		}
		items := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			item, err := snapshot(iter.Value(), depth+1)
			if err != nil {
				return nil, err
			}
			items[iter.Key().String()] = item
		}
		return items, nil
	case reflect.Struct:
		items := make(map[string]any)
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			tag, ok := field.Tag.Lookup("json")
			if !ok {
				tag = field.Tag.Get("toml")
			}
			name := strings.Split(tag, ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			name = strcase.ToLowerCamel(name)
			if _, exists := items[name]; exists {
				return nil, errors.New("script data has conflicting field names")
			}
			item, err := snapshot(v.Field(i), depth+1)
			if err != nil {
				return nil, err
			}
			items[name] = item
		}
		return items, nil
	default:
		return nil, errors.New("script data contains an unsupported Go value")
	}
}

func encodeData(value any) ([]byte, error) {
	data, err := Snapshot(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(data)
}
