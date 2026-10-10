---
intent_links:
  - intent: "#default-models"
    code:
      - common/llm_types.go:SmallModels
      - flow_action/exec_context_test.go:TestGetModelConfig_SmallFallback_ReasoningSupported
      - flow_action/exec_context_test.go:TestGetModelConfig_SmallFallback_ReasoningNotSupported
  - intent: "#prompt-caching"
    code:
      - llm2/openai_prompt_cache_test.go:TestOpenAIProviders_PromptCacheKey
      - persisted_ai/llm2_activities_test.go:TestStreamActivity_PromptCacheKey
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

The default summarization and small model is gpt-5.6-luna.

## Prompt Caching

OpenAI prompt caching is implicit: sufficiently long shared prompt prefixes are
cached automatically, without markers. To improve hit rates, Sidekick sends
`prompt_cache_key` set to the flow id, so requests within a flow are routed
together. This is only sent to native OpenAI providers, never to
OpenAI-compatible proxies. Explicit cache breakpoints and cache retention
settings are not yet used.

## Verification

Integration tests allow authentication by any means (API or subscription, any
secret manager), to support developers with a wide variety of setups.
