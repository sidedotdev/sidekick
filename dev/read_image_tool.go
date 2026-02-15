package dev

import (
	"sidekick/llm"

	"github.com/invopop/jsonschema"
)

type ReadImageParams struct {
	FilePath string `json:"file_path" jsonschema:"description=The path to the image file relative to the current working directory. Absolute paths and '..' traversal are not allowed."`
}

var readImageTool = llm.Tool{
	Name:        "read_image",
	Description: "Reads an image file and adds it to the conversation history so the model can see it. The file path must be relative to the current working directory; absolute paths and '..' segments are disallowed.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&ReadImageParams{}),
}
