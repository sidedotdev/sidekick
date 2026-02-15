# Branch Comparison: `side/add-image-history-tool` (current) vs `side/image-kv-storage-integration`

## Architecture: KV Storage Indirection vs Direct Data URL

This is the single most consequential difference between the two branches.

### Current branch (`add-image-history-tool`)
- Introduces a **KV storage indirection layer**: the `ReadImageActivity` in `persisted_ai/` reads the image, stores the data URL in KV under a flow-prefixed key, and returns the key.
- Chat history stores `kv:<key>` references instead of actual data URLs.
- The `Hydrate()` method in `llm2/chat_history.go` resolves `kv:` prefixed URLs by loading data from KV at hydration time.
- The `handleToolCall` function in `dev/handle_tool_call.go` calls the persisted_ai activity via Temporal, then constructs `ToolResultContent` with `kv:` image URLs.
- `ToolCallResponseInfo` carries `ToolResultContent []llm2.ContentBlock`.

### Other branch (`image-kv-storage-integration`)
- **No KV storage layer at all**. The `ReadImageActivity` lives in `dev/` and returns the raw data URL string directly.
- Chat history stores the **full data URL inline** in `ImageDataURL string` on `ToolCallResponseInfo`.
- No `kv:` URL scheme, no hydration resolution, no `KvImagePrefix` constant.
- The `readImageTool` handling is done inline in `authorEditBlocks` via a `customHandlers` map passed to `handleToolCalls`, rather than in the central `handleToolCall` switch.
- `ToolCallResponseInfo` carries `ImageDataURL string` instead of `ToolResultContent`.

### Tradeoffs

| Aspect | Current (KV indirection) | Other (direct data URL) |
|---|---|---|
| **Serialized history size** | Small (`kv:` keys ~40 chars) | Large (full base64 data URLs, potentially MBs per image) |
| **Persistence robustness** | Images survive history serialization/deserialization without bloating JSON | History JSON contains full image data, may hit Temporal payload limits |
| **Complexity** | More moving parts: KV store, hydration, key management | Simpler: no extra storage layer |
| **Activity registration** | Needs `ReadImageActivities` struct + worker registration | Plain function activity, lighter registration |
| **Cleanup** | KV keys are flow-prefixed, enabling future cleanup by prefix | No cleanup needed since data is inline |
| **Replay safety** | KV read during hydration is a Temporal activity concern; `kv:` refs are deterministic | Data URL is returned from activity directly, clean Temporal replay |

**Verdict**: The KV approach is better for production (avoids bloating serialized chat history and Temporal payloads), but adds complexity. The direct approach is simpler and more self-contained.

---

## Tool Registration: Always vs Provider-Gated

### Current branch
- `readImageTool` is **always appended** to the tools list in `buildAuthorEditBlockInput`, regardless of provider/model.

### Other branch
- Adds a `supportsImageToolResults()` function that checks provider/model compatibility.
- Only appends `readImageTool` when the provider supports multimodal tool results (Anthropic: yes, OpenAI: yes, Google: only Gemini 3+).
- Includes tests for this gating logic (`TestSupportsImageToolResults`, `TestBuildAuthorEditBlockInput_ReadImageToolPresence`).

**Verdict**: The other branch is more defensive — it avoids offering a tool that would fail at the provider level for unsupported models (e.g., Gemini 2.x). This is clearly preferable.

---

## Tool Call Handling Location

### Current branch
- Adds `case readImageTool.Name:` to the **central `handleToolCall` switch** in `dev/handle_tool_call.go`.
- This means `read_image` is available in all agent loops that use `handleToolCall` (requirements, plan, edit code).

### Other branch
- Handles `read_image` via a **`customHandlers` map** passed only in `authorEditBlocks` in `dev/edit_code.go`.
- The tool is scoped exclusively to the edit code loop.

**Verdict**: The other branch's approach is more intentional — `read_image` only makes sense in the edit code loop. The current branch's central registration means it could be invoked in requirements/plan building where it wasn't intended.

---

## Google Provider: Gemini 3 Detection vs Uniform Fallback

