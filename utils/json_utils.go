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
