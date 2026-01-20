# Phase 5: Frontend Support for New Format

## Goal

Update frontend code to support both legacy `llm.ChatMessage` format and new `llm2.Message` format in `actionParams` and `actionResult`. Since `PersistFlowAction` will now contain refs instead of full chat history, the frontend must handle hydration and display of both formats.

## Tasks

### 1. Update TypeScript Types

**Directory:** `frontend/src/`

Add TypeScript types for:
- `MessageRef` - the reference format used in new workflows
- `Llm2Message` / `Llm2ContentBlock` - the llm2 message structure
- Union types that accept either legacy or new format

### 2. Add Hydration API Endpoint

**File:** `api/` (backend)

Add endpoint to hydrate message refs:
- Accepts list of `MessageRef` objects
- Returns hydrated `llm2.Message` content from KV storage
- Used by frontend when displaying flow actions with refs

### 3. Update Flow Action Display Components

**Directory:** `frontend/src/components/`

Update components that display `actionParams` and `actionResult`:
- Detect whether data contains legacy messages or refs
- For refs, call hydration endpoint before display
- Handle both `llm.ChatMessage` and `llm2.Message` response formats

### 4. Update Chat Message Rendering

**Directory:** `frontend/src/components/`

Update chat message rendering to handle `llm2.Message` structure:
- `llm2.Message` has `content` as array of `ContentBlock` instead of single string
- Support rendering different block types (text, tool_use, tool_result, etc.)
- Maintain backward compatibility with legacy string content

### 5. Handle Loading States

Add appropriate loading states when hydrating refs, since this requires an async API call that legacy format did not need.