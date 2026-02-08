package lsp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"sidekick/env"
	"sidekick/utils"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/activity"
)

var autofixTracer = otel.Tracer("sidekick/coding/lsp/autofix")

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
	var span trace.Span
	if activity.IsActivity(ctx) {
		// When called via Temporal, use the existing span created by Temporal
		span = trace.SpanFromContext(ctx)
	} else {
		// When called directly, create our own span
		ctx, span = autofixTracer.Start(ctx, "AutofixActivity")
		defer span.End()
	}
	span.SetAttributes(attribute.String("documentURI", input.DocumentURI))

	// step 1: initialize
	langName := utils.InferLanguageNameFromFilePath(input.DocumentURI)
	lspClient, err := lspa.findOrInitClient(ctx, input.EnvContainer.Env.GetWorkingDirectory(), langName)
	if err != nil {
		if errors.Is(err, ErrUnsupportedLanguage) {
			span.SetAttributes(attribute.Bool("skipped", true), attribute.String("reason", "unsupported language"))
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
	ctx, span := autofixTracer.Start(ctx, "getAutofixCodeActions")
	defer span.End()

	// Calculate end line and character based on file contents
	fileContent, err := readURI(documentURI)
	if err != nil {
		return []CodeAction{}, fmt.Errorf("failed to read file: %w", err)
	}
	lines := strings.Split(string(fileContent), "\n")
	endLine := len(lines) - 1
	endCharacter := len(lines[endLine])
	codeActions, err := lspClient.TextDocumentCodeAction(ctx, CodeActionParams{
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
	span.SetAttributes(attribute.Int("codeActionCount", len(codeActions)))
	return codeActions, err
}

func applyCodeActions(ctx context.Context, envContainer env.EnvContainer, codeActions []CodeAction) (AutofixActivityOutput, error) {
	ctx, span := autofixTracer.Start(ctx, "applyCodeActions")
	defer span.End()

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

	span.SetAttributes(
		attribute.Int("appliedEdits", len(output.AppliedEdits)),
		attribute.Int("failedEdits", len(output.FailedEdits)),
		attribute.Int("skippedCodeActions", len(output.SkippedCodeActions)),
	)
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
