package check

import (
	"bufio"
	"context"
	"fmt"
	"go/build/constraint"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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

// parseBuildConstraint extracts the build constraint expression from a Go file.
// Returns the parsed constraint expression, or nil if no constraint is found.
// For legacy // +build syntax, multiple lines are combined with AND.
func parseBuildConstraint(filePath string) constraint.Expr {
	file, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	var result constraint.Expr
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip empty lines
		if trimmed == "" {
			continue
		}

		// Stop at package declaration - build constraints must come before
		if strings.HasPrefix(trimmed, "package ") {
			break
		}

		// Check for //go:build directive (takes precedence, return immediately)
		if constraint.IsGoBuild(line) {
			expr, err := constraint.Parse(line)
			if err == nil {
				return expr
			}
		}

		// Check for // +build directive (multiple lines are ANDed together)
		if constraint.IsPlusBuild(line) {
			expr, err := constraint.Parse(line)
			if err == nil {
				if result == nil {
					result = expr
				} else {
					result = &constraint.AndExpr{X: result, Y: expr}
				}
			}
		}
	}
	return result
}

// knownOSes and knownArches are used to identify OS/arch-specific build tags
var knownOSes = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true,
	"freebsd": true, "hurd": true, "illumos": true, "ios": true,
	"js": true, "linux": true, "netbsd": true, "openbsd": true,
	"plan9": true, "solaris": true, "wasip1": true, "windows": true,
}

var knownArches = map[string]bool{
	"386": true, "amd64": true, "arm": true, "arm64": true,
	"loong64": true, "mips": true, "mips64": true, "mips64le": true,
	"mipsle": true, "ppc64": true, "ppc64le": true, "riscv64": true,
	"s390x": true, "wasm": true,
}

var unixOSes = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true,
	"freebsd": true, "hurd": true, "illumos": true, "ios": true,
	"linux": true, "netbsd": true, "openbsd": true, "solaris": true,
}

// constraintsConflict checks if two build constraints are mutually exclusive.
// Returns true only if we can definitively determine that no build context
// can satisfy both constraints. Conservative: returns false for unknown tags.
func constraintsConflict(expr1, expr2 constraint.Expr) bool {
	if expr1 == nil || expr2 == nil {
		return false
	}

	// Extract the OS/arch tags from both expressions
	tags1 := extractTags(expr1)
	tags2 := extractTags(expr2)

	// If either expression has unknown tags, be conservative and say no conflict
	for tag := range tags1 {
		if !isKnownTag(tag) {
			return false
		}
	}
	for tag := range tags2 {
		if !isKnownTag(tag) {
			return false
		}
	}

	// Two constraints conflict if their conjunction is unsatisfiable
	combined := &constraint.AndExpr{X: expr1, Y: expr2}
	return !isSatisfiable(combined)
}

// extractTags collects all tag names referenced in a constraint expression
func extractTags(expr constraint.Expr) map[string]bool {
	tags := make(map[string]bool)
	var collect func(e constraint.Expr)
	collect = func(e constraint.Expr) {
		switch v := e.(type) {
		case *constraint.TagExpr:
			tags[v.Tag] = true
		case *constraint.NotExpr:
			collect(v.X)
		case *constraint.AndExpr:
			collect(v.X)
			collect(v.Y)
		case *constraint.OrExpr:
			collect(v.X)
			collect(v.Y)
		}
	}
	collect(expr)
	return tags
}

// isKnownTag returns true if the tag is a known OS, arch, or special tag
func isKnownTag(tag string) bool {
	if knownOSes[tag] || knownArches[tag] {
		return true
	}
	if tag == "unix" || tag == "cgo" {
		return true
	}
	return false
}

// isSatisfiable checks if a constraint expression can be satisfied by any build context.
func isSatisfiable(expr constraint.Expr) bool {
	// Include all OSes from knownOSes
	osOptions := []string{
		"aix", "android", "darwin", "dragonfly", "freebsd", "hurd",
		"illumos", "ios", "js", "linux", "netbsd", "openbsd",
		"plan9", "solaris", "wasip1", "windows",
	}
	archOptions := []string{
		"386", "amd64", "arm", "arm64", "loong64", "mips", "mips64",
		"mips64le", "mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm",
	}
	// Try with cgo both enabled and disabled
	cgoOptions := []bool{true, false}

	for _, goos := range osOptions {
		for _, goarch := range archOptions {
			for _, cgo := range cgoOptions {
				if evalConstraint(expr, goos, goarch, cgo) {
					return true
				}
			}
		}
	}
	return false
}

// evalConstraintWithTags evaluates a constraint expression against a specific
// OS/arch/cgo and a set of enabled custom tags.
func evalConstraintWithTags(expr constraint.Expr, goos, goarch string, cgo bool, enabledTags map[string]bool) bool {
	return expr.Eval(func(tag string) bool {
		if tag == "unix" {
			return unixOSes[goos]
		}
		if tag == "cgo" {
			return cgo
		}
		if tag == goos || tag == goarch {
			return true
		}
		// Check custom/unknown tags
		if enabledTags != nil {
			return enabledTags[tag]
		}
		return false
	})
}

