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
const InfoNeeds = "info-needs"
const DisableContextCodeVisibilityCheck = "disable-context-code-visibility-check"
