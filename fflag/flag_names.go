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
