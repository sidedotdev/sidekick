package fflag

/*
Enabling the CheckEdits flag does the following:

1. Each edit is checked in various ways after the initial edit block application
2. If the check fails, the edit is backed out
3. If the check succeeds, the edit is confirmed by staging it

Note: when CheckEdits is disabled, staging of changes is also disabled, these go
hand-in-hand.
*/
const CheckEdits = "check-edits"

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
