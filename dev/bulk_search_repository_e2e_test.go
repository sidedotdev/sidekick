package dev

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/env"
	"sidekick/utils"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type BulkSearchRepositoryE2ETestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env          *testsuite.TestWorkflowEnvironment
	dir          string
	envContainer env.EnvContainer

	// a wrapper is required to set the ctx1 value, so that we can a method that
	// isn't a real workflow. otherwise we get errors about not having
	// StartToClose or ScheduleToCloseTimeout set
	wrapperWorkflow func(ctx workflow.Context, envContainer env.EnvContainer, params BulkSearchRepositoryParams) (string, error)
}

func (s *BulkSearchRepositoryE2ETestSuite) SetupTest() {
	// log warnings only (default debug level is too noisy when tests fail)
	th := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{AddSource: false, Level: slog.LevelWarn})
	s.SetLogger(tlog.NewStructuredLogger(slog.New(th)))

	// Create a temporary directory for the test
	s.dir = s.T().TempDir()

	// Initialize git repository so that rg respects .gitignore files
	cmd := exec.Command("git", "init")
	cmd.Dir = s.dir
	err := cmd.Run()
	s.Require().NoError(err)

	// Set up the environment container
	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{
		RepoDir: s.dir,
	})
	s.Require().NoError(err)
	s.envContainer = env.EnvContainer{
		Env: devEnv,
	}

	// setting up for the first time is the same as resetting
	s.ResetWorkflowEnvironment()
}

func (s *BulkSearchRepositoryE2ETestSuite) ResetWorkflowEnvironment() {
	if s.env != nil {
		s.env.AssertExpectations(s.T())
	}

	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
	s.env.SetTestTimeout(30 * time.Second)
	s.env.RegisterActivity(env.EnvRunCommandActivity)
	s.env.RegisterActivity(GetSymbolsActivity)
	s.env.RegisterActivity(EnsureCoreIgnoreFileActivity)
	s.env.RegisterActivity(BulkSearchRepositoryActivity)

	s.wrapperWorkflow = func(ctx workflow.Context, envContainer env.EnvContainer, params BulkSearchRepositoryParams) (string, error) {
		ctx1 := utils.NoRetryCtx(ctx)
		return BulkSearchRepository(ctx1, envContainer, params)
	}
	s.env.RegisterWorkflow(s.wrapperWorkflow)
}

func (s *BulkSearchRepositoryE2ETestSuite) TearDownTest() {
	s.env.AssertExpectations(s.T())
}

func (s *BulkSearchRepositoryE2ETestSuite) AfterTest(suiteName, testName string) {
	if s.T().Failed() {
		s.T().Logf("Test failed, temporary directory: %s", s.dir)
	}
}

// Helper function to create a test file with given content
func (s *BulkSearchRepositoryE2ETestSuite) createTestFile(filename, content string) {
	filepath := filepath.Join(s.dir, filename)
	err := os.WriteFile(filepath, []byte(content), 0644)
	s.Require().NoError(err)
}

// Helper function to execute the BulkSearchRepository workflow
func (s *BulkSearchRepositoryE2ETestSuite) executeBulkSearchRepository(params BulkSearchRepositoryParams) (string, error) {
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, params)
	var result string
	err := s.env.GetWorkflowResult(&result)
	return result, err
}

func TestBulkSearchRepositorySuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(BulkSearchRepositoryE2ETestSuite))
}

func (s *BulkSearchRepositoryE2ETestSuite) TestPathGlobIsNonExistentFile() {
	// Execute bulk search with non-existent file
	result, err := s.executeBulkSearchRepository(BulkSearchRepositoryParams{
		ContextLines: 0,
		Searches: []SingleSearchParams{
			{PathGlob: "nonexistent.txt", SearchTerm: "test"},
		},
	})

	// Verify results
	s.Require().NoError(err)
	s.Contains(result, "No files matched the path glob")
}

