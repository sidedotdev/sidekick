package lsp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"sidekick/env"
	"sidekick/utils"
)

type FailedEdit struct {
	Edit  WorkspaceEdit `json:"edit"`
	Error string        `json:"error"`
}

type AutofixActivityInput struct {
	EnvContainer env.EnvContainer
	DocumentURI  string
}

type AutofixActivityOutput struct {
	SkippedCodeActions []CodeAction    `json:"skippedCodeActions"`
	AppliedEdits       []WorkspaceEdit `json:"appliedEdits"`
	FailedEdits        []FailedEdit    `json:"failedEdits"`
}

func (lspa *LSPActivities) AutofixActivity(ctx context.Context, input AutofixActivityInput) (AutofixActivityOutput, error) {
	// step 1: initialize
	langName := utils.InferLanguageNameFromFilePath(input.DocumentURI)
	lspClient, err := lspa.findOrInitClient(ctx, input.EnvContainer.Env.GetWorkingDirectory(), langName)
	if err != nil {
		if errors.Is(err, ErrUnsupportedLanguage) {
			err = nil // unsupported languages just means we can't autofix, not that autofix failed
		}
		return AutofixActivityOutput{}, err
	}

	// step 2: get code actions
	codeActions, err := getAutofixCodeActions(ctx, lspClient, input.DocumentURI)
	if err != nil {
		return AutofixActivityOutput{}, err
	}

	// step 3: apply each code action's workspace edits
	output, err := applyCodeActions(ctx, input.EnvContainer, codeActions)
	if err != nil {
		return AutofixActivityOutput{}, err
	}

	return output, nil
}

func getAutofixCodeActions(ctx context.Context, lspClient LSPClient, documentURI string) ([]CodeAction, error) {
	// Calculate end line and character based on file contents
	fileContent, err := readURI(documentURI)
	if err != nil {
		return []CodeAction{}, fmt.Errorf("failed to read file: %w", err)
	}
	lines := strings.Split(string(fileContent), "\n")
	endLine := len(lines) - 1
	endCharacter := len(lines[endLine])
	return lspClient.TextDocumentCodeAction(ctx, CodeActionParams{
		TextDocument: TextDocumentIdentifier{
			URI: documentURI,
		},
		Range: Range{
			Start: Position{
				Line:      0,
				Character: 0,
			},
			End: Position{
				Line:      endLine,
				Character: endCharacter,
			},
		},
		Context: CodeActionContext{
			Only: []CodeActionKind{CodeActionKindSourceFixAll, CodeActionKindSourceOrganizeImports},
		},
	})
}

func applyCodeActions(ctx context.Context, envContainer env.EnvContainer, codeActions []CodeAction) (AutofixActivityOutput, error) {
	output := AutofixActivityOutput{}

	for _, codeAction := range codeActions {
		if codeAction.Kind == nil || codeAction.Edit == nil {
			output.SkippedCodeActions = append(output.SkippedCodeActions, codeAction)
			continue
		}
		if *codeAction.Kind == CodeActionKindSourceFixAll || *codeAction.Kind == CodeActionKindSourceOrganizeImports {
			err := ApplyWorkspaceEdit(ctx, envContainer, *codeAction.Edit)
			if err != nil {
				output.FailedEdits = append(output.FailedEdits, FailedEdit{
					Edit:  *codeAction.Edit,
					Error: err.Error(),
				})
			} else {
				output.AppliedEdits = append(output.AppliedEdits, *codeAction.Edit)
			}
		}
	}

	return output, nil
}

var defaultClientCapabilities = ClientCapabilities{
	TextDocument: TextDocumentClientCapabilities{
		Synchronization: &TextDocumentSyncClientCapabilities{
			DidSave: &[]bool{true}[0],
		},
		CodeAction: CodeActionClientCapabilities{
			CodeActionLiteralSupport: &CodeActionLiteralSupport{
				CodeActionKind: &CodeActionKindValueSet{
					ValueSet: []CodeActionKind{
						CodeActionKindSourceFixAll,
						CodeActionKindSourceOrganizeImports,
					},
				},
			},
		},
	},
}
