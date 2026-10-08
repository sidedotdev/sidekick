package fflag

import "go.temporal.io/sdk/workflow"

// retiredCheckEditsFlag gated edit checking and staging, which are now
// unconditional. It defaulted to enabled, so workflow executions that predate
// its removal are treated as having had it enabled.
const retiredCheckEditsFlag = "check-edits"

// EvalRetiredCheckEditsFlag re-issues the flag evaluation activity that
// workflow executions started before the check-edits flag was retired
// performed at each call site, keeping their command sequences deterministic
// on replay. Its result is irrelevant: the flag is always considered enabled.
func EvalRetiredCheckEditsFlag(ctx workflow.Context) {
	if workflow.GetVersion(ctx, "retire-check-edits-flag", workflow.DefaultVersion, 1) == workflow.DefaultVersion {
		IsEnabled(ctx, retiredCheckEditsFlag)
	}
}