func (s *BulkSearchRepositoryE2ETestSuite) TestPathGlobIsExistentFileWithoutMatches() {
	// Create a Go file with some symbols
	s.createTestFile("example.go", `package example

func ExampleFunc() string {
	return "example"
}

type ExampleType struct {
	Field string
}
`)

	// Execute bulk search with non-matching term
	result, err := s.executeBulkSearchRepository(BulkSearchRepositoryParams{
		ContextLines: 0,
		Searches: []SingleSearchParams{
			{PathGlob: "example.go", SearchTerm: "nonexistent"},
		},
	})

	// Verify results include symbol information
	s.Require().NoError(err)
	s.Contains(result, "No results found for search term 'nonexistent' in file 'example.go'")
	s.Contains(result, "ExampleFunc")
	s.Contains(result, "ExampleType")
}

// TestSchedulesSingleActivity ensures the workflow no longer sends per-command
// inputs and outputs over the activity boundary.
func (s *BulkSearchRepositoryE2ETestSuite) TestSchedulesSingleActivity() {
	s.createTestFile("test1.txt", "This is test file one\nwith some content\nfor testing")

	scheduledActivities := []string{}
	s.env.SetOnActivityStartedListener(func(activityInfo *activity.Info, ctx context.Context, args converter.EncodedValues) {
		scheduledActivities = append(scheduledActivities, activityInfo.ActivityType.Name)
	})

	_, err := s.executeBulkSearchRepository(BulkSearchRepositoryParams{
		ContextLines: 0,
		Searches: []SingleSearchParams{
			{PathGlob: "test1.txt", SearchTerm: "one"},
			{PathGlob: "nonexistent.txt", SearchTerm: "missing"},
		},
	})
	s.Require().NoError(err)
	s.Require().Equal([]string{"BulkSearchRepositoryActivity"}, scheduledActivities)
}

func (s *BulkSearchRepositoryE2ETestSuite) TestBasicBulkSearch() {
	// Create test files
	s.createTestFile("test1.txt", "This is test file one\nwith multiple lines\nfor searching")
	s.createTestFile("test2.txt", "This is test file two\nwith different content\nfor testing")

	// Execute the bulk search
	result, err := s.executeBulkSearchRepository(BulkSearchRepositoryParams{
		ContextLines: 0,
		Searches: []SingleSearchParams{
			{PathGlob: "test1.txt", SearchTerm: "one"},
			{PathGlob: "test2.txt", SearchTerm: "two"},
		},
	})

	// Verify the results
	s.Require().NoError(err)
	s.Contains(result, "test1.txt")
	s.Contains(result, "test file one")
	s.Contains(result, "test2.txt")
	s.Contains(result, "test file two")
}

func (s *BulkSearchRepositoryE2ETestSuite) TestSideignoreUnignoresGitignored() {
	// Create a directory that will be ignored by .gitignore but un-ignored by .sideignore
	err := os.MkdirAll(filepath.Join(s.dir, "vendor"), 0755)
	s.Require().NoError(err)

	// .gitignore ignores the vendor directory
	s.createTestFile(".gitignore", "vendor/")

	// .sideignore un-ignores the vendor directory using negation pattern
	s.createTestFile(".sideignore", "!vendor/")

	// Create a file inside the vendor directory
	s.createTestFile("vendor/lib.go", "package vendor\n\nfunc LibFunction() string {\n\treturn \"hello from vendor\"\n}")

	// Create a file outside vendor for comparison
	s.createTestFile("main.go", "package main\n\nfunc main() {\n\tprintln(\"hello from main\")\n}")

	// Execute bulk search looking for content in both files
	result, err := s.executeBulkSearchRepository(BulkSearchRepositoryParams{
		ContextLines: 0,
		Searches: []SingleSearchParams{
			{PathGlob: "**/*.go", SearchTerm: "hello"},
		},
	})

	// Verify results
	s.Require().NoError(err)
	s.Contains(result, "main.go")
	s.Contains(result, "hello from main")

	// The vendor file should be found because .sideignore un-ignores it.
	// This verifies that .sideignore negation patterns override .gitignore.
	s.Contains(result, "vendor/lib.go", "vendor/lib.go should be found because .sideignore un-ignores it")
	s.Contains(result, "hello from vendor")
}

