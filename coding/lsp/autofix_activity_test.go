package lsp

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/coding/check"
	"sidekick/env"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAutofixActivity(t *testing.T) {
	// Create a mock LSP client.
	mockLSPClient := MockLSPClient{
		InitializeFunc: func(ctx context.Context, params InitializeParams) (InitializeResponse, error) {
			return InitializeResponse{}, nil
		},
		TextDocumentCodeActionFunc: func(ctx context.Context, params CodeActionParams) ([]CodeAction, error) {
			return []CodeAction{
				{
					Title: "Mock Code Action",
					Edit:  &WorkspaceEdit{},
				},
			}, nil
		},
	}

	lspa := NewLSPActivities(func(language string) LSPClient {
		return mockLSPClient
	})

	// Test case: success case.
	// Create a temporary file for this test
	dir := t.TempDir()
	tmpFile, err := os.CreateTemp(dir, "test")
	if err != nil {
		t.Fatal("Cannot create temporary file", err)
	}
	defer os.Remove(tmpFile.Name())

	// Pass the temporary file's name as the uri with file://
	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{
		RepoDir: dir,
	})
	assert.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}
	uri := "file://" + tmpFile.Name()
	result, err := lspa.AutofixActivity(context.Background(), AutofixActivityInput{envContainer, uri})
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result.SkippedCodeActions))
	assert.Equal(t, 0, len(result.AppliedEdits))

	// Test case: success case where the TextDocumentCodeActionFunc returns a code action with a valid edit.
	lspa.InitializedClients = nil // reset mock clients
	mockLSPClient.TextDocumentCodeActionFunc = func(ctx context.Context, params CodeActionParams) ([]CodeAction, error) {
		kind := CodeActionKindSourceOrganizeImports
		return []CodeAction{
			{
				Title: "Mock Code Action",
				Kind:  &kind,
				Edit: &WorkspaceEdit{
					DocumentChanges: []TextDocumentEdit{
						{
							TextDocument: OptionalVersionedTextDocumentIdentifier{
								TextDocumentIdentifier: TextDocumentIdentifier{
									DocumentURI: uri,
								},
							},
							Edits: []TextEdit{
								{
									Range: Range{
										Start: Position{Line: 0, Character: 0},
										End:   Position{Line: 0, Character: 0},
									},
									NewText: "edited",
								},
								{
									Range: Range{
										Start: Position{Line: 0, Character: 0},
										End:   Position{Line: 0, Character: 0},
									},
									NewText: "twice ",
								},
							},
						},
					},
				},
			},
		}, nil
	}
	result, err = lspa.AutofixActivity(context.Background(), AutofixActivityInput{envContainer, uri})
	assert.NoError(t, err)

	// Read the content of the file to confirm the edit
	content, err := os.ReadFile(tmpFile.Name())
	assert.Equal(t, 0, len(result.SkippedCodeActions))
	assert.Equal(t, 0, len(result.FailedEdits))
	assert.Equal(t, 1, len(result.AppliedEdits))
	assert.NoError(t, err)
	assert.Contains(t, string(content), "twice edited")

	// Test case: failure case where reading the file fails.
	uri = "file://does/not/exist"
	result, err = lspa.AutofixActivity(context.Background(), AutofixActivityInput{envContainer, uri})
	assert.Error(t, err)
	assert.Equal(t, 0, len(result.SkippedCodeActions))
	assert.Equal(t, 0, len(result.FailedEdits))
	assert.Equal(t, 0, len(result.AppliedEdits))

	// Test case: failure case where applying the workspace edit fails.
	lspa.InitializedClients = nil // reset mock clients
	mockLSPClient.TextDocumentCodeActionFunc = func(ctx context.Context, params CodeActionParams) ([]CodeAction, error) {
		kind := CodeActionKindSourceOrganizeImports
		return []CodeAction{
			{
				Title: "Mock Code Action",
				Kind:  &kind,
				Edit: &WorkspaceEdit{
					DocumentChanges: []TextDocumentEdit{
						{
							TextDocument: OptionalVersionedTextDocumentIdentifier{
								TextDocumentIdentifier: TextDocumentIdentifier{
									DocumentURI: uri,
								},
							},
							Edits: []TextEdit{
								{
									Range: Range{
										Start: Position{Line: 0, Character: 0},
										End:   Position{Line: 1000, Character: 1000},
									},
									NewText: "edited",
								},
							},
						},
					},
				},
			},
		}, nil
	}
	uri = "file://" + tmpFile.Name()
	result, err = lspa.AutofixActivity(context.Background(), AutofixActivityInput{envContainer, uri})
	assert.NoError(t, err)
	assert.Equal(t, 0, len(result.SkippedCodeActions))
	assert.Equal(t, 1, len(result.FailedEdits))
	assert.Equal(t, 0, len(result.AppliedEdits))
}