### Current branch
- Adds `isGemini3OrLater()` to detect Gemini 3 models and use multimodal `FunctionResponsePart` with `InlineData`/`FileData` inside the function response.
- For Gemini 2.x, appends images as fallback user content parts outside the function response.
- Passes `model` string through to `googleFromLlm2Messages`.

### Other branch
- **Removes** Gemini 3 special casing entirely.
- All models use the same approach: function response text + image parts appended as regular inline content alongside the function response.
- `googleFromLlm2Messages` no longer takes a `model` parameter.

**Verdict**: The other branch is simpler and avoids speculative Gemini 3 API support (which may not be stable). The current branch's Gemini 3 path uses `FunctionResponsePart` and `FunctionResponseBlob` types that may not be well-tested in production.

---

## Error Handling in `addToolCallResponse`

### Current branch
- `addToolCallResponse` returns nothing (void). Errors in tool result content construction are silent.

### Other branch
- `addToolCallResponse` returns `error`. All callers handle or log errors.
- Includes proper error return when trying to use image data with non-llm2 chat history format.

**Verdict**: The other branch has better error propagation.

---

## MIME Type Detection

### Current branch (`persisted_ai/read_image_tool.go`)
- Uses `http.DetectContentType(raw)` — content-sniffing based on file bytes.
- Rejects non-image files at runtime.

### Other branch (`dev/read_image_tool.go`)
- Uses a **whitelist of file extensions** (`.png`, `.jpg`, `.jpeg`, `.gif`).
- Determines MIME from extension, not content.
- Rejects unsupported extensions before reading the file.

**Verdict**: Content-sniffing is more robust (handles misnamed files), but the extension whitelist is more predictable and fails faster. The extension approach also explicitly documents supported formats in the tool description.

---

## Test Coverage

### Current branch
- Tests in `persisted_ai/read_image_tool_test.go` for path validation and KV storage.
- Tests in `llm2/llm2_chat_history_test.go` for `kv:` URL hydration.
- No tests for `supportsImageToolResults` or tool presence gating (doesn't exist).

### Other branch
- Tests in `dev/read_image_tool_test.go` for path validation and direct data URL return.
- Tests in `dev/edit_code_test.go` for `supportsImageToolResults` and `buildAuthorEditBlockInput` tool presence.
- No `kv:` hydration tests (doesn't exist).

**Verdict**: The other branch has more end-to-end coverage of the integration points (tool gating, build input). The current branch tests infrastructure that may not be needed (KV hydration).

---

## OpenAI Chat Completions: Image Detail Level

### Current branch
- Uses `Detail: "auto"` for tool result images.

### Other branch
- Uses `Detail: "high"` for tool result images.

**Verdict**: `"high"` is better for code/screenshot analysis where detail matters. Minor difference.

---

## OpenAI Responses Provider: Multimodal Output

### Current branch
- Converts tool result images to `input_image` items in the Responses API output.
- Has `hasImageContent()` and `toolResultToResponsesOutputParts()` helpers.

### Other branch
- **Removes** all Responses-specific multimodal tool result handling.
- Falls back to text-only function call output.

**Verdict**: The current branch is more feature-complete for the Responses API path, but the other branch avoids complexity for an edge case.

---

## Summary Recommendation

| If you prefer... | Choose... |
|---|---|
| Production readiness (smaller payloads, KV-backed images) | Current branch (`add-image-history-tool`) |
| Simplicity, fewer moving parts, easier to review | Other branch (`image-kv-storage-integration`) |
| Defensive tool registration (provider-gated) | Other branch |
| Scoped tool availability (edit loop only) | Other branch |
| Better error handling | Other branch |
| Multimodal Responses API support | Current branch |
| Gemini 3 multimodal function response | Current branch (speculative) |

**Overall**: The other branch (`image-kv-storage-integration`) makes more pragmatic choices — it's simpler, better scoped, has provider gating, and better error handling. The current branch's KV indirection is architecturally cleaner for production but adds significant complexity. A hybrid approach (KV storage from current + provider gating and scoped handling from other) would be ideal.