func (s *BulkSearchRepositoryE2ETestSuite) TestSearchFindsResultsInSideignoredFile() {
	// Create a file in a directory that's ignored by .sideignore
	err := os.MkdirAll(filepath.Join(s.dir, "mocks"), 0755)
	s.Require().NoError(err)

	s.createTestFile("mocks/client.go", `package mocks

func (_m *Client) DoSomething() error {
	return nil
}
`)

	s.createTestFile(".sideignore", "mocks\n")

	result, err := s.executeBulkSearchRepository(BulkSearchRepositoryParams{
		ContextLines: 0,
		Searches: []SingleSearchParams{
			{PathGlob: "mocks/client.go", SearchTerm: "func (_m *Client) DoSomething"},
		},
	})
	s.Require().NoError(err)
	s.Contains(result, "DoSomething")
	s.NotContains(result, "No results found")
	s.NotContains(result, "No files matched")
}

const ignoredFixtureTerm = "needleInIgnoredFixture"

// setupIgnoredFixture creates a non-ignored lib/visible.go and the file at
// ignoredPath, both containing ignoredFixtureTerm. Applying the exclusion for
// ignoredPath is left to the caller so each ignore mechanism is isolated.
func (s *BulkSearchRepositoryE2ETestSuite) setupIgnoredFixture(ignoredPath string) {
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, "lib"), 0755))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, filepath.Dir(ignoredPath)), 0755))
	s.createTestFile("lib/visible.go", "package lib\n\n// "+ignoredFixtureTerm+" visible\n")
	s.createTestFile(ignoredPath, "package lib\n\n// "+ignoredFixtureTerm+" ignored\n")
}

func (s *BulkSearchRepositoryE2ETestSuite) requireGitIgnored(path string) {
	cmd := exec.Command("git", "check-ignore", "-q", path)
	cmd.Dir = s.dir
	s.Require().NoError(cmd.Run(), "%s should be git-ignored", path)
}

func (s *BulkSearchRepositoryE2ETestSuite) searchOne(pathGlob string) string {
	s.ResetWorkflowEnvironment()
	result, err := s.executeBulkSearchRepository(BulkSearchRepositoryParams{
		ContextLines: 0,
		Searches: []SingleSearchParams{
			{PathGlob: pathGlob, SearchTerm: ignoredFixtureTerm},
		},
	})
	s.Require().NoError(err)
	return result
}

// assertIgnoredFileSearchable checks that the ignored fixture file is found
// via its exact path and via the path-specific glob (alongside any non-ignored
// matches), while a broad search still excludes it.
func (s *BulkSearchRepositoryE2ETestSuite) assertIgnoredFileSearchable(ignoredPath, pathSpecificGlob string, alsoExpectedInGlob ...string) {
	result := s.searchOne(ignoredPath)
	s.Contains(result, ignoredPath, "exact path search should find ignored file")
	s.Contains(result, ignoredFixtureTerm+" ignored")

	result = s.searchOne(pathSpecificGlob)
	s.Contains(result, ignoredPath, "path-specific glob %q should find ignored file", pathSpecificGlob)
	s.Contains(result, ignoredFixtureTerm+" ignored")
	for _, expected := range alsoExpectedInGlob {
		s.Contains(result, expected, "path-specific glob %q should still find non-ignored file", pathSpecificGlob)
	}

	result = s.searchOne("**/*.go")
	s.Contains(result, "lib/visible.go")
	s.NotContains(result, ignoredPath, "broad search must keep respecting ignores")
}

