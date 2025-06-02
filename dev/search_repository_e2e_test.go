package dev

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/coding/tree_sitter"
	"sidekick/env"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type SearchRepositoryE2ETestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env          *testsuite.TestWorkflowEnvironment
	dir          string
	envContainer env.EnvContainer

	// a wrapper is required to set the ctx1 value, so that we can a method that
	// isn't a real workflow. otherwise we get errors about not having
	// StartToClose or ScheduleToCloseTimeout set
	wrapperWorkflow func(ctx workflow.Context, envContainer env.EnvContainer, input SearchRepositoryInput) (string, error)
}

func (s *SearchRepositoryE2ETestSuite) SetupTest() {
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

func (s *SearchRepositoryE2ETestSuite) ResetWorkflowEnvironment() {
	if s.env != nil {
		// if there was already an env we are resetting from, we need to assert
		// before overwriting s.env
		s.env.AssertExpectations(s.T())
	}

	// Create a new TestWorkflowEnvironment for each test
	s.env = s.NewTestWorkflowEnvironment()

	s.env.RegisterActivity(env.EnvRunCommandActivity)
	s.wrapperWorkflow = func(ctx workflow.Context, envContainer env.EnvContainer, input SearchRepositoryInput) (string, error) {
		ctx1 := utils.NoRetryCtx(ctx)
		return SearchRepository(ctx1, envContainer, input)
	}
	s.env.RegisterWorkflow(s.wrapperWorkflow)
}

func (s *SearchRepositoryE2ETestSuite) TearDownTest() {
	// Clean up after each test
	if s.dir != "" {
		os.RemoveAll(s.dir)
	}
	// Reset the environment for the next test
	s.env = nil
	s.dir = ""
	s.envContainer = env.EnvContainer{}
}

func (s *SearchRepositoryE2ETestSuite) AfterTest(suiteName, testName string) {
	if s.env != nil {
		s.env.AssertExpectations(s.T())
	}
}

// Helper function to create a test file with given content
func (s *SearchRepositoryE2ETestSuite) createTestFile(filename, content string) {
	filepath := filepath.Join(s.dir, filename)
	err := os.WriteFile(filepath, []byte(content), 0644)
	s.Require().NoError(err)
}

// Helper function to execute the SearchRepository workflow
func (s *SearchRepositoryE2ETestSuite) executeSearchRepository(input SearchRepositoryInput) (string, error) {
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, input) // NOTE: this doesn't return anything, and that's by design!
	var result string
	err := s.env.GetWorkflowResult(&result)
	return result, err
}

func (s *SearchRepositoryE2ETestSuite) TestBasicSearch() {
	// Create a test file
	s.createTestFile("test.txt", "This is a test file\nwith multiple lines\nfor searching")

	// Execute the search
	result, err := s.executeSearchRepository(SearchRepositoryInput{
		PathGlob:     "*",
		SearchTerm:   "test",
		ContextLines: 0,
	})

	// Verify the results
	s.Require().NoError(err)
	s.Contains(result, "test.txt")
	s.Contains(result, "This is a test file")
}

func (s *SearchRepositoryE2ETestSuite) TestMultiwordSearch() {
	// Create a test file
	s.createTestFile("test.txt", "This is a test file\nwith multiple lines\nfor searching")

	// Execute the search
	result, err := s.executeSearchRepository(SearchRepositoryInput{
		PathGlob:     "*",
		SearchTerm:   "This is a test",
		ContextLines: 0,
	})

	// Verify the results
	s.Require().NoError(err)
	s.Contains(result, "test.txt")
	s.Contains(result, "This is a test file")
}

func (s *SearchRepositoryE2ETestSuite) TestTruncatedLongSearchOutputWithMultipleFiles() {
	// Create multiple large files with repeating content
	uniqueContent1 := strings.Repeat("first file content line\n", maxSearchOutputLength/len("first file content line\n")/2)
	uniqueContent2 := strings.Repeat("second file line here\n", maxSearchOutputLength/len("second file line here\n")/2)
	uniqueContent3 := strings.Repeat("third file test data\n", maxSearchOutputLength/len("third file test data\n")/2)

	s.createTestFile("file1.txt", uniqueContent1)
	s.createTestFile("file2.txt", uniqueContent2)
	s.createTestFile("file3.txt", uniqueContent3)

	// Test case: Output is truncated and additional files are listed
	result, err := s.executeSearchRepository(SearchRepositoryInput{
		PathGlob:   "*.txt",
		SearchTerm: "file",
	})

	s.Require().NoError(err)

	// Verify content is present but truncated
	searchBlocks := tree_sitter.ExtractSearchCodeBlocks(result)
	s.Assertions.Len(searchBlocks, 2)
	s.Contains(result, "file1.txt")
	s.Contains(searchBlocks[0].Code, strings.TrimSpace(uniqueContent1))
	s.Contains(result, "file2.txt")
	s.Contains(searchBlocks[1].Code, "second file line here\n")
	s.Contains(result, "file3.txt")
	s.NotContains(searchBlocks[1].Code, "third file test data\n")

	// Verify exact truncation message is included
	s.Contains(result, "... (search output truncated). The last file's results may be partial. Further matches exist in these files:")

	// Verify total output length constraint
	s.LessOrEqual(len(result), maxSearchOutputLength)
}

