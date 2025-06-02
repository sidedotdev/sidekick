package dev

import "sidekick/domain"

// Run tests in a loop until they pass or the LLM says they don't have to pass
func EnsurePrerequisites(dCtx DevContext) error {
	return RunSubflowWithoutResult(dCtx, "prereqs", "Ensure Prerequisites", func(_ domain.Subflow) error {
		return ensurePrerequisitesSubflow(dCtx)
	})
}

// TODO: Implement EnsurePrerequisites
func ensurePrerequisitesSubflow(dCtx DevContext) error {
	return nil
}

// TODO: bring the following logic into EnsurePrerequisites
/*
	flowScope := flow.NewFlowActionScope(ctx, workspaceId, "Ensure Prerequisites")
	chatHistory := &[]llm.ChatMessage{}

	*chatHistory = append(*chatHistory, llm.ChatMessage{
		Role:    "user",
		Content: requirements,
	})

	firstTime := true
	for {
		testResult, err := RunTests(ctx, envContainer, flowScope)
		if err != nil {
			return fmt.Errorf("failed to run tests: %v", err)
		}

		if testResult.TestsPassed {
			break
		}

		if firstTime {
			firstTime = false
			response, err := GetHelpOrInput(ctx, []HelpOrInputRequest{
				{
					Content: testResult.Output,
				},
			})
			*chatHistory = append(*chatHistory, llm.ChatMessage{
				Role:    "user",
				Content: response,
			})
		} else {
			prompt := fmt.Sprintf(`Tests failed:

	%s

	We are ensuring that tests pass before we start development, otherwise
	development is liable to fail. Let the user know this. If the user says that
	they fixed the tests, tell them they haven't yet based on the above latest test
	failures and ask them again to resolve it. If the user says they want your help fixing the tests
	`, testResult.Output)
			functionCall, err := forceMakeChoice(ctx, envContainer, chatHistory, ChoiceInfo{
				Prompt:  prompt,
				Choices: []string{"already_fixed", "do_the_fixing", "other"},
			})
			if err != nil {
				return fmt.Errorf("failed to force getting human input: %v", err)
			}
		}
	}
*/