func (s *BulkSearchRepositoryE2ETestSuite) TestExplicitTargetBypassesGitInfoExclude() {
	s.setupIgnoredFixture("lib/excluded.go")
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, ".git", "info"), 0755))
	s.createTestFile(".git/info/exclude", "lib/excluded.go\n")
	s.requireGitIgnored("lib/excluded.go")

	s.assertIgnoredFileSearchable("lib/excluded.go", "lib/*.go", "lib/visible.go")
}

func (s *BulkSearchRepositoryE2ETestSuite) TestExplicitTargetBypassesGitInfoExcludeDirectory() {
	s.setupIgnoredFixture("private/notes.go")
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, ".git", "info"), 0755))
	s.createTestFile(".git/info/exclude", "private/\n")
	s.requireGitIgnored("private/notes.go")

	s.assertIgnoredFileSearchable("private/notes.go", "private/*.go")
}

// TestGitInfoExcludeRespectedOutsideExplicitTarget pins down that
// .git/info/exclude keeps applying to everything the target does not name,
// both in broad searches and in the explicit-target search mode, which
// assembles its ignore rules itself rather than relying on rg's git support.
func (s *BulkSearchRepositoryE2ETestSuite) TestGitInfoExcludeRespectedOutsideExplicitTarget() {
	s.setupIgnoredFixture("lib/excluded.go")
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, "lib", "sub"), 0755))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, "other"), 0755))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, ".git", "info"), 0755))
	s.createTestFile("lib/sub/deep.go", "package sub\n\n// "+ignoredFixtureTerm+" deep\n")
	s.createTestFile("other/ignored.go", "package other\n\n// "+ignoredFixtureTerm+" other\n")
	s.createTestFile(".git/info/exclude", "lib/excluded.go\nlib/sub/\nother/\n")
	s.requireGitIgnored("lib/excluded.go")
	s.requireGitIgnored("lib/sub/deep.go")
	s.requireGitIgnored("other/ignored.go")

	result := s.searchOne("lib/*.go")
	s.Contains(result, "lib/visible.go")
	s.Contains(result, "lib/excluded.go")
	s.NotContains(result, "lib/sub/deep.go", "git-excluded files not matching the glob must stay hidden")
	s.NotContains(result, "other/ignored.go", "git-excluded files outside the target must stay hidden")

	result = s.searchOne("lib/visible.go")
	s.Contains(result, "lib/visible.go")
	s.NotContains(result, "lib/excluded.go")
	s.NotContains(result, "other/ignored.go")

	for _, glob := range []string{"**/*.go", "*.go", "**/*"} {
		result = s.searchOne(glob)
		s.Contains(result, "lib/visible.go", "broad glob %q", glob)
		s.NotContains(result, "lib/excluded.go", "broad glob %q must respect .git/info/exclude", glob)
		s.NotContains(result, "lib/sub/deep.go", "broad glob %q must respect .git/info/exclude", glob)
		s.NotContains(result, "other/ignored.go", "broad glob %q must respect .git/info/exclude", glob)
	}

	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, "pkg"), 0755))
	s.createTestFile("pkg/ignored.go", "package pkg\n\n// "+ignoredFixtureTerm+" pkg\n")
	result = s.searchOne("*/ignored.go")
	s.Contains(result, "pkg/ignored.go")
	s.NotContains(result, ignoredFixtureTerm+" other", "git-excluded directory behind a leading wildcard must not be searched")
	s.Contains(result, "\tother/\n", "git-excluded directories must be reported as skipped")
}

func (s *BulkSearchRepositoryE2ETestSuite) TestExplicitTargetBypassesRootGitignore() {
	s.setupIgnoredFixture("lib/generated.go")
	s.createTestFile(".gitignore", "lib/generated.go\n")
	s.requireGitIgnored("lib/generated.go")

	s.assertIgnoredFileSearchable("lib/generated.go", "lib/*.go", "lib/visible.go")
}

