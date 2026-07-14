---
intent_links:
  - intent: "#verification"
    code:
      - llm2/integration_test_helpers_test.go:requireOpenAIIntegrationCredentials
      - llm2/openai_responses_provider_test.go:TestOpenAIResponsesProvider_Integration
      - llm2/openai_responses_provider_test.go:TestOpenAIResponsesProvider_ReasoningContinuation
      - llm2/openai_responses_provider_test.go:TestOpenAIResponsesProvider_ToolResultImageIntegration
---
# OpenAI

Note: This intent is incomplete.
Major aspects that are omitted here but exist in the code should have their 
underlying intent be retained as-is.

## Default Models

The default summarization and small model is gpt-5.4-nano.

## Verification

Integration tests allow authentication by any means (API or subscription, any
secret manager), to support developers with a wide variety of setups.