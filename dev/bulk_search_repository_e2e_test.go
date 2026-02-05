package dev

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/env"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/suite"
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
	s.env.RegisterActivity(env.EnvRunCommandActivity)
	s.env.RegisterActivity(GetSymbolsActivity)

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