func (s *BulkSearchRepositoryE2ETestSuite) TestExplicitTargetBypassesNestedGitignore() {
	s.setupIgnoredFixture("lib/generated.go")
	s.createTestFile("lib/.gitignore", "generated.go\n")
	s.requireGitIgnored("lib/generated.go")

	s.assertIgnoredFileSearchable("lib/generated.go", "lib/*.go", "lib/visible.go")
}

func (s *BulkSearchRepositoryE2ETestSuite) TestExplicitTargetBypassesSideignore() {
	s.setupIgnoredFixture("lib/mock.go")
	s.createTestFile(".sideignore", "lib/mock.go\n")

	s.assertIgnoredFileSearchable("lib/mock.go", "lib/*.go", "lib/visible.go")
}

func (s *BulkSearchRepositoryE2ETestSuite) TestExplicitTargetBypassesSideignoreWithGitignorePresent() {
	s.setupIgnoredFixture("lib/mock.go")
	s.createTestFile(".gitignore", "unrelated.tmp\n")
	s.createTestFile(".sideignore", "lib/mock.go\n")

	s.assertIgnoredFileSearchable("lib/mock.go", "lib/*.go", "lib/visible.go")
}

// TestExplicitTargetBypassesRipgrepIgnoreFiles covers .ignore and .rgignore,
// which rg gives higher precedence than any --ignore-file.
func (s *BulkSearchRepositoryE2ETestSuite) TestExplicitTargetBypassesRipgrepIgnoreFiles() {
	s.setupIgnoredFixture("lib/dotignored.go")
	s.createTestFile("lib/rgignored.go", "package lib\n\n// "+ignoredFixtureTerm+" ignored\n")
	s.createTestFile(".ignore", "lib/dotignored.go\n")
	s.createTestFile(".rgignore", "lib/rgignored.go\n")

	s.assertIgnoredFileSearchable("lib/dotignored.go", "lib/*.go", "lib/visible.go", "lib/rgignored.go")
	s.assertIgnoredFileSearchable("lib/rgignored.go", "lib/*.go", "lib/visible.go", "lib/dotignored.go")
}

// TestExplicitTargetBraceGlobIncludesIgnoredFile covers doublestar brace
// alternation, which must translate into a working unignore pattern.
func (s *BulkSearchRepositoryE2ETestSuite) TestExplicitTargetBraceGlobIncludesIgnoredFile() {
	s.setupIgnoredFixture("lib/a.go")
	s.createTestFile("lib/b.go", "package lib\n\n// "+ignoredFixtureTerm+" b\n")
	s.createTestFile(".gitignore", "lib/a.go\n")
	s.requireGitIgnored("lib/a.go")

	result := s.searchOne("lib/{a,b}.go")
	s.Contains(result, "lib/a.go")
	s.Contains(result, "lib/b.go")
	s.NotContains(result, "lib/visible.go")
}

func (s *BulkSearchRepositoryE2ETestSuite) TestExplicitTargetBypassesCoreIgnore() {
	s.setupIgnoredFixture(".side/tmp/scratch.go")

	s.assertIgnoredFileSearchable(".side/tmp/scratch.go", ".side/tmp/*.go")
}

func (s *BulkSearchRepositoryE2ETestSuite) TestExplicitTargetBypassIsScopedToTarget() {
	s.setupIgnoredFixture("lib/generated.go")
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, "lib", "sub"), 0755))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, "other"), 0755))
	s.createTestFile("lib/sub/deep.go", "package sub\n\n// "+ignoredFixtureTerm+" deep\n")
	s.createTestFile("other/ignored.go", "package other\n\n// "+ignoredFixtureTerm+" other\n")
	s.createTestFile(".gitignore", "lib/generated.go\nlib/sub/\nother/\n")
	s.requireGitIgnored("lib/generated.go")
	s.requireGitIgnored("lib/sub/deep.go")
	s.requireGitIgnored("other/ignored.go")

	result := s.searchOne("lib/*.go")
	s.Contains(result, "lib/visible.go")
	s.Contains(result, "lib/generated.go")
	s.NotContains(result, "lib/sub/deep.go", "ignored files not matching the glob must stay hidden")
	s.NotContains(result, "other/ignored.go", "ignored files outside the target must stay hidden")

	result = s.searchOne("lib/generated.go")
	s.Contains(result, "lib/generated.go")
	s.NotContains(result, "other/ignored.go")
}