func (s *SearchRepositoryE2ETestSuite) TestTruncatedLongSearchOutput() {
	// Create a large file with repeating content
	largeContent := strings.Repeat("line\n", maxSearchOutputLength/len("line\n"))
	s.createTestFile("large_file.txt", largeContent)

	// Test case: Output is truncated but not refused
	result, err := s.executeSearchRepository(SearchRepositoryInput{
		PathGlob:   "*",
		SearchTerm: "line",
	})

	s.Require().NoError(err)
	s.Contains(result, "large_file.txt")
	s.Contains(result, "line\n")
	s.Contains(result, "... (search output truncated). The last file's matches are cut off, but no other files matched.")
	s.LessOrEqual(len(result), maxSearchOutputLength)
}

func (s *SearchRepositoryE2ETestSuite) TestRefusalForOverlyLongSearchOutput() {
	// Create a large file with repeating content
	largerContent := strings.Repeat("line\n", 1000)
	s.createTestFile("larger_file.txt", largerContent)

	// Test case: Output is too long and refused
	result, err := s.executeSearchRepository(SearchRepositoryInput{
		PathGlob:   "*",
		SearchTerm: "line",
	})

	s.Require().NoError(err)
	s.Contains(result, "Search output is too long.")
	s.Contains(result, "You could try with fewer context lines")
}

func TestSearchRepositorySuite(t *testing.T) {
	suite.Run(t, new(SearchRepositoryE2ETestSuite))
}

func (s *SearchRepositoryE2ETestSuite) TestPathGlobSearch() {
	// Create directories
	err := os.MkdirAll(filepath.Join(s.dir, "dir1"), 0755)
	s.Require().NoError(err)
	err = os.MkdirAll(filepath.Join(s.dir, "dir2"), 0755)
	s.Require().NoError(err)

	// Create files in different directories
	s.createTestFile("file1.txt", "This is file1")
	s.createTestFile("dir1/file2.txt", "This is file2")
	s.createTestFile("dir2/file3.txt", "This is file3")

	// Test with a glob that matches some files
	var result string
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:     "dir1/*.txt",
		SearchTerm:   "file",
		ContextLines: 0,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	s.Contains(result, "dir1/file2.txt")
	s.NotContains(result, "file1.txt")
	s.NotContains(result, "dir2/file3.txt")

	// Test with a glob that doesn't match any files
	s.ResetWorkflowEnvironment()
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:     "nonexistent/*.txt",
		SearchTerm:   "file",
		ContextLines: 0,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	s.Contains(result, "No files matched the path glob nonexistent/*.txt")
}

func (s *SearchRepositoryE2ETestSuite) TestPathGlobSearchWithGlobstar() {
	// Create directories
	err := os.MkdirAll(filepath.Join(s.dir, "sub", "deeper"), 0755)
	s.Require().NoError(err)

	// Create files with a common search term
	commonContent := "globstar_test_content"
	s.createTestFile("root_file.vue", commonContent)
	s.createTestFile("sub/sub_file.vue", commonContent)
	s.createTestFile("sub/deeper/deep_file.vue", commonContent)
	s.createTestFile("root_file.js", commonContent)    // Should not match the glob pattern
	s.createTestFile("sub/another.txt", commonContent) // Should not match

	// Execute the search
	var result string
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:     "**/*.vue",
		SearchTerm:   commonContent,
		ContextLines: 0,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	// Verify the results
	// These assertions are expected to fail with the current implementation
	s.Contains(result, "root_file.vue")
	s.Contains(result, filepath.Join("sub", "sub_file.vue"))
	s.Contains(result, filepath.Join("sub", "deeper", "deep_file.vue"))

	s.NotContains(result, "root_file.js")
	s.NotContains(result, filepath.Join("sub", "another.txt"))
}

