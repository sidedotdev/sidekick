package dev

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/cbroglie/mustache"
)

func init() {
	mustache.AllowMissingVariables = false
}

func RenderPrompt(template *mustache.Template, data interface{}) string {
	result, err := template.Render(data)
	if err != nil {
		panic(err)
	}
	return result
}

// fsPartialProvider is a PartialProvider that reads partials from a file system implementing fs.ReadFileFS.
type fsPartialProvider struct {
	fs     fs.ReadFileFS
	prefix string
}

func (epp *fsPartialProvider) Get(name string) (string, error) {
	templatePath := fmt.Sprintf("prompts/%s%s.mustache", epp.prefix, name)
	templateBytes, err := epp.fs.ReadFile(templatePath)
	if err != nil {
		return "", err
	}
	return string(templateBytes), nil
}

func panicParseMustache(fileSystem fs.ReadFileFS, templateName string) *mustache.Template {
	templatePath := fmt.Sprintf("prompts/%s.mustache", templateName)
	templateBytes, err := fileSystem.ReadFile(templatePath)
	if err != nil {
		panic(err)
	}

	prefix := templateName[:strings.LastIndex(templateName, "/")+1]
	partialProvider := &fsPartialProvider{fs: fileSystem, prefix: prefix}
	template, err := mustache.ParseStringPartials(string(templateBytes), partialProvider)
	if err != nil {
		panic(err)
	}
	return template
}

//go:embed prompts/*
var promptsFS embed.FS

var GeneralFeedback = panicParseMustache(promptsFS, "general_feedback")
var AuthorEditBlockFeedback = panicParseMustache(promptsFS, "author_edit_block/feedback")
var RecordPlanInitial = panicParseMustache(promptsFS, "record_plan/initial")
var AuthorEditBlockInitial = panicParseMustache(promptsFS, "author_edit_block/initial")
var AuthorEditBlockInitialWithPlan = panicParseMustache(promptsFS, "author_edit_block/initial_with_plan")
var CodeContextInitial = panicParseMustache(promptsFS, "code_context/initial")
var CodeContextFeedback = panicParseMustache(promptsFS, "code_context/feedback")
var CodeContextRefineAndRank = panicParseMustache(promptsFS, "code_context/refine_and_rank")
