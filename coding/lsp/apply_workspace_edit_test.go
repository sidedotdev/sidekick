package lsp

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"sidekick/env"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyTextEdit(t *testing.T) {
	tests := []struct {
		name             string
		originalContents string
		edit             TextEdit
		expectedContents string
		expectErr        bool
	}{
		{
			name:             "ReplaceBeginning",
			originalContents: "Hello, world!",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 0},
					End:   Position{Line: 0, Character: 5},
				},
				NewText: "Hi",
			},
			expectedContents: "Hi, world!",
			expectErr:        false,
		},
		{
			name:             "ReplaceEnd",
			originalContents: "Hello, World!",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 7},
					End:   Position{Line: 0, Character: 13},
				},
				NewText: "Universe?",
			},
			expectedContents: "Hello, Universe?",
			expectErr:        false,
		},
		{
			name:             "ReplaceMultiline",
			originalContents: "Hello,\nworld!",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 5},
					End:   Position{Line: 1, Character: 6},
				},
				NewText: ";\nUniverse?",
			},
			expectedContents: "Hello;\nUniverse?",
			expectErr:        false,
		},
		{
			name:             "ReplaceWholeLine",
			originalContents: "Hello, world!",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 0},
					End:   Position{Line: 0, Character: 13},
				},
				NewText: "Hi, Universe?",
			},
			expectedContents: "Hi, Universe?",
			expectErr:        false,
		},
		{
			name:             "InsertAtBeginning",
			originalContents: "Hello, world!",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 0},
					End:   Position{Line: 0, Character: 0},
				},
				NewText: "Hi, ",
			},
			expectedContents: "Hi, Hello, world!",
			expectErr:        false,
		},
		{
			name:             "InsertMiddle",
			originalContents: "\t" + `"context"`,
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 8},
					End:   Position{Line: 0, Character: 8},
				},
				NewText: `t"` + "\n\t" + `"fm`,
			},
			expectedContents: "\t" + `"context"` + "\n\t" + `"fmt"`,
			expectErr:        false,
		},
		{
			name:             "InsertAtEnd",
			originalContents: "Hello, world!",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 13},
					End:   Position{Line: 0, Character: 13},
				},
				NewText: " How are you?",
			},
			expectedContents: "Hello, world! How are you?",
			expectErr:        false,
		},
		{
			name:             "InsertAtBeginningAndEnd",
			originalContents: "Hello, world!",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 0},
					End:   Position{Line: 0, Character: 0},
				},
				NewText: "Hi, ",
			},
			expectedContents: "Hi, Hello, world!",
			expectErr:        false,
		},
		{
			name:             "EmptyOriginalContents",
			originalContents: "",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 0},
					End:   Position{Line: 0, Character: 0},
				},
				NewText: "Hello, World!",
			},
			expectedContents: "Hello, World!",
			expectErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modifiedContents, err := applyTextEdit(tt.originalContents, tt.edit)
			if (err != nil) != tt.expectErr {
				t.Errorf("applyTextEdit() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if modifiedContents != tt.expectedContents {
				t.Errorf("applyTextEdit() = %v, want %v", modifiedContents, tt.expectedContents)
			}
		})
	}
}

func TestApplyTextDocumentEdit_NoEdits(t *testing.T) {
	// Create a TextDocumentEdit with no edits
	textDocumentEdit := TextDocumentEdit{
		Edits: []TextEdit{},
	}

	// Call applyTextDocumentEdit with some originalContents
	originalContents := "original contents"
	updatedContents, err := applyTextDocumentEdit(originalContents, textDocumentEdit)

	// Assert that no error is returned
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Assert that the returned string is the same as the originalContents
	if updatedContents != originalContents {
		t.Fatalf("expected %s, got %s", originalContents, updatedContents)
	}
}