// evalConstraint evaluates a constraint expression against a specific OS/arch/cgo.
func evalConstraint(expr constraint.Expr, goos, goarch string, cgo bool) bool {
	return evalConstraintWithTags(expr, goos, goarch, cgo, nil)
}

// buildContext represents a complete build configuration
type buildContext struct {
	goos       string
	goarch     string
	cgo        bool
	customTags map[string]bool
}

// matchesBuildContextFull checks if a constraint matches the given full build context.
func matchesBuildContextFull(expr constraint.Expr, ctx buildContext) bool {
	if expr == nil {
		return true
	}
	return evalConstraintWithTags(expr, ctx.goos, ctx.goarch, ctx.cgo, ctx.customTags)
}

// findSatisfyingContext finds a complete build context that satisfies the given constraint.
// Prefers the current runtime OS/arch if it satisfies the constraint.
// Returns a zero buildContext if no satisfying context is found.
func findSatisfyingContext(expr constraint.Expr) buildContext {
	if expr == nil {
		return buildContext{goos: runtime.GOOS, goarch: runtime.GOARCH, cgo: false, customTags: nil}
	}

	// Extract custom tags from the expression
	tags := extractTags(expr)
	var customTagNames []string
	for tag := range tags {
		if !isKnownTag(tag) {
			customTagNames = append(customTagNames, tag)
		}
	}

	osOptions := []string{
		"aix", "android", "darwin", "dragonfly", "freebsd", "hurd",
		"illumos", "ios", "js", "linux", "netbsd", "openbsd",
		"plan9", "solaris", "wasip1", "windows",
	}
	archOptions := []string{
		"386", "amd64", "arm", "arm64", "loong64", "mips", "mips64",
		"mips64le", "mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm",
	}
	cgoOptions := []bool{false, true}

	// Generate all possible custom tag assignments
	tagAssignments := generateTagAssignments(customTagNames)

	// Try current runtime first
	for _, cgo := range cgoOptions {
		for _, tagAssignment := range tagAssignments {
			ctx := buildContext{
				goos:       runtime.GOOS,
				goarch:     runtime.GOARCH,
				cgo:        cgo,
				customTags: tagAssignment,
			}
			if matchesBuildContextFull(expr, ctx) {
				return ctx
			}
		}
	}

	// Search for any satisfying context
	for _, goos := range osOptions {
		for _, goarch := range archOptions {
			for _, cgo := range cgoOptions {
				for _, tagAssignment := range tagAssignments {
					ctx := buildContext{
						goos:       goos,
						goarch:     goarch,
						cgo:        cgo,
						customTags: tagAssignment,
					}
					if matchesBuildContextFull(expr, ctx) {
						return ctx
					}
				}
			}
		}
	}
	return buildContext{}
}

// generateTagAssignments generates all possible true/false assignments for the given tags.
// For n tags, generates 2^n assignments. Limits to reasonable number to avoid explosion.
func generateTagAssignments(tagNames []string) []map[string]bool {
	if len(tagNames) == 0 {
		return []map[string]bool{nil}
	}

	// Limit to 8 custom tags (256 combinations) to avoid explosion
	if len(tagNames) > 8 {
		tagNames = tagNames[:8]
	}

	numAssignments := 1 << len(tagNames)
	assignments := make([]map[string]bool, numAssignments)

	for i := 0; i < numAssignments; i++ {
		assignment := make(map[string]bool)
		for j, tag := range tagNames {
			assignment[tag] = (i & (1 << j)) != 0
		}
		assignments[i] = assignment
	}
	return assignments
}

// filterFilesByBuildTags filters a list of Go files to include only those that
// would be compiled together with the target file. It finds a build context
// (GOOS/GOARCH, cgo, and custom tags) that satisfies the target file's constraint,
// then includes only files that match that same context. The target file is
// always included.
func filterFilesByBuildTags(filePaths []string, targetFile string) []string {
	targetConstraint := parseBuildConstraint(targetFile)

	// Find a build context that satisfies the target constraint
	ctx := findSatisfyingContext(targetConstraint)
	if ctx.goos == "" {
		// No satisfying context found - just return the target file
		for _, fp := range filePaths {
			if fp == targetFile {
				return []string{targetFile}
			}
		}
		return filePaths
	}

	// Filter all files by the chosen build context
	var result []string
	for _, fp := range filePaths {
		if fp == targetFile {
			// Always include the target file
			result = append(result, fp)
			continue
		}
		expr := parseBuildConstraint(fp)
		if matchesBuildContextFull(expr, ctx) {
			result = append(result, fp)
		}
	}
	return result
}

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
	var goFiles []string
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".go") {
			if !strings.HasSuffix(relativeFilePath, "_test.go") && strings.HasSuffix(file.Name(), "_test.go") {
				// unless checking a test file, no need to include test files in compilation
				continue
			}
			goFiles = append(goFiles, filepath.Join(dir, file.Name()))
		}
	}
	targetFilePath := filepath.Join(dir, filepath.Base(relativeFilePath))
	goFiles = filterFilesByBuildTags(goFiles, targetFilePath)
	args = append(args, goFiles...)

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
