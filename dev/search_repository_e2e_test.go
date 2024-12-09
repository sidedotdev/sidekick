package dev

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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
	var err error
	s.dir, err = os.MkdirTemp("", "search-repository-test")
	s.Require().NoError(err)

	// Initialize git repository so that rg respects .gitignore files
	cmd := exec.Command("git", "init")
	cmd.Dir = s.dir
	err = cmd.Run()
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