func golangIntegrationTest(t *testing.T, code string, expectedCode string) {
	lspa := &LSPActivities{
		LSPClientProvider: func(language string) LSPClient {
			return &Jsonrpc2LSPClient{
				LanguageName: "golang",
			}
		},
	}

	// Create a temporary file for this test
	dir := t.TempDir()
	tmpFile, err := os.CreateTemp(dir, "test*.go")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	// Write a bad Go file to the temporary file, missing the import "context"
	tmpFile.WriteString(code)

	// Pass the temporary file's name as the uri with file://
	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{
		RepoDir: dir,
	})
	assert.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}
	uri := "file://" + tmpFile.Name()
	result, err := lspa.AutofixActivity(context.Background(), AutofixActivityInput{envContainer, uri})
	assert.NoError(t, err)
	assert.Equal(t, []string{}, utils.Map(result.FailedEdits, func(fe FailedEdit) string { return fe.Error }))
	assert.Equal(t, 0, len(result.SkippedCodeActions))
	assert.Equal(t, 1, len(result.AppliedEdits))

	// Read the content of the file to confirm the autofix added the missing import
	content, err := os.ReadFile(tmpFile.Name())
	assert.NoError(t, err)
	assert.Equal(t, expectedCode, string(content))
	passed, errorString, err := check.CheckFileValidity(envContainer, filepath.Base(tmpFile.Name()))
	assert.NoError(t, err)
	if !passed {
		t.Errorf("Go file failed to pass check. Errors:\n%s", errorString)
	}
}

func vueIntegrationTest(t *testing.T, code string, expectedCode string) {
	lspa := &LSPActivities{
		LSPClientProvider: func(language string) LSPClient {
			return &Jsonrpc2LSPClient{
				LanguageName: "golang",
			}
		},
	}

	// Create a temporary file for this test
	dir := t.TempDir()
	tmpFile, err := os.CreateTemp(dir, "test*.go")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	// Write a bad Go file to the temporary file, missing the import "context"
	tmpFile.WriteString(code)

	// Pass the temporary file's name as the uri with file://
	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{
		RepoDir: dir,
	})
	assert.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}
	uri := "file://" + tmpFile.Name()
	result, err := lspa.AutofixActivity(context.Background(), AutofixActivityInput{envContainer, uri})
	assert.NoError(t, err)
	assert.Equal(t, result, AutofixActivityOutput{}) // nothing happens yet Vue files due to there being no LSP client for Vue
}

// this is an integration test with non-mock LSP client for golang
func TestAutofixActivity_integrationGolang_missingImport(t *testing.T) {
	code := `package bad
	
import (
	"context"
)

func bad(ctx context.Context) {
	fmt.Println("hi")
}
`
	expectedCode := `package bad

import (
	"context"
	"fmt"
)

func bad(ctx context.Context) {
	fmt.Println("hi")
}
`
	golangIntegrationTest(t, code, expectedCode)
}

func TestAutofixActivity_integrationGolang_extraImportStatements(t *testing.T) {
	code := `package bad

import (
	"context"
)

import (
	"context"
	"fmt"
)

import (
	"fmt"
)

func bad(ctx context.Context) {
	fmt.Println("hi")
}
`
	expectedCode := `package bad

import (
	"context"
	"fmt"
)

func bad(ctx context.Context) {
	fmt.Println("hi")
}
`
	golangIntegrationTest(t, code, expectedCode)
}