func (s *BulkSearchRepositoryE2ETestSuite) TestLiteralPrefixGlobstarIncludesIgnoredFiles() {
	s.setupIgnoredFixture("lib/generated.go")
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, "lib", "sub"), 0755))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, "other"), 0755))
	s.createTestFile("lib/sub/deep.go", "package sub\n\n// "+ignoredFixtureTerm+" deep\n")
	s.createTestFile("lib/sub/notes.txt", ignoredFixtureTerm+" text\n")
	s.createTestFile("other/ignored.go", "package other\n\n// "+ignoredFixtureTerm+" other\n")
	s.createTestFile(".gitignore", "lib/generated.go\nlib/sub/\nother/\n")
	s.requireGitIgnored("lib/generated.go")
	s.requireGitIgnored("lib/sub/deep.go")
	s.requireGitIgnored("other/ignored.go")

	result := s.searchOne("lib/**/*.go")
	s.Contains(result, "lib/visible.go")
	s.Contains(result, "lib/generated.go")
	s.Contains(result, "lib/sub/deep.go")
	s.NotContains(result, "lib/sub/notes.txt", "ignored files not matching the glob must stay hidden")
	s.NotContains(result, "other/ignored.go", "ignored files outside the literal prefix must stay hidden")

	result = s.searchOne("**/*.go")
	s.Contains(result, "lib/visible.go")
	s.NotContains(result, "lib/generated.go")
	s.NotContains(result, "lib/sub/deep.go")
	s.NotContains(result, "other/ignored.go")
}

// TestExplicitTargetNoMatchFallbackStaysScoped covers the empty-result
// fallback: hints about matches outside the target glob must not expose
// ignored files, while ignored files matching the glob are still listed.
func (s *BulkSearchRepositoryE2ETestSuite) TestExplicitTargetNoMatchFallbackStaysScoped() {
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, "lib"), 0755))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, "other"), 0755))
	s.createTestFile("lib/visible.go", "package lib\n\n// nothing to see here\n")
	s.createTestFile("lib/generated.go", "package lib\n\n// nothing to see here either\n")
	s.createTestFile("other/ignored.go", "package other\n\n// "+ignoredFixtureTerm+" other\n")
	s.createTestFile("elsewhere.go", "package main\n\n// "+ignoredFixtureTerm+" elsewhere\n")
	s.createTestFile(".gitignore", "lib/generated.go\nother/\n")
	s.requireGitIgnored("lib/generated.go")
	s.requireGitIgnored("other/ignored.go")

	result := s.searchOne("lib/*.go")
	s.Contains(result, "lib/visible.go")
	s.Contains(result, "lib/generated.go", "ignored files matching the target should be listed as searched")
	s.Contains(result, "elsewhere.go", "non-ignored matches elsewhere should still be hinted")
	s.NotContains(result, "other/ignored.go", "fallback hints must not expose unrelated ignored files")
	s.NotContains(result, ignoredFixtureTerm+" other")
}