func TestApplyTextDocumentEdit_MultipleEdits(t *testing.T) {
	// Create a TextDocumentEdit with multiple edits
	textDocumentEdit := TextDocumentEdit{
		Edits: []TextEdit{
			{
				Range: Range{
					Start: Position{Line: 0, Character: 0},
					End:   Position{Line: 0, Character: 6},
				},
				NewText: "Hi,",
			},
			{
				Range: Range{
					Start: Position{Line: 0, Character: 7},
					End:   Position{Line: 0, Character: 13},
				},
				NewText: "Universe?",
			},
		},
	}

	// Call applyTextDocumentEdit with some originalContents
	originalContents := "Hello, World!"
	updatedContents, err := applyTextDocumentEdit(originalContents, textDocumentEdit)

	// Assert that no error is returned
	if err != nil {
		t.Fatalf("expected no error, but got: %v", err)
	}

	// Assert that the returned string is as expected
	expectedContents := "Hi, Universe?"
	if updatedContents != expectedContents {
		t.Fatalf("expected '%s', but got '%s'", expectedContents, updatedContents)
	}
}

func TestApplyTextDocumentEdit_OverlappingEdits(t *testing.T) {
	originalContents := "Hello, world!"

	// Create a TextDocumentEdit with overlapping edits
	textDocumentEdit := TextDocumentEdit{
		Edits: []TextEdit{
			{
				NewText: "Hi",
				Range: Range{
					Start: Position{Line: 0, Character: 0},
					End:   Position{Line: 0, Character: 5},
				},
			},
			{
				NewText: "everyone",
				Range: Range{
					Start: Position{Line: 0, Character: 4},
					End:   Position{Line: 0, Character: 11},
				},
			},
		},
	}

	// Call applyTextDocumentEdit with the original contents and the TextDocumentEdit
	_, err := applyTextDocumentEdit(originalContents, textDocumentEdit)

	// Assert that an error is returned
	if err == nil {
		t.Errorf("Expected an error to be returned due to overlapping edits, but got nil")
	}
}

func TestApplyWorkspaceEdit(t *testing.T) {
	const originalContents = `Line 1
Line 2
Line 3
Line 4
Line 5`

	tempFile, err := os.CreateTemp("", "mock_file.go")
	assert.Nil(t, err)
	tempFile.Write([]byte(originalContents))
	documentURI := "file://" + tempFile.Name()

	// Create a mock WorkspaceEdit with valid edits
	workspaceEdit := WorkspaceEdit{
		DocumentChanges: []TextDocumentEdit{
			{
				TextDocument: OptionalVersionedTextDocumentIdentifier{
					TextDocumentIdentifier: TextDocumentIdentifier{
						URI: documentURI,
					},
				},
				Edits: []TextEdit{
					{
						Range: Range{
							Start: Position{Line: 0, Character: 0},
							End:   Position{Line: 0, Character: 6},
						},
						NewText: "New content",
					},
				},
			},
		},
	}

	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{
		RepoDir: "TODO tempdir",
	})
	assert.Nil(t, err)
	envContainer := env.EnvContainer{
		Env: devEnv,
	}
	// Call ApplyWorkspaceEdit with the mock WorkspaceEdit
	err = ApplyWorkspaceEdit(context.Background(), envContainer, workspaceEdit)
	assert.Nil(t, err)

	// Read the contents of the file
	contents, err := readURI(documentURI)
	assert.Nil(t, err)

	// Assert that the contents of the file have been updated as expected
	expectedContents := "New content" + originalContents[6:]
	if contents != expectedContents {
		t.Errorf("Expected file contents to be '%s', but got '%s'", expectedContents, contents)
	}
}

