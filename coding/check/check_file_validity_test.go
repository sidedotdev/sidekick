package check

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/env"
	"strings"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/stretchr/testify/assert"
)

func TestCheckFileValidity_Golang(t *testing.T) {
	testCases := []struct {
		name           string
		fileContent    string
		wantPass       bool
		expectedErrors []string
	}{
		{
			name: "valid Go file",
			fileContent: `package main

import "fmt"
func main() { fmt.Println("Hello, World!") }`,
			wantPass: true,
		},
		{
			name: "Package imported and not used Go file",
			fileContent: `package main

import "fmt"
func main() {}`,
			wantPass: true, // while an error, it is not a syntax error and should pass this basic check
		},
		{
			name: "Multiple errors: Package imported and not used + multiple import statements",
			fileContent: `package main

import "fmt"
import "fmt"
func main() {}`,
			wantPass: false, // it's not quite a syntax error, but it shows there was a bad edit, which is hard to recover from
			//expectedErrors: []string{"4:8: fmt redeclared in this block", "other declaration of fmt"},
			expectedErrors: []string{"Multiple import statements found"},
		},
		{
			name: "Go file with syntax error",
			fileContent: `package main

func main() {`,
			wantPass:       false,
			expectedErrors: []string{"Syntax error"},
		},
		{
			name: "Valid Go file with multiple imports",
			fileContent: `package main

import (
	"fmt"
	"os"
)

func main() { fmt.Println(os.Args) }`,
			wantPass: true,
		},
		{
			name: "Invalid Go file with late additional import statements",
			fileContent: `package main

import "fmt"
func main() { fmt.Println(os.Args) }
import "os"`,
			wantPass: false,
			//expectedErrors: []string{"imports must appear before other declarations"},
			expectedErrors: []string{"Multiple import statements found"},
		},
		{
			name:           "empty file",
			fileContent:    "",
			wantPass:       false,
			expectedErrors: []string{"File is blank"},
		},
		{
			name:           "blank file",
			fileContent:    "\t\n\t \r\n",
			wantPass:       false,
			expectedErrors: []string{"File is blank"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dir, filename, err := writeTempFile(t, "go", tc.fileContent)
			if err != nil {
				t.Fatalf("Failed to write temp file: %v", err)
			}
			defer os.Remove(filepath.Join(dir, filename))

			envContainer := env.EnvContainer{
				Env: &env.LocalEnv{
					WorkingDirectory: dir,
				},
			}
			passed, errorString, err := CheckFileValidity(envContainer, filename)
			assert.NoError(t, err)
			// fmt.Println(errorString) // debug
			if passed != tc.wantPass {
				t.Errorf("Want check pass = %v, got: %v", tc.wantPass, passed)
			}
			if !tc.wantPass {
				for _, expectedError := range tc.expectedErrors {
					if !strings.Contains(errorString, expectedError) {
						t.Errorf("Expected error string to contain '%s', but it was '%s'", expectedError, errorString)
					}
				}
			}
			if tc.wantPass && errorString != "" {
				t.Errorf("Didn't expect error: %s", errorString)
			}
		})
	}
}

func TestCheckFileValidity_Vue(t *testing.T) {
	testCases := []struct {
		name           string
		fileContent    string
		wantPass       bool
		expectedErrors []string
	}{
		{
			name:        "Vue file with valid embedded TS",
			fileContent: `<template><div>Hello World</div></template><script lang="ts">let message: string = "Hello"; console.log(message);</script>`,
			wantPass:    true,
		},
		{
			name:           "Vue file with invalid embedded TS",
			fileContent:    `<template><div>Hello World</div></template><script lang="ts">let message: string = "Hello"; console.log(message;</script>`,
			wantPass:       false,
			expectedErrors: []string{"Syntax error"},
		},
		{
			name:        "Vue file with valid embedded JS",
			fileContent: `<template><div>Hello World</div></template><script>let message = "Hello"; console.log(message);</script>`,
			wantPass:    true,
		},
		{
			name:        "Vue file without script tag",
			fileContent: `<template><div>Hello World</div></template>`,
			wantPass:    true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dir, filename, err := writeTempFile(t, "vue", tc.fileContent)
			if err != nil {
				t.Fatalf("Failed to write temp file: %v", err)
			}
			defer os.Remove(filepath.Join(dir, filename))
			envContainer := env.EnvContainer{
				Env: &env.LocalEnv{
					WorkingDirectory: dir,
				},
			}
			passed, errorString, err := CheckFileValidity(envContainer, filename)
			assert.NoError(t, err)
			if passed != tc.wantPass {
				t.Errorf("Want check pass = %v, got: %v", tc.wantPass, passed)
			}
			if !tc.wantPass {
				for _, expectedError := range tc.expectedErrors {
					if !strings.Contains(errorString, expectedError) {
						t.Errorf("Expected error string to contain '%s', but it was '%s'", expectedError, errorString)
					}
				}
			}
			if tc.wantPass && errorString != "" {
				t.Errorf("Didn't expect error: %s", errorString)
			}
		})
	}
}

