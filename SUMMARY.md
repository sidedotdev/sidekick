# Analysis: Parallel Tool Call Message Representation Across LLM Providers

## Question

When there are multiple parallel tool calls (e.g., two `read_image` calls, or a `read_image` + `bulk_search_repository`), each tool result gets appended as a **separate** `llm2.Message` (each with `Role: user` and a single `ContentBlockTypeToolResult` block). Would the final API representation sent to each LLM provider differ if all tool results were instead combined into a **single** `llm2.Message` with multiple `ContentBlockTypeToolResult` content blocks?

## How Separate Messages Are Created

In `handleToolCalls`, `appendToolCallResult` is called once per tool result in a loop:

- **Normal tool results**: `addToolCallResponse` wraps each `ToolResultBlock` in its own `llm2.Message{Role: RoleUser, Content: [{Type: ContentBlockTypeToolResult, ToolResult: &trb}]}` → calls `AppendChatHistory` → calls `AppendMessage` activity → produces one `MessageRef` per tool result.
- **Image tool results**: `AppendRef` is called with the ref from `ReadImageActivity`, which already persisted a single `MessageRef` with one `ContentBlockTypeToolResult` block.

So with 3 parallel tool calls, the chat history ends up with **3 consecutive user messages**, each containing exactly one tool result block.

## Provider-by-Provider Analysis

### Anthropic (`messagesToAnthropicParams`)

**Result: No difference.**

The function **merges consecutive same-role messages** by tracking `currentRole` and accumulating `currentBlocks`. It only flushes when the role changes. Since all tool result messages have `RoleUser`, consecutive user messages have their content blocks accumulated into a single `anthropic.NewUserMessage(block1, block2, block3)`. The API sees one user message with three tool result blocks regardless of how the internal `[]Message` is structured.

### OpenAI Chat Completions (`messagesToChatCompletionParams`)

**Result: No difference.**

This function processes each message independently and does NOT merge consecutive messages. However, each `ContentBlockTypeToolResult` in a user message becomes its own `OfTool` message (a top-level `ChatCompletionMessageParamUnion`), not part of a user message. Tool results are extracted from their enclosing message and emitted as independent tool messages. Whether the source is 3 messages × 1 block or 1 message × 3 blocks, the output is the same: 3 separate `OfTool` entries in the result slice.

For image content within tool results: `toolResultImageParts` extracts images and appends them as a follow-up `OfUser` message. This happens per tool result block regardless of message grouping.

### OpenAI Responses (`messageToResponsesInput`)

**Result: No difference.**

The function iterates all messages and all content blocks in a flat loop. Each `ContentBlockTypeToolResult` produces one `ResponseInputItemParamOfFunctionCallOutput`. Message boundaries are completely irrelevant — the output is a flat `[]ResponseInputItemUnionParam` regardless of how blocks are grouped into messages.

### Google (`googleFromLlm2Messages`)

**Result: No difference.**

Like Anthropic, Google **merges consecutive same-role parts**. It tracks `currentRole` and accumulates `currentParts`, flushing into a `genai.Content` only when the role changes. Tool result blocks force `currentRole = "user"` if not already set, and all results are user role. So consecutive user messages with tool results produce the same merged `genai.Content{Role: "user", Parts: [...all tool result parts...]}` regardless of internal message boundaries.

## Conclusion

**All four providers produce identical final API representations** whether tool results come as separate consecutive user messages or as a single combined user message. This is because:

1. **Anthropic and Google** explicitly merge consecutive same-role messages during conversion.
2. **OpenAI Chat Completions** extracts tool results into top-level tool messages regardless of their enclosing message structure.
3. **OpenAI Responses** flattens everything into a single item list, ignoring message boundaries entirely.

The current approach of creating separate `MessageRef`/`Message` per tool result is therefore **correct and produces no behavioral difference** compared to grouping them. The separate-message approach is actually preferable because:
- It naturally maps to the `MessageRef` + KV storage model (one ref per append operation).
- It avoids needing to coordinate across parallel goroutines to build a single combined message.
- It keeps `appendToolCallResult` simple — each call is independent.