func (s *SearchRepositoryE2ETestSuite) TestCaseSensitiveAndInsensitiveSearch() {
	// Create a file with mixed-case content
	s.createTestFile("mixed_case.txt", "This FILE contains mixed case content\nThis file contains mixed case content")

	// Perform a case-sensitive search
	var result string
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:     "*",
		SearchTerm:   "FILE",
		ContextLines: 0,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	s.Contains(result, "This FILE contains mixed case content")
	s.NotContains(result, "This file contains mixed case content")

	// Perform a case-insensitive search
	s.ResetWorkflowEnvironment()
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:        "*",
		SearchTerm:      "file",
		ContextLines:    0,
		CaseInsensitive: true,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	s.Contains(result, "This FILE contains mixed case content")
	s.Contains(result, "This file contains mixed case content")
}

func (s *SearchRepositoryE2ETestSuite) TestNoResults() {
	// Create a file with some content
	s.createTestFile("no_results.txt", "This file contains some content")

	// Perform a search that should yield no results
	var result string
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:     "*",
		SearchTerm:   "nonexistent",
		ContextLines: 0,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	s.Equal(SearchRepoNoResultsMessage, result, "Expected SearchRepoNoResultsMessage for nonexistent search term")
}

func (s *SearchRepositoryE2ETestSuite) TestRespectIgnoreFiles() {
	// Create .sideignore file
	s.createTestFile(".sideignore", "ignored_gen_file.txt\n*.genignore")

	// Create .gitignore file
	s.createTestFile(".gitignore", "ignored_git_file.txt\n*.gitignore")

	// Create files that should be ignored
	s.createTestFile("ignored_gen_file.txt", "This file should be ignored by genflow")
	s.createTestFile("ignored_git_file.txt", "This file should be ignored by git")
	s.createTestFile("test.genignore", "This file should be ignored by genflow")
	s.createTestFile("test.gitignore", "This file should be ignored by git")

	// Create a file that should not be ignored
	s.createTestFile("normal_file.txt", "This file should not be ignored")

	// Perform a search that would normally find content in all files
	var result string
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:     "*",
		SearchTerm:   "file",
		ContextLines: 0,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	// Verify the results
	s.Contains(result, "normal_file.txt")
	s.NotContains(result, "ignored_gen_file.txt")
	s.NotContains(result, "ignored_git_file.txt")
	s.NotContains(result, "test.genignore")
	s.NotContains(result, "test.gitignore")
}

func (s *SearchRepositoryE2ETestSuite) TestFallbackToCaseInsensitiveUponNoResults() {
	// Test automatic fallback to case-insensitive search
	s.createTestFile("fallback.txt", "This is a FALLBACK test file")

	// Perform a search that should automatically fall back to case-insensitive
	var result string
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:     "*",
		SearchTerm:   "fallback",
		ContextLines: 0,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	// Verify that it found the result despite the case mismatch
	s.Contains(result, "fallback.txt")
	s.Contains(result, "This is a FALLBACK test file")
}
func (s *SearchRepositoryE2ETestSuite) TestFallbackToFixedStringSearch() {
	// Create a test file that contains the literal search term
	testFilename := "test_invalid_regex.txt"
	testContent := "This is a test containing something ( inside it."
	s.createTestFile(testFilename, testContent)

	// Use an invalid regex as the search term to trigger fallback to fixed string search
	input := SearchRepositoryInput{
		PathGlob:        testFilename,
		SearchTerm:      "something (",
		ContextLines:    2,
		CaseInsensitive: false,
		FixedStrings:    false,
	}

	result, err := s.executeSearchRepository(input)
	s.Require().NoError(err, "Search should not error on fallback")
	s.Require().NotContains(result, "regex parse error", "Output should not contain regex parse error")
	s.Require().Contains(result, "This is a test containing something (", "Output should contain the matching line")
}

func (s *SearchRepositoryE2ETestSuite) TestFallbackToFixedStringSearch_NoMatches() {
	// Create a test file that does NOT contain the search term
	testFilename := "test_no_match.txt"
	testContent := "This is a test with no matching content."
	s.createTestFile(testFilename, testContent)

	// Use an invalid regex as the search term which also doesn't match any content
	input := SearchRepositoryInput{
		PathGlob:        testFilename,
		SearchTerm:      "something (",
		ContextLines:    2,
		CaseInsensitive: false,
		FixedStrings:    false,
	}

	result, err := s.executeSearchRepository(input)
	s.Require().NoError(err, "Search should not error even when no matches are found")
	// Expecting no results message, which is defined in SearchRepoNoResultsMessage
	s.Require().Equal(SearchRepoNoResultsMessage, result, "Output should indicate no results found")
}