func TestApplyWorkspaceEditSpaceInPath(t *testing.T) {
	const originalContents = `Line 1
Line 2
Line 3
Line 4
Line 5`

	tempDirWithSpaces := filepath.Join(t.TempDir(), "path with spaces")
	testFilePath := filepath.Join(tempDirWithSpaces, "test file with spaces.go")
	os.MkdirAll(tempDirWithSpaces, 0755)
	err := os.WriteFile(testFilePath, []byte(originalContents), 0644)
	assert.Nil(t, err)

	uri, err := url.Parse("file://" + testFilePath)
	assert.Nil(t, err)
	documentURI := uri.String()

	// TODO write mockFileContents to tempFile
	// Create a mock WorkspaceEdit with valid edits
	workspaceEdit := WorkspaceEdit{
		DocumentChanges: []TextDocumentEdit{
			{
				TextDocument: OptionalVersionedTextDocumentIdentifier{
					TextDocumentIdentifier: TextDocumentIdentifier{
						URI: documentURI,
					},
				},
				Edits: []TextEdit{
					{
						Range: Range{
							Start: Position{Line: 0, Character: 0},
							End:   Position{Line: 0, Character: 6},
						},
						NewText: "New content",
					},
				},
			},
		},
	}

	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{
		RepoDir: "TODO tempdir",
	})
	assert.Nil(t, err)
	envContainer := env.EnvContainer{
		Env: devEnv,
	}
	// Call ApplyWorkspaceEdit with the mock WorkspaceEdit
	err = ApplyWorkspaceEdit(context.Background(), envContainer, workspaceEdit)
	assert.Nil(t, err)

	// Read the contents of the file
	contents, err := readURI(documentURI)
	assert.Nil(t, err)

	// Assert that the contents of the file have been updated as expected
	expectedContents := "New content" + originalContents[6:]
	if contents != expectedContents {
		t.Errorf("Expected file contents to be '%s', but got '%s'", expectedContents, contents)
	}
}

func TestApplyTextEditCRLF(t *testing.T) {
	tests := []struct {
		name             string
		originalContents string
		edit             TextEdit
		expectedContents string
		expectErr        bool
	}{
		{
			name:             "ReplaceBeginningCRLF",
			originalContents: "Hello, world!\r\n",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 0},
					End:   Position{Line: 0, Character: 5},
				},
				NewText: "Hi",
			},
			expectedContents: "Hi, world!\r\n",
			expectErr:        false,
		},
		{
			name:             "ReplaceEndCRLF",
			originalContents: "Hello, World!\r\n",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 7},
					End:   Position{Line: 0, Character: 13},
				},
				NewText: "Universe?",
			},
			expectedContents: "Hello, Universe?\r\n",
			expectErr:        false,
		},
		{
			name:             "ReplaceMultilineCRLF",
			originalContents: "Hello,\r\nworld!\r\n",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 5},
					End:   Position{Line: 1, Character: 6},
				},
				NewText: ";\r\nUniverse?",
			},
			expectedContents: "Hello;\r\nUniverse?\r\n",
			expectErr:        false,
		},
		{
			name:             "ReplaceWholeLineCRLF",
			originalContents: "Hello, world!\r\n",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 0},
					End:   Position{Line: 0, Character: 13},
				},
				NewText: "Hi, Universe?",
			},
			expectedContents: "Hi, Universe?\r\n",
			expectErr:        false,
		},
		{
			name:             "InsertAtBeginningCRLF",
			originalContents: "Hello, world!\r\n",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 0},
					End:   Position{Line: 0, Character: 0},
				},
				NewText: "Hi, ",
			},
			expectedContents: "Hi, Hello, world!\r\n",
			expectErr:        false,
		},
		{
			name:             "InsertMiddleCRLF",
			originalContents: "\t\"context\"\r\n",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 8},
					End:   Position{Line: 0, Character: 8},
				},
				NewText: "t\"\r\n\t\"fm",
			},
			expectedContents: "\t\"context\"\r\n\t\"fmt\"\r\n",
			expectErr:        false,
		},
		{
			name:             "InsertAtEndCRLF",
			originalContents: "Hello, world!\r\n",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 13},
					End:   Position{Line: 0, Character: 13},
				},
				NewText: " How are you?",
			},
			expectedContents: "Hello, world! How are you?\r\n",
			expectErr:        false,
		},
		{
			name:             "InsertAtBeginningAndEndCRLF",
			originalContents: "Hello, world!\r\n",
			edit: TextEdit{
				Range: Range{
					Start: Position{Line: 0, Character: 0},
					End:   Position{Line: 0, Character: 0},
				},
				NewText: "Hi, ",
			},
			expectedContents: "Hi, Hello, world!\r\n",
			expectErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modifiedContents, err := applyTextEdit(tt.originalContents, tt.edit)
			if (err != nil) != tt.expectErr {
				t.Errorf("applyTextEdit() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if modifiedContents != tt.expectedContents {
				t.Errorf("applyTextEdit() = %v, want %v", modifiedContents, tt.expectedContents)
			}
		})
	}
}
