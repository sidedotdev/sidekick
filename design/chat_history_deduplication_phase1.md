# Phase 1: ChatHistory Interface Foundation

## Goal

Establish the foundational interfaces and types that will enable chat history deduplication, without changing any existing behavior. At the end of this phase, the new types exist and are tested, but not yet used in workflow code.

## Tasks

### 1. Define Message Interface

Create a `Message` interface in `common/` that abstracts over `llm.ChatMessage` and `llm2.Message`. This interface will be removed in a future version (e.g., v0.8 or v0.9) when we clean up old workflow version code, giving users ample time to complete old-versioned workflows before replay support is dropped.

**File:** `common/message.go` (new file)

The interface should include:
- `GetRole() string` - returns the message role
- `GetContentString() string` - returns content as a single string
- `GetContentBlocks() []llm2.ContentBlock` - returns content as blocks

### 2. Implement Message Interface on Existing Types

Add methods to make both message types implement the interface:

**File:** `common/llm_types.go`
- Add `GetRole()`, `GetContentString()`, `GetContentBlocks()` methods to `ChatMessage`
- `GetContentBlocks()` should convert the single `Content` string to a `[]llm2.ContentBlock` with one text block

**File:** `llm2/types.go`
- Add `GetRole()`, `GetContentString()`, `GetContentBlocks()` methods to `Message`
- `GetContentString()` should concatenate text from all text-type content blocks

### 3. Define KeyValueStorage Interface

**File:** `srv/storage.go`

Add `KeyValueStorage` interface with:
- `MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error)`
- `MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error`

Embed this interface in the existing `Storage` interface.

### 4. Implement KeyValueStorage in SQLite

**File:** `srv/sqlite/kv_storage.go` (new file or add to existing)

Implement `MGet` and `MSet` methods on the SQLite storage type. May require a new table for key-value storage if one doesn't exist.

### 5. Define ChatHistory Interface

**File:** `common/chat_history.go` (new file)

Define minimal `ChatHistory` interface using the `common.Message` interface:
- `Append(msg Message)`
- `Len() int`
- `Get(index int) Message`
- `Messages() []Message`
- `Hydrate(ctx context.Context, storage KeyValueStorage) error`
- `Persist(ctx context.Context, storage KeyValueStorage) error`

### 6. Implement LegacyChatHistory

**File:** `common/chat_history.go`

Implement `LegacyChatHistory` that:
- Internally stores `[]llm2.Message` (converted from `llm.ChatMessage` on construction)
- `Hydrate()` and `Persist()` are no-ops (return nil)
- Provides constructor `NewLegacyChatHistoryFromLlmMessages(msgs []llm.ChatMessage) *LegacyChatHistory`

### 7. Implement ChatHistoryContainer

**File:** `common/chat_history.go`

Implement `ChatHistoryContainer` struct with:
- `History ChatHistory` field
- `UnmarshalJSON` that detects `[]llm.ChatMessage` format and wraps in `LegacyChatHistory`
- `MarshalJSON` that delegates to underlying implementation
- For `LegacyChatHistory`, marshal as `[]llm.ChatMessage` for backward compatibility

### 8. Unit Tests

**File:** `common/chat_history_test.go` (new file)

Test cases:
- `LegacyChatHistory` basic operations (Append, Len, Get, Messages)
- `ChatHistoryContainer` marshals `LegacyChatHistory` identically to `[]llm.ChatMessage`
- `ChatHistoryContainer` unmarshals `[]llm.ChatMessage` JSON into `LegacyChatHistory`
- Round-trip: marshal then unmarshal preserves content

**File:** `common/message_test.go` (new file)

Test cases:
- `llm.ChatMessage` implements `Message` interface correctly
- `llm2.Message` implements `Message` interface correctly
- `GetContentString()` and `GetContentBlocks()` conversions work correctly