func (s *SearchRepositoryE2ETestSuite) TestSearchWithSpecialCharacters() {
	// Test searching for terms with various shell metacharacters

	// Test with parentheses
	s.createTestFile("test_parens.go", "func NewActionContext() {\n\treturn nil\n}")
	result, err := s.executeSearchRepository(SearchRepositoryInput{
		PathGlob:     "*.go",
		SearchTerm:   "NewActionContext(",
		ContextLines: 1,
	})
	s.Require().NoError(err)
	s.Contains(result, "test_parens.go")
	s.Contains(result, "NewActionContext(")

	// Test with double quotes
	s.ResetWorkflowEnvironment()
	s.createTestFile("test_quotes.go", "func \"test\" example() {\n\treturn\n}")
	result, err = s.executeSearchRepository(SearchRepositoryInput{
		PathGlob:     "*.go",
		SearchTerm:   "func \"test\"",
		ContextLines: 1,
	})
	s.Require().NoError(err)
	s.Contains(result, "test_quotes.go")
	s.Contains(result, "func \"test\"")

	// Test with single quotes
	s.ResetWorkflowEnvironment()
	s.createTestFile("test_single_quotes.txt", "This is a 'quoted' string")
	result, err = s.executeSearchRepository(SearchRepositoryInput{
		PathGlob:     "*.txt",
		SearchTerm:   "'quoted'",
		ContextLines: 0,
	})
	s.Require().NoError(err)
	s.Contains(result, "test_single_quotes.txt")
	s.Contains(result, "'quoted'")

	// Test with backticks
	s.ResetWorkflowEnvironment()
	s.createTestFile("test_backticks.md", "Use `command` to run it")
	result, err = s.executeSearchRepository(SearchRepositoryInput{
		PathGlob:     "*.md",
		SearchTerm:   "`command`",
		ContextLines: 0,
	})
	s.Require().NoError(err)
	s.Contains(result, "test_backticks.md")
	s.Contains(result, "`command`")

	// Test with semicolon and ampersand
	s.ResetWorkflowEnvironment()
	s.createTestFile("test_special.sh", "echo 'hello'; echo 'world' &")
	result, err = s.executeSearchRepository(SearchRepositoryInput{
		PathGlob:     "*.sh",
		SearchTerm:   "echo 'world' &",
		ContextLines: 0,
	})
	s.Require().NoError(err)
	s.Contains(result, "test_special.sh")
	s.Contains(result, "echo 'world' &")

	// Test with pipe and dollar sign
	s.ResetWorkflowEnvironment()
	s.createTestFile("test_pipe_dollar.sh", "cat file | grep $VAR")
	result, err = s.executeSearchRepository(SearchRepositoryInput{
		PathGlob:     "*.sh",
		SearchTerm:   "grep $VAR",
		ContextLines: 0,
	})
	s.Require().NoError(err)
	s.Contains(result, "test_pipe_dollar.sh")
	s.Contains(result, "grep $VAR")

	// Test with backslashes
	s.ResetWorkflowEnvironment()
	s.createTestFile("test_backslash.txt", "Path: C:\\Users\\test\\file.txt")
	result, err = s.executeSearchRepository(SearchRepositoryInput{
		PathGlob:     "*.txt",
		SearchTerm:   "C:\\Users\\test",
		ContextLines: 0,
	})
	s.Require().NoError(err)
	s.Contains(result, "test_backslash.txt")
	s.Contains(result, "C:\\Users\\test")

	// Test with terms starting with double dashes
	s.ResetWorkflowEnvironment()
	s.createTestFile("test_double_dash.go", "// --verbose flag enables detailed output\nfunc main() {\n\tfmt.Println(\"--help\")\n}")
	result, err = s.executeSearchRepository(SearchRepositoryInput{
		PathGlob:     "*.go",
		SearchTerm:   "--verbose",
		ContextLines: 0,
	})
	s.Require().NoError(err)
	s.Contains(result, "test_double_dash.go")
	s.Contains(result, "--verbose")
}

func (s *SearchRepositoryE2ETestSuite) TestGlobPatternsRespectGitignore() {
	// Create .gitignore file
	s.createTestFile(".gitignore", "ignored_dir/\n*.ignored")

	// Create directories
	err := os.MkdirAll(filepath.Join(s.dir, "ignored_dir"), 0755)
	s.Require().NoError(err)
	err = os.MkdirAll(filepath.Join(s.dir, "normal_dir"), 0755)
	s.Require().NoError(err)

	// Create files that should be ignored by .gitignore
	s.createTestFile("ignored_dir/test_file.txt", "This file should be ignored by git")
	s.createTestFile("test.ignored", "This file should be ignored by git")

	// Create files that should not be ignored
	s.createTestFile("normal_dir/test_file.txt", "This file should not be ignored")
	s.createTestFile("test.txt", "This file should not be ignored")

	// Test with glob pattern that would match both ignored and non-ignored files
	var result string
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:     "*/test_file.txt",
		SearchTerm:   "file",
		ContextLines: 0,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	// Verify that only non-ignored files are found
	s.Contains(result, "normal_dir/test_file.txt")
	s.NotContains(result, "ignored_dir/test_file.txt")

	// Test with glob pattern that matches ignored file extension
	s.ResetWorkflowEnvironment()
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:     "*.ignored",
		SearchTerm:   "file",
		ContextLines: 0,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	// Should return no results because .ignored files are in .gitignore
	s.Equal("No files matched the path glob *.ignored - please try a different path glob", result)
}

func (s *SearchRepositoryE2ETestSuite) TestGlobPatternsRespectSideignore() {
	// Create .sideignore file
	s.createTestFile(".sideignore", "temp_dir/\n*.temp")

	// Create directories
	err := os.MkdirAll(filepath.Join(s.dir, "temp_dir"), 0755)
	s.Require().NoError(err)
	err = os.MkdirAll(filepath.Join(s.dir, "src_dir"), 0755)
	s.Require().NoError(err)

	// Create files that should be ignored by .sideignore
	s.createTestFile("temp_dir/build_file.txt", "This file should be ignored by sideignore")
	s.createTestFile("cache.temp", "This file should be ignored by sideignore")

	// Create files that should not be ignored
	s.createTestFile("src_dir/build_file.txt", "This file should not be ignored")
	s.createTestFile("cache.txt", "This file should not be ignored")

	// Test with glob pattern that would match both ignored and non-ignored files
	var result string
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:     "*/build_file.txt",
		SearchTerm:   "file",
		ContextLines: 0,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	// Verify that only non-ignored files are found
	s.Contains(result, "src_dir/build_file.txt")
	s.NotContains(result, "temp_dir/build_file.txt")

	// Test with glob pattern that matches ignored file extension
	s.ResetWorkflowEnvironment()
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:     "*.temp",
		SearchTerm:   "file",
		ContextLines: 0,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	// Should return no results because .temp files are in .sideignore
	s.Equal("No files matched the path glob *.temp - please try a different path glob", result)
}

func (s *SearchRepositoryE2ETestSuite) TestManualGlobFilteringBasicFunctionality() {
	// Create directories
	err := os.MkdirAll(filepath.Join(s.dir, "src"), 0755)
	s.Require().NoError(err)
	err = os.MkdirAll(filepath.Join(s.dir, "docs"), 0755)
	s.Require().NoError(err)

	// Create files with same search term but different extensions
	s.createTestFile("src/main.go", "This is a Go source file with function")
	s.createTestFile("src/utils.go", "This is another Go file with function")
	s.createTestFile("docs/readme.txt", "This is documentation with function")
	s.createTestFile("config.json", "This is config with function")

	// Test glob pattern that should only match .go files
	var result string
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:     "*.go",
		SearchTerm:   "function",
		ContextLines: 0,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	// Verify that only .go files are found
	s.Contains(result, "src/main.go")
	s.Contains(result, "src/utils.go")
	s.NotContains(result, "docs/readme.txt")
	s.NotContains(result, "config.json")

	// Test glob pattern that should only match files in src directory
	s.ResetWorkflowEnvironment()
	s.env.ExecuteWorkflow(s.wrapperWorkflow, s.envContainer, SearchRepositoryInput{
		PathGlob:     "src/*",
		SearchTerm:   "function",
		ContextLines: 0,
	})
	s.Require().NoError(s.env.GetWorkflowResult(&result))

	// Verify that only files in src directory are found
	s.Contains(result, "src/main.go")
	s.Contains(result, "src/utils.go")
	s.NotContains(result, "docs/readme.txt")
	s.NotContains(result, "config.json")
}
