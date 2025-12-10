package check

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sidekick/env"
	"strings"
)

var blacklistErrorsRegex = regexp.MustCompile(strings.Join([]string{
	"syntax error:",
	"already declared at",
	"redeclared in this block",
	"other declaration of",
	"imports must appear before other declarations",
	"EOF",
}, "|"))

// CheckViaGoBuild checks a Go source file for errors via go's build system. To
// avoid actually creating binaries and get faster feedback, `go test -c` is
// used instead.
// Returns true if the file is valid, false otherwise, along with a string containing any errors found.
// This is limited to considering errors that are blacklisted, i.e. only very
// bad errors that should revert the edit that caused them.
func CheckViaGoBuild(envContainer env.EnvContainer, relativeFilePath string) (bool, string, error) {
	// Get all files in the directory to build, to avoid errors due to missing dependencies from other files within the same package
	dir := filepath.Dir(filepath.Join(envContainer.Env.GetWorkingDirectory(), relativeFilePath))
	args := []string{"test", "-c"}
	files, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Sprintf("Failed to read directory: %v", err), err
	}
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".go") {
			if !strings.HasSuffix(relativeFilePath, "_test.go") && strings.HasSuffix(file.Name(), "_test.go") {
				// unless checking a test file, no need to include test files in compilation
				continue
			}
			args = append(args, filepath.Join(dir, file.Name()))
		}
	}

	result, err := envContainer.Env.RunCommand(context.Background(), env.EnvRunCommandInput{
		Command: "go",
		Args:    args,
	})
	if err != nil {
		return false, fmt.Sprintf("Failed to run go test compile: %v", err), err
	}

	if result.ExitStatus != 0 {
		lines := strings.Split(result.Stderr, "\n")
		var matchedErrors []string
		for _, line := range lines {
			// nope: if strings.Contains(line, relativeFilePath) {
			if blacklistErrorsRegex.MatchString(line) {
				matchedErrors = append(matchedErrors, strings.ReplaceAll(line, envContainer.Env.GetWorkingDirectory(), ""))
			}
		}
		if len(matchedErrors) > 0 {
			return false, fmt.Sprintf("Go test compile errors:\n%s", strings.Join(matchedErrors, "\n")), nil
		}
	}
	return true, "", nil
}
