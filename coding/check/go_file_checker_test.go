package check

import (
	"fmt"
	"os"
	"path/filepath"
	"sidekick/env"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func writeTempFile(t *testing.T, extension, code string) (string, string, error) {
	testDir := t.TempDir()
	tmpfile, err := os.CreateTemp(testDir, fmt.Sprintf("test*.%s", extension))
	if err != nil {
		return "", "", err
	}
	defer tmpfile.Close()

	if _, err := tmpfile.WriteString(code); err != nil {
		return "", "", err
	}

	return testDir, filepath.Base(tmpfile.Name()), nil
}
func TestCheckGoFile(t *testing.T) {

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
			name: "Ignores package imported and not used Go file",
			fileContent: `package main

import "fmt"
func main() {}`,
			wantPass: true,
		},
		{
			name: "Multiple errors: Package imported and not used + multiple import statements, only includes multiple import statements",
			fileContent: `package main

import "fmt"
import "fmt"
func main() {}`,
			wantPass:       false,
			expectedErrors: []string{"4:8: fmt redeclared in this block", "other declaration of fmt"},
		},
		{
			name: "Go file with syntax error",
			fileContent: `package main

func main() {`,
			wantPass:       false,
			expectedErrors: []string{"EOF"},
		},
		{
			name: "Invalid Go file with late additional import statements",
			fileContent: `package main

import "fmt"
func main() { fmt.Println(os.Args) }
import "os"`,
			wantPass:       false,
			expectedErrors: []string{"imports must appear before other declarations"},
		},
		{
			name:           "empty file",
			fileContent:    "",
			wantPass:       false,
			expectedErrors: []string{"EOF"},
		},
		{
			name: "Duplicate struct declarations",
			fileContent: `package main
type A struct {}
type A struct {}`,
			wantPass:       false,
			expectedErrors: []string{"redeclared in this block"},
		},
		{
			name: "Duplicate method declarations",
			fileContent: `package main
type A struct {}
func (a A) foo() {}
func (a A) foo() {}`,
			wantPass:       false,
			expectedErrors: []string{"already declared at"},
		},
		{
			name: "Valid struct + method declarations",
			fileContent: `package main
type A struct {}
func (a A) foo() {}

type B struct {}
func (b B) foo() {}
`,
			wantPass: true,
		},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
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
			passed, goCheckErrorString, err := CheckViaGoBuild(envContainer, filename)
			assert.NoError(t, err)
			// fmt.Println(goCheckErrorString) // debug
			if passed != tc.wantPass {
				t.Errorf("Want check pass = %v, got: %v", tc.wantPass, passed)
			}
			if !tc.wantPass {
				for _, expectedError := range tc.expectedErrors {
					if !strings.Contains(goCheckErrorString, expectedError) {
						t.Errorf("Expected error string to contain '%s', but it was '%s'", expectedError, goCheckErrorString)
					}
				}
			}
		})
	}
}
