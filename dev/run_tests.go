package dev

import (
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
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

// RunTests runs the provided test commands.
func RunTests(dCtx DevContext, commandsToRun []common.CommandConfig) (TestResult, error) {
	if len(commandsToRun) == 0 {
		return TestResult{}, fmt.Errorf("no test commands configured")
	}
	for _, testCommand := range commandsToRun {
		if testCommand.Command == "" {
			return TestResult{}, fmt.Errorf("test command is empty")
		}
	}

	resultsCh := workflow.NewChannel(dCtx)
	actionParams := map[string]any{
		"testCommands": commandsToRun,
	}

	actionCtx := dCtx.NewActionContext("run_tests")
	actionCtx.ActionParams = actionParams
	testResults, err := Track(actionCtx, func(flowAction *domain.FlowAction) ([]TestResult, error) {
		// execute all test commands in parallel
		for _, testCommand := range commandsToRun {
			// Capture testCommand for the goroutine
			testCommand := testCommand

			workflow.Go(dCtx, func(ctx workflow.Context) {
				localActionCtx := actionCtx.WithContext(ctx)
				runSingleTest(localActionCtx, testCommand.WorkingDir, testCommand.Command, *dCtx.EnvContainer, resultsCh)
			})
		}

		// Wait for all goroutines to finish and collect the results
		var results []TestResult
		for i := 0; i < len(commandsToRun); i++ {
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

func runSingleTest(actionCtx DevActionContext, workingDir string, fullCommand string, envContainer env.EnvContainer, resultsCh workflow.Channel) {
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
	err := flow_action.PerformWithUserRetry(actionCtx.FlowActionContext(), env.EnvRunCommandActivity, &runTestOutput, runTestInput)
	if err != nil {
		resultsCh.Send(actionCtx, fmt.Errorf("failed to run test command '%s': %v", fullCommand, err))
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

	resultsCh.Send(actionCtx, testResult)
}

func SummarizeTestOutput(dCtx DevContext, testOutput string) (string, error) {
	prompt := fmt.Sprintf(`
Summarize the following test run results, maintain all important details that a
software engineer may use to fix the underlyng issue. Leave out extraneous
details to make the output significantly shorter, but maintain all salient
details, including but not limited to:

1. The test command that failed in the test run
2. The names/descriptions of the tests that failed (don't repeat the parent test name if there are many similar ones, group together instead)
3. Details of the assertions that failed, if applicable (don't repeat if there are many similar assertions that failed, group these together instead)
4. All relevant file paths and line numbers (don't repeat if there are many similar file paths and line numbers, group together instead)
5. Any relevant logs or error messages (don't repeat if there are many similar logs/errors, group together instead)
6. Any relevant stack traces or partial stack traces (don't repeat in full if there are many similar ones, group together instead)

Choosing the most important and representative failures, copy and paste the test
output for those failures verbatim, though you can cut out large sections that
are especially verbose or irrelevant/noisy. (important: only retain 1 or 2
verbatim samples when it's repetitive). Don't repeat the same details across
both the summary and verbatim sample(s), to keep the summary very short overall.
If a verbatim sample is representative, just mention all the tests with similar
failures for that group of failures.

Present the summary directly without referring to the word "summary" or a
preamble like "Here is the summary". Do not provide any guidance on how to fix
the issue, or any ideas for where the problem might be: simply summarize the
test output, no more, no less.

Rough template to follow (though adjust to the formatting of actual test outputs
and feel free to adjust labels as needed):

`+"```"+`example
Test Command: <command>

## Test Failures

### <group1> (name of test failure group)
- <parent> (class/parent test or similar, if any)
	- <child1> (sub-test name)
	- <child2> (sub-test name)
	- <child3> (sub-test name)
		- Assertion/log/error specific to child3 only, if any
	- <child4> (sub-test name)

Common (failing assertions/logs/errors/etc for this group):
	- <assertion1> at <relative file path>:<line> (common assertion)
	- <log1> (verbatim log common across )
	- <error1> (common error)

(Verbatim-ish output of representative test failure) <parent.childN>:
<sample1> (include a common stack trace here if any, removing anything noisy)

### Test2
...

### <group3> (name of test failure group)
...

etc

## (Summary of other relevant logs/etc)

(Anything here, only include if necessary)
`+"```"+`

Here is the test result output to summarize:

%s
`, testOutput)

	modelConfig := dCtx.GetModelConfig(common.SummarizationKey, 0, "small")

	toolChatOptions := llm.ToolChatOptions{
		Secrets: *dCtx.Secrets,
		Params: llm.PromptToToolChatParams(prompt, llm.ChatControlParams{
			ModelConfig: modelConfig,
		}),
	}
	chatResponse, err := TrackedToolChat(dCtx, "summarize_tests", toolChatOptions)
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
