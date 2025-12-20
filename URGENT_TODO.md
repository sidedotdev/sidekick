# URGENT: User-Adjusted Requirements for TrackedToolChatWithHistory

The user has explicitly adjusted the requirements. These steps MUST be followed exactly.

## Steps

- [x] **Step 1**: Create `common.MessageResponse` interface and implement it for both `llm.ChatMessageResponse` and `llm2.MessageResponse`
  - Interface needs methods to get fields present in both response types (Id, StopReason, Usage, GetMessage/Output, etc.)
  - DONE: Added interface to common/message.go, implemented for common.ChatMessageResponse and llm2.MessageResponse
  
- [ ] **Step 2**: Adjust `TrackedToolChatWithHistory` to return `common.MessageResponse` instead of `*llm.ChatMessageResponse`
  - Also need to update `ExecuteChatStream` to return `common.MessageResponse` in addition to the history

- [ ] **Step 3**: Adjust callers of `TrackedToolChatWithHistory` to use `common.MessageResponse`
  - `generateDevRequirements` in `dev/build_dev_requirements.go`
  - `generateDevPlan` in `dev/build_dev_plan.go`
  - `authorEditBlocks` in `dev/edit_code.go`

- [ ] **Step 4**: Adjust callers of callers of `TrackedToolChatWithHistory` (propagate interface up the call chain)
  - `buildDevRequirementsIteration` uses result of `generateDevRequirements`
  - `buildDevPlanIteration` uses result of `generateDevPlan`
  - etc.

- [ ] **Step 5**: Adjust `ForceToolCallWithTrackOptionsV2` and `ForceToolCallWithTrackOptions` to return `common.MessageResponse`

- [ ] **Step 6**: Adjust `generateBranchNameCandidates` to use the new interface

## Notes
- `common.MessageResponse` should have methods that work for both legacy (`llm.ChatMessageResponse`) and new (`llm2.MessageResponse`) types
- Key fields needed: message content, tool calls, stop reason, usage stats
- This removes the need for complex type conversions between llm and llm2 types

**DELETE THIS FILE WHEN ALL STEPS ARE COMPLETE**