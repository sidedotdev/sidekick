package dev

import (
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/llm"
	"strings"

	"go.temporal.io/sdk/workflow"
)

var maxTestOutputSize = min(4000, defaultMaxChatHistoryLength/4)

// TestResult holds a detailed information about test run
type TestResult struct {
	TestsPassed bool   `json:"testsPassed"`
	Output      string `json:"output"`
}

// RunTests runs tests configured via side.toml in a repo dir
func RunTests(dCtx DevContext) (TestResult, error) {
	repoConfig := dCtx.RepoConfig
	if len(repoConfig.TestCommands) == 0 {
		return TestResult{}, fmt.Errorf("no test commands configured")
	}
	for _, testCommand := range repoConfig.TestCommands {
		if testCommand.Command == "" {
			return TestResult{}, fmt.Errorf("test command is empty")
		}
	}

	resultsCh := workflow.NewChannel(dCtx)
	actionParams := map[string]any{
		"testCommands": repoConfig.TestCommands,
	}

	actionCtx := dCtx.NewActionContext("Run Tests")
	actionCtx.ActionParams = actionParams
	testResults, err := Track(actionCtx, func(flowAction domain.FlowAction) ([]TestResult, error) {
		// execute all test commands in parallel
		for _, testCommand := range repoConfig.TestCommands {
			// Capture testCommand for the goroutine
			testCommand := testCommand

			workflow.Go(dCtx, func(ctx workflow.Context) {
				runSingleTest(ctx, testCommand.WorkingDir, testCommand.Command, *dCtx.EnvContainer, resultsCh)
			})
		}

		// Wait for all goroutines to finish and collect the results
		var results []TestResult
		for i := 0; i < len(repoConfig.TestCommands); i++ {
			var value interface{}
			if ok := resultsCh.Receive(dCtx, &value); !ok {
				return nil, fmt.Errorf("failed to receive test result, channel closed early")
			}
			switch v := value.(type) {
			case TestResult:
				results = append(results, v)
			case error:
				return nil, fmt.Errorf("error running test command: %v", v)
			default:
				panic(fmt.Sprintf("unexpected test result type %T", v))
			}
		}

		return results, nil
	})

	if err != nil {
		return TestResult{}, err
	}

	for i, result := range testResults {
		if len(result.Output) > maxTestOutputSize {
			summarizedOutput, err := SummarizeTestOutput(dCtx, result.Output)
			if err != nil {
				return TestResult{}, fmt.Errorf("failed to summarize test output: %v", err)
			}
			testResults[i].Output = summarizedOutput
		}
	}

	return combineTestResults(testResults), nil
}

func combineTestResults(results []TestResult) TestResult {
	allPassed := true
	var combinedOutput strings.Builder

	for _, result := range results {
		allPassed = allPassed && result.TestsPassed
		combinedOutput.WriteString(result.Output)
		combinedOutput.WriteString("\n")
	}

	return TestResult{
		TestsPassed: allPassed,
		Output:      combinedOutput.String(),
	}
}

func runSingleTest(ctx workflow.Context, workingDir string, fullCommand string, envContainer env.EnvContainer, resultsCh workflow.Channel) {
	runTestInput := env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "/usr/bin/env",
		Args:               []string{"sh", "-c", fullCommand},
	}
	if workingDir != "" {
		runTestInput.RelativeWorkingDir = workingDir
	}
	var runTestOutput env.EnvRunCommandActivityOutput
	err := workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, runTestInput).Get(ctx, &runTestOutput)
	if err != nil {
		resultsCh.Send(ctx, fmt.Errorf("failed to run test command '%s': %v", fullCommand, err))
		return
	}

	// Check for test success or failure based on the error returned
	testsPassed := runTestOutput.ExitStatus == 0
	var output string
	if testsPassed {
		// only a summary of test output for a passing test
		output = "Test Command: " + fullCommand + "\nTest Result: Passed"
	} else {
		// full output when the test fails
		output = "Test Command: " + fullCommand + "\nTest Result: Failed\nTest stderr: " + runTestOutput.Stderr + "\nTest stdout: " + runTestOutput.Stdout
	}

	testResult := TestResult{
		TestsPassed: testsPassed,
		Output:      output,
	}

	resultsCh.Send(ctx, testResult)
}

func SummarizeTestOutput(dCtx DevContext, testOutput string) (string, error) {
	prompt := fmt.Sprintf(`
Summarize the following test run results in a manner that a software engineer
may use to fix the issue. Leave out extraneous details to make the output
significantly shorter, but maintain all salient details, including but not
limited to:

1. The test command that failed in the test run
2. The names/descriptions of the tests that failed
3. Details of the assertions that failed, if applicable
4. All relevant file paths and line numbers
5. Any relevant logs or error messages
6. Any relevant stack traces or partial stack traces

Present the summary directly without referring to the word "summary" or a
preamble like "Here is the summary".

`+

		/*
		   The summary should be formed from significant substrings of the test run output,
		   rather than reformulating the output entirely, both those substrings should be
		   constrained to the most relevant ones.
		*/

		`Here is the test result output to summarize:

%s
`, testOutput)

	provider, modelConfig, isDefault := dCtx.GetToolChatConfig(common.SummarizationKey, 0)

	model := modelConfig.Model
	if isDefault {
		model = provider.SmallModel() // summarization is typically an easier task
	}

	toolChatOptions := llm.ToolChatOptions{
		Secrets: *dCtx.Secrets,
		Params: llm.PromptToToolChatParams(prompt, llm.ChatControlParams{
			Provider: provider,
			Model:    model,
		}),
	}
	chatResponse, err := TrackedToolChat(dCtx, "Summarize Tests", toolChatOptions)
	if err != nil {
		return "", err
	}
	summarizedOutput := chatResponse.Content

	if len(summarizedOutput) > maxTestOutputSize {
		messageFormat := "\n...\nNote: the summarized test output was too long, so we truncated the last %d characters.\n\n"
		keptLength := maxTestOutputSize - len(messageFormat) - 5
		message := fmt.Sprintf(messageFormat, len(summarizedOutput)-keptLength)
		summarizedOutput = message + summarizedOutput[:keptLength]
	}

	return summarizedOutput, nil
}