// TestLeadingWildcardGlobReportsHiddenIgnoredDirs covers wholly ignored
// directories that a glob reaches only through a leading wildcard: they are
// not searched while other matches exist, but are reported with guidance.
// The report must reflect the rules the search actually applied: root
// gitignore/sideignore/core exclusions count, while directories re-included by
// .sideignore or excluded only by a nested .gitignore are searched and so
// must not be reported.
func (s *BulkSearchRepositoryE2ETestSuite) TestLeadingWildcardGlobReportsHiddenIgnoredDirs() {
	s.setupIgnoredFixture("libext/mocks/hidden_mock.go")
	for _, dir := range []string{"lib/mocks", "lib/cache/mocks", "build", "vendor/mocks", "tmpout/mocks", ".side/tmp/mocks"} {
		s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, dir), 0755))
	}
	s.createTestFile("lib/mocks/visible_mock.go", "package mocks\n\n// "+ignoredFixtureTerm+" visible mock\n")
	s.createTestFile("build/out.go", "package build\n\n// "+ignoredFixtureTerm+" build\n")
	s.createTestFile("lib/cache/mocks/nested_mock.go", "package mocks\n\n// "+ignoredFixtureTerm+" nested\n")
	s.createTestFile("vendor/mocks/vendored_mock.go", "package mocks\n\n// "+ignoredFixtureTerm+" vendored\n")
	s.createTestFile("tmpout/mocks/tmp_mock.go", "package mocks\n\n// "+ignoredFixtureTerm+" tmpout\n")
	s.createTestFile(".side/tmp/mocks/core_mock.go", "package mocks\n\n// "+ignoredFixtureTerm+" core\n")
	s.createTestFile(".gitignore", "libext/\nbuild/\nvendor/\n")
	s.createTestFile(".sideignore", "!vendor/\ntmpout/\n")
	s.createTestFile("lib/.gitignore", "cache/\n")
	s.requireGitIgnored("libext/mocks/hidden_mock.go")
	s.requireGitIgnored("lib/cache/mocks/nested_mock.go")
	s.requireGitIgnored("vendor/mocks/vendored_mock.go")

	result := s.searchOne("**/mocks/*.go")
	s.Contains(result, "lib/mocks/visible_mock.go")
	s.Contains(result, "vendor/mocks/vendored_mock.go", ".sideignore negations re-include gitignored directories")
	s.Contains(result, "lib/cache/mocks/nested_mock.go", "nested .gitignore rules are not applied by explicit-target searches")
	s.NotContains(result, "hidden_mock.go")
	s.NotContains(result, "tmp_mock.go", ".sideignore-only exclusions stay hidden")
	s.NotContains(result, "core_mock.go", "core ignore exclusions stay hidden")
	s.NotContains(result, ignoredFixtureTerm+" build", "unrelated ignored content must stay hidden")
	s.Contains(result, "3 ignored directories reachable only through the leading wildcard")
	s.Contains(result, "\tbuild/\n")
	s.Contains(result, "\tlibext/\n")
	s.Contains(result, "\ttmpout/\n", ".sideignore-only exclusions are reported")
	s.NotContains(result, ".side/", "core-ignored internals are not worth reporting")
	s.NotContains(result, "\tvendor/\n", "searched directories must not be reported as hidden")
	s.NotContains(result, "\tlib/cache/\n", "searched directories must not be reported as hidden")
	s.Contains(result, "e.g. 'build/**/mocks/*.go'")

	result = s.searchOne("*/mocks/*.go")
	s.Contains(result, "lib/mocks/visible_mock.go")
	s.Contains(result, "3 ignored directories reachable only through the leading wildcard")
	s.Contains(result, "\tbuild/\n")
	s.Contains(result, "\ttmpout/\n")
	s.Contains(result, "e.g. 'build/mocks/*.go'")

	// Without other matches, the NoIgnore retry reaches the hidden directories.
	s.createTestFile("lib/mocks/visible_mock.go", "package mocks\n")
	s.createTestFile("vendor/mocks/vendored_mock.go", "package mocks\n")
	s.createTestFile("lib/cache/mocks/nested_mock.go", "package mocks\n")
	result = s.searchOne("*/mocks/*.go")
	s.Contains(result, "libext/mocks/hidden_mock.go")
	s.Contains(result, "tmpout/mocks/tmp_mock.go")
	s.NotContains(result, "were not searched")

	result = s.searchOne("lib/**/*.go")
	s.Contains(result, "lib/visible.go")
	s.NotContains(result, "were not searched")
}