func TestCheckFileValidity_Python(t *testing.T) {
	testCases := []struct {
		name           string
		fileContent    string
		wantPass       bool
		expectedErrors []string
	}{
		{
			name:           "empty file",
			fileContent:    "",
			wantPass:       false,
			expectedErrors: []string{"File is blank"},
		},
		{
			name:           "blank file",
			fileContent:    "\t\n\t \r\n",
			wantPass:       false,
			expectedErrors: []string{"File is blank"},
		},
		{
			name:        "only comments Python file",
			fileContent: `# only comments here`,
			wantPass:    true,
		},
		{
			name: "valid Python file",
			fileContent: `def main():
    print("Hello, World!")`,
			wantPass: true,
		},
		{
			name: "pass function body",
			fileContent: `def empty_function():
    pass`,
			wantPass: true,
		},
		{
			name: "pass method body in class",
			fileContent: `class MyClass:
    def empty_method(self):
        pass`,
			wantPass:       true,
			expectedErrors: []string{"Empty function body found"},
		},
		{
			name:           "empty function body",
			fileContent:    `def empty_function():`,
			wantPass:       false,
			expectedErrors: []string{"Empty function body found"},
		},
		{
			name: "empty method body in class",
			fileContent: `
class MyClass:
    def empty_method(self):`,
			wantPass:       false,
			expectedErrors: []string{"Empty function body found"},
		},
		{
			name:           "empty class body",
			fileContent:    `class EmptyClass:`,
			wantPass:       false,
			expectedErrors: []string{"Empty class body found"},
		},
		{
			name: "non-empty function body",
			fileContent: `def non_empty_function():
    print("This is a non-empty function")`,
			wantPass: true,
		},
		{
			name: "non-empty method body in class",
			fileContent: `class MyClass:
    def non_empty_method(self):
        print("This is a non-empty method")`,
			wantPass: true,
		},
		{
			name:           "empty file",
			fileContent:    "",
			wantPass:       false,
			expectedErrors: []string{"File is blank"},
		},
		{
			name:           "blank file",
			fileContent:    "\t\n\t \r\n",
			wantPass:       false,
			expectedErrors: []string{"File is blank"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dir, filename, err := writeTempFile(t, "py", tc.fileContent)
			if err != nil {
				t.Fatalf("Failed to write temp file: %v", err)
			}
			defer os.Remove(filepath.Join(dir, filename))

			envContainer := env.EnvContainer{
				Env: &env.LocalEnv{
					WorkingDirectory: dir,
				},
			}
			passed, errorString, err := CheckFileValidity(envContainer, filename)
			assert.NoError(t, err)
			if passed != tc.wantPass {
				t.Errorf("Want check pass = %v, got: %v", tc.wantPass, passed)
			}
			if !tc.wantPass {
				for _, expectedError := range tc.expectedErrors {
					if !strings.Contains(errorString, expectedError) {
						t.Errorf("Expected error string to contain '%s', but it was '%s'", expectedError, errorString)
					}
				}
			}
			if tc.wantPass && errorString != "" {
				t.Errorf("Didn't expect error: %s", errorString)
			}
		})
	}
}

func TestExtractErrors_NoErrors(t *testing.T) {
	// Setup a tree with no error nodes
	// Simulating the process of reading source code and parsing it to create a tree-sitter tree
	sitterLanguage := golang.GetLanguage()
	sourceCode := []byte("package main")
	parser := sitter.NewParser()
	parser.SetLanguage(sitterLanguage)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceCode)
	assert.Nil(t, err)
	assert.NotNil(t, tree)

	// Call ExtractErrors
	errors := ExtractErrorNodes(tree.RootNode())
	assert.Empty(t, errors)
}

func TestExtractErrors_MultipleErrors(t *testing.T) {
	// Setup a tree with multiple error nodes
	// Simulating the creation of a tree with multiple error nodes
	sitterLanguage := golang.GetLanguage()
	parser := sitter.NewParser()
	parser.SetLanguage(sitterLanguage)
	sourceCode := []byte("int x = y // error here")
	tree, err := parser.ParseCtx(context.Background(), nil, sourceCode)
	assert.Nil(t, err)
	assert.NotNil(t, tree)

	// Call ExtractErrors
	errors := ExtractErrorNodes(tree.RootNode())
	assert.Len(t, errors, 2)
}

func TestExtractErrors_NestedErrors(t *testing.T) {
	// Setup a tree with nested error nodes
	// Simulating the creation of a tree with nested error nodes
	sitterLanguage := golang.GetLanguage()
	parser := sitter.NewParser()
	parser.SetLanguage(sitterLanguage)
	sourceCode := []byte("package main\n\nfunc main() { int x = } // error here")
	tree, err := parser.ParseCtx(context.Background(), nil, sourceCode)
	assert.Nil(t, err)
	assert.NotNil(t, tree)

	// Call ExtractErrors
	errors := ExtractErrorNodes(tree.RootNode())
	assert.Len(t, errors, 1)
}
