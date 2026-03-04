package utils

import (
	"bytes"
	"encoding/json"
)

func PrettyJSON(i interface{}) string {
	s, err := json.MarshalIndent(i, "", "\t")
	if err != nil {
		panic(err)
	}
	return string(s)
}

func PanicJSON(i interface{}) string {
	s, err := json.Marshal(i)
	if err != nil {
		panic(err)
	}
	return string(s)
}

func PanicParseMapJSON(s string) map[string]any {
	var i map[string]any
	err := json.Unmarshal([]byte(s), &i)
	if err != nil {
		panic(err)
	}
	return i
}

func PrettyPrint(i interface{}) {
	println(PrettyJSON(i))
}

func Transcode(in, out interface{}) {
	buf := new(bytes.Buffer)
	json.NewEncoder(buf).Encode(in)
	json.NewDecoder(buf).Decode(out)
}

// StructToMap marshals a value to JSON and unmarshals it into a map,
// so that json tags (including omitempty) are respected.
func StructToMap(v interface{}) (map[string]any, error) {
	if v == nil {
		return map[string]any{}, nil
	}

	jsonBytes, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil, err
	}

	return result, nil
}
