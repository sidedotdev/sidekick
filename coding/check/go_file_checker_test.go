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

func writeNamedTempFile(t *testing.T, dir, filename, code string) error {
	return os.WriteFile(filepath.Join(dir, filename), []byte(code), 0644)
}
func TestCheckGoFile_BuildTagsPreventFalseRedeclaration(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()

	// Create a file with unix build tag
	unixCode := `//go:build unix

package main

const maxOutputTail = 100
`
	err := writeNamedTempFile(t, testDir, "process_unix.go", unixCode)
	assert.NoError(t, err)

	// Create a file with windows build tag - same constant name
	windowsCode := `//go:build windows

package main

const maxOutputTail = 100
`
	err = writeNamedTempFile(t, testDir, "process_windows.go", windowsCode)
	assert.NoError(t, err)

	// Create a main file without build tags
	mainCode := `package main

func main() {}
`
	err = writeNamedTempFile(t, testDir, "main.go", mainCode)
	assert.NoError(t, err)

	envContainer := env.EnvContainer{
		Env: &env.LocalEnv{
			WorkingDirectory: testDir,
		},
	}

	// Check the unix file - should pass because windows file is filtered out (conflicting constraint)
	passed, errStr, err := CheckViaGoBuild(envContainer, "process_unix.go")
	assert.NoError(t, err)
	assert.True(t, passed, "Expected check to pass but got errors: %s", errStr)

	// Check the windows file - should also pass (unix file filtered out)
	passed, errStr, err = CheckViaGoBuild(envContainer, "process_windows.go")
	assert.NoError(t, err)
	assert.True(t, passed, "Expected check to pass but got errors: %s", errStr)

	// Check the main file - has no constraint, so we filter mutually conflicting files
	// keeping only one of the conflicting pair
	passed, errStr, err = CheckViaGoBuild(envContainer, "main.go")
	assert.NoError(t, err)
	assert.True(t, passed, "Expected check to pass but got errors: %s", errStr)
}

func TestCheckGoFile_LegacyBuildTags(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()

	// Create a file with legacy unix build tag
	unixCode := `// +build unix

package main

const platformName = "unix"
`
	err := writeNamedTempFile(t, testDir, "platform_unix.go", unixCode)
	assert.NoError(t, err)

	// Create a file with legacy windows build tag
	windowsCode := `// +build windows

package main

const platformName = "windows"
`
	err = writeNamedTempFile(t, testDir, "platform_windows.go", windowsCode)
	assert.NoError(t, err)

	mainCode := `package main

func main() {}
`
	err = writeNamedTempFile(t, testDir, "main.go", mainCode)
	assert.NoError(t, err)

	envContainer := env.EnvContainer{
		Env: &env.LocalEnv{
			WorkingDirectory: testDir,
		},
	}

	passed, errStr, err := CheckViaGoBuild(envContainer, "platform_unix.go")
	assert.NoError(t, err)
	assert.True(t, passed, "Expected check to pass but got errors: %s", errStr)
}

func TestCheckGoFile_CgoBuildTags(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()

	// Create a file with cgo build tag
	cgoCode := `//go:build cgo

package main

const buildMode = "cgo"
`
	err := writeNamedTempFile(t, testDir, "build_cgo.go", cgoCode)
	assert.NoError(t, err)

	// Create a file with negated cgo build tag
	noCgoCode := `//go:build !cgo

package main

const buildMode = "nocgo"
`
	err = writeNamedTempFile(t, testDir, "build_nocgo.go", noCgoCode)
	assert.NoError(t, err)

	mainCode := `package main

func main() {}
`
	err = writeNamedTempFile(t, testDir, "main.go", mainCode)
	assert.NoError(t, err)

	envContainer := env.EnvContainer{
		Env: &env.LocalEnv{
			WorkingDirectory: testDir,
		},
	}

	// Check the cgo file - should pass because nocgo file is filtered out
	passed, errStr, err := CheckViaGoBuild(envContainer, "build_cgo.go")
	assert.NoError(t, err)
	assert.True(t, passed, "Expected check to pass but got errors: %s", errStr)

	// Check the nocgo file - should also pass
	passed, errStr, err = CheckViaGoBuild(envContainer, "build_nocgo.go")
	assert.NoError(t, err)
	assert.True(t, passed, "Expected check to pass but got errors: %s", errStr)
}

func TestCheckGoFile_CustomBuildTags(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()

	// Create a file with custom build tag
	integrationCode := `//go:build integration

package main

const testMode = "integration"
`
	err := writeNamedTempFile(t, testDir, "test_integration.go", integrationCode)
	assert.NoError(t, err)

	// Create a file with negated custom build tag
	unitCode := `//go:build !integration

package main

const testMode = "unit"
`
	err = writeNamedTempFile(t, testDir, "test_unit.go", unitCode)
	assert.NoError(t, err)

	mainCode := `package main

func main() {}
`
	err = writeNamedTempFile(t, testDir, "main.go", mainCode)
	assert.NoError(t, err)

	envContainer := env.EnvContainer{
		Env: &env.LocalEnv{
			WorkingDirectory: testDir,
		},
	}

	// Check the integration file - should pass because unit file is filtered out
	passed, errStr, err := CheckViaGoBuild(envContainer, "test_integration.go")
	assert.NoError(t, err)
	assert.True(t, passed, "Expected check to pass but got errors: %s", errStr)

	// Check the unit file - should also pass
	passed, errStr, err = CheckViaGoBuild(envContainer, "test_unit.go")
	assert.NoError(t, err)
	assert.True(t, passed, "Expected check to pass but got errors: %s", errStr)
}

func TestCheckGoFile_NegatedBuildTags(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()

	// Create a file with negated windows build tag (common pattern for unix files)
	unixCode := `//go:build !windows

package main

const maxOutputTail = 100
`
	err := writeNamedTempFile(t, testDir, "process_unix.go", unixCode)
	assert.NoError(t, err)

	// Create a file with positive windows build tag
	windowsCode := `//go:build windows

package main

const maxOutputTail = 100
`
	err = writeNamedTempFile(t, testDir, "process_windows.go", windowsCode)
	assert.NoError(t, err)

	mainCode := `package main

func main() {}
`
	err = writeNamedTempFile(t, testDir, "main.go", mainCode)
	assert.NoError(t, err)

	envContainer := env.EnvContainer{
		Env: &env.LocalEnv{
			WorkingDirectory: testDir,
		},
	}

	// Check the unix file - should pass because windows file is filtered out
	passed, errStr, err := CheckViaGoBuild(envContainer, "process_unix.go")
	assert.NoError(t, err)
	assert.True(t, passed, "Expected check to pass but got errors: %s", errStr)

	// Check the windows file - should also pass
	passed, errStr, err = CheckViaGoBuild(envContainer, "process_windows.go")
	assert.NoError(t, err)
	assert.True(t, passed, "Expected check to pass but got errors: %s", errStr)
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
