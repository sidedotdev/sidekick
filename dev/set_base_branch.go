package dev

import (
	"fmt"
	"sidekick/common"
	"sidekick/llm"

	"github.com/invopop/jsonschema"
)

type SetBaseBranchParams struct {
	BranchName string `json:"branchName" jsonschema:"description=The name of the branch to use as the new base branch for diff computation and merge targeting"`
}

var setBaseBranchTool = llm.Tool{
	Name:        "set_base_branch",
	Description: "Changes the base branch used for diff computation and merge targeting. Use this when the worktree has been merged from a different branch than the original base, causing unrelated changes to appear in the diff. The most recent update to the base branch (whether from this tool or the user changing the target branch in the UI) takes effect.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&SetBaseBranchParams{}),
}

func SetBaseBranch(dCtx DevContext, params SetBaseBranchParams) (string, error) {
	oldBranch := dCtx.ExecContext.GlobalState.GetStringValue(common.KeyCurrentTargetBranch)
	if oldBranch == "" {
		oldBranch = "(unset)"
	}

	dCtx.ExecContext.GlobalState.SetValue(common.KeyCurrentTargetBranch, params.BranchName)

	return fmt.Sprintf("Base branch changed from %q to %q. Diffs and merge targeting will now use %q.", oldBranch, params.BranchName, params.BranchName), nil
}
