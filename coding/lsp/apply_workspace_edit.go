package lsp

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sidekick/env"
	"sort"
	"strings"
)

// applyTextEdit applies a single TextEdit to a string representing the file contents.
// It takes the original contents and a TextEdit as input, and returns the modified contents.
func applyTextEdit(originalContents string, edit TextEdit) (string, error) {
	// Parse the range from the TextDocumentEdit
	start := edit.Range.Start
	end := edit.Range.End

	// Check if the range is valid (contains the start and end positions, both line and character)
	// Detect line endings
	lineEnding := "\n"
	if strings.Contains(originalContents, "\r\n") {
		lineEnding = "\r\n"
	}

	// Split lines based on detected line ending
	lines := strings.Split(originalContents, lineEnding)
	if start.Line < 0 || start.Line >= len(lines) || end.Line < 0 || end.Line >= len(lines) {
		return "", fmt.Errorf("invalid range: line out of bounds")
	}
	if start.Character < 0 || start.Character > len(lines[start.Line]) || end.Character < 0 || end.Character > len(lines[end.Line]) {
		return "", fmt.Errorf("invalid range: character out of bounds")
	}

	// Replace the specified range with the new text
	startIndex := len(strings.Join(lines[:start.Line], lineEnding)) + start.Character
	endIndex := len(strings.Join(lines[:end.Line], lineEnding)) + end.Character

	// as soon as you join a list of 1 line, you are missing the initial line ending,
	// but this doesn't apply to the first line
	if start.Line > 0 {
		startIndex += len(lineEnding)
	}
	if end.Line > 0 {
		endIndex += len(lineEnding)
	}

	originalContents = originalContents[:startIndex] + edit.NewText + originalContents[endIndex:]

	// Return the modified contents
	return originalContents, nil
}

func applyTextDocumentEdit(originalContents string, textDocumentEdit TextDocumentEdit) (string, error) {
	// Order edits in reverse order by start line and character, this way we can
	// apply them in order without messing up the ranges on the original
	// contents
	sort.Slice(textDocumentEdit.Edits, func(i, j int) bool {
		iEdit := textDocumentEdit.Edits[i]
		jEdit := textDocumentEdit.Edits[j]
		return iEdit.Range.Start.Line > jEdit.Range.Start.Line ||
			(iEdit.Range.Start.Line == jEdit.Range.Start.Line &&
				iEdit.Range.Start.Character > jEdit.Range.Start.Character)
	})

	// initial value of updatedContents is the original contents
	updatedContents := originalContents

	// utils.PrettyPrint(textDocumentEdit.Edits) // debug
	var prevEdit *TextEdit
	// Loop over the edits in the TextDocumentEdit
	for _, textEdit := range textDocumentEdit.Edits {
		// If the start of the current edit is less than the end of the previous edit, return an error
		if prevEdit != nil && (textEdit.Range.End.Line > prevEdit.Range.Start.Line ||
			(textEdit.Range.End.Line == prevEdit.Range.Start.Line && textEdit.Range.End.Character > prevEdit.Range.Start.Character)) {
			return "", fmt.Errorf("overlapping edits: %v and %v", prevEdit, textEdit)
		}
		// Apply the edit and overwrite the updatedContents
		var err error
		updatedContents, err = applyTextEdit(updatedContents, textEdit)
		if err != nil {
			return "", err
		}
		tempEdit := textEdit // copy
		prevEdit = &tempEdit
	}

	return updatedContents, nil
}

// readURI parses the given URI and returns the corresponding file path.
// Returns an error if the URI is not a valid file URI.
func readURI(documentURI string) (string, error) {
	if !strings.HasPrefix(documentURI, "file://") {
		return "", fmt.Errorf("invalid file URI: %s", documentURI)
	}
	u, err := url.Parse(documentURI)
	if err != nil {
		return "", err
	}
	contents, err := os.ReadFile(u.Path)
	if err != nil {
		return "", err
	}
	return string(contents), nil
}

func writeURI(documentURI, updatedContents string) error {
	if !strings.HasPrefix(documentURI, "file://") {
		return fmt.Errorf("invalid file URI: %s", documentURI)
	}
	u, err := url.Parse(documentURI)
	if err != nil {
		return err
	}
	err = os.WriteFile(u.Path, []byte(updatedContents), 0644)
	if err != nil {
		return err
	}
	return nil
}

func ApplyWorkspaceEdit(ctx context.Context, envContainer env.EnvContainer, workspaceEdit WorkspaceEdit) error {
	for _, documentEdit := range workspaceEdit.DocumentChanges {
		originalContents, err := readURI(documentEdit.TextDocument.TextDocumentIdentifier.DocumentURI)
		if err != nil {
			return err
		}

		updatedContents, err := applyTextDocumentEdit(originalContents, documentEdit)
		if err != nil {
			return err
		}

		// Write the updated contents back to the file
		err = writeURI(documentEdit.TextDocument.TextDocumentIdentifier.DocumentURI, updatedContents)
		if err != nil {
			return err
		}
	}
	return nil
}

//type ApplyWorkspaceEditParams struct {
//	WorkspaceEdit WorkspaceEdit
//	EnvContainer  env.EnvContainer
//}
