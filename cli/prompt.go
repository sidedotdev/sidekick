package main

import (
	"github.com/charmbracelet/huh"
)

// runPrompt runs a single field as a form, rather than via Field.Run, so that
// every prompt is rendered the same way, help footer included.
func runPrompt(field huh.Field) error {
	return huh.NewForm(huh.NewGroup(field)).Run()
}

// selectOption prompts for a single choice among the given options.
func selectOption(title string, options []string) (string, error) {
	var selected string
	err := runPrompt(huh.NewSelect[string]().
		Title(title).
		Options(huh.NewOptions(options...)...).
		Value(&selected))
	if err != nil {
		return "", err
	}
	return selected, nil
}