// TestLeadingWildcardGlobHiddenDirsListIsCapped covers the truncated report
// when many ignored directories are hidden behind a leading wildcard.
func (s *BulkSearchRepositoryE2ETestSuite) TestLeadingWildcardGlobHiddenDirsListIsCapped() {
	s.setupIgnoredFixture("lib/generated.go")
	var gitignore strings.Builder
	for i := 0; i < 12; i++ {
		dir := fmt.Sprintf("gen%02d", i)
		s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, dir), 0755))
		s.createTestFile(dir+"/visible.go", "package gen\n\n// "+ignoredFixtureTerm+" gen\n")
		fmt.Fprintf(&gitignore, "%s/\n", dir)
	}
	s.createTestFile(".gitignore", gitignore.String())
	s.requireGitIgnored("gen11/visible.go")

	result := s.searchOne("*/visible.go")
	s.Contains(result, "lib/visible.go")
	s.NotContains(result, ignoredFixtureTerm+" gen", "hidden directory content must not be searched")
	s.Contains(result, "12 ignored directories reachable only through the leading wildcard")
	s.Contains(result, "\tgen00/\n")
	s.Contains(result, "\tgen09/\n")
	s.NotContains(result, "\tgen10/\n")
	s.Contains(result, "\t... and 2 more\n")
	s.Contains(result, "e.g. 'gen00/visible.go'")
}

// TestPathSpecificGlobsWithoutLiteralPrefixIncludeIgnoredFiles covers globs
// that are path-specific only through a literal segment after a wildcard.
func (s *BulkSearchRepositoryE2ETestSuite) TestPathSpecificGlobsWithoutLiteralPrefixIncludeIgnoredFiles() {
	s.setupIgnoredFixture("lib/generated.go")
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, "lib", "mocks"), 0755))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, "libext"), 0755))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, "other"), 0755))
	s.createTestFile("lib/mocks/visible_mock.go", "package mocks\n\n// "+ignoredFixtureTerm+" visible mock\n")
	s.createTestFile("lib/mocks/ignored_mock.go", "package mocks\n\n// "+ignoredFixtureTerm+" ignored mock\n")
	s.createTestFile("libext/generated.go", "package libext\n\n// "+ignoredFixtureTerm+" libext\n")
	s.createTestFile("other/ignored.go", "package other\n\n// "+ignoredFixtureTerm+" other\n")
	s.createTestFile(".gitignore", "lib/generated.go\nlib/mocks/ignored_mock.go\nlibext/\nother/\n")
	s.requireGitIgnored("lib/generated.go")
	s.requireGitIgnored("lib/mocks/ignored_mock.go")
	s.requireGitIgnored("libext/generated.go")
	s.requireGitIgnored("other/ignored.go")

	for _, glob := range []string{"*/mocks/*.go", "**/mocks/*.go"} {
		result := s.searchOne(glob)
		s.Contains(result, "lib/mocks/visible_mock.go", "glob %q", glob)
		s.Contains(result, "lib/mocks/ignored_mock.go", "glob %q should include ignored file", glob)
		s.NotContains(result, "lib/generated.go", "glob %q must not expose ignored files outside the glob", glob)
		s.NotContains(result, "other/ignored.go", "glob %q must not expose ignored files outside the glob", glob)
	}

	result := s.searchOne("lib*/generated.go")
	s.Contains(result, "lib/generated.go")
	s.Contains(result, "libext/generated.go")
	s.NotContains(result, "lib/mocks/ignored_mock.go")
	s.NotContains(result, "other/ignored.go")

	for _, glob := range []string{"**/*.go", "*.go", "**/*"} {
		result := s.searchOne(glob)
		s.NotContains(result, "lib/generated.go", "broad glob %q must respect ignores", glob)
		s.NotContains(result, "lib/mocks/ignored_mock.go", "broad glob %q must respect ignores", glob)
		s.NotContains(result, "libext/generated.go", "broad glob %q must respect ignores", glob)
		s.NotContains(result, "other/ignored.go", "broad glob %q must respect ignores", glob)
	}
}
