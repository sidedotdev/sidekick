package fflag

// CheckGoBuild enables the go compile check (`go test -c`) when validating Go
// files after edits. It is expensive, especially on remote environments where
// it runs over the network, so it defaults to off.
const CheckGoBuild = "check-go-build"

const InfoNeeds = "info-needs"
const DisableContextCodeVisibilityCheck = "disable-context-code-visibility-check"
const InitialRepoSummary = "initial-repo-summary"
const ManageHistoryWithContextMarkers = "manage-history-with-context-markers"
const DisableDoneCoding = "disable-done-coding"
const ContextGatheringForget = "context-gathering-forget"

// LlmCommitMessages has an LLM commit coding work and write commit and merge
// messages, instead of committing with messages derived from task and step
// titles. It defaults to on.
const LlmCommitMessages = "llm-commit-messages"
