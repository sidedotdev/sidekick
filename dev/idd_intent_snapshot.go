package dev

import (
	"fmt"
	"strings"

	"sidekick/env"

	"go.temporal.io/sdk/workflow"
)

type IddIntentSnapshotInput struct {
	EnvContainer env.EnvContainer
	Base         string
	OriginalBase string
}

type IddIntentSnapshotResult struct {
	Tree         string
	OriginalTree string
	Diff         string
	Full         bool
}

func captureIddIntentSnapshot(dCtx DevContext, input IddIntentSnapshotInput) (IddIntentSnapshotResult, error) {
	var output env.EnvRunCommandActivityOutput
	if err := workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, iddIntentSnapshotCommand(input)).Get(dCtx, &output); err != nil {
		return IddIntentSnapshotResult{}, err
	}
	return parseIddIntentSnapshot(output)
}

func iddIntentSnapshotCommand(input IddIntentSnapshotInput) env.EnvRunCommandActivityInput {
	return env.EnvRunCommandActivityInput{
		EnvContainer:       input.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "sh",
		Args: []string{"-c", `
set -eu
original=$(git rev-parse --verify "$2^{tree}") || {
	echo "original intent base unavailable: $2" >&2
	exit 1
}
mode=incremental
if [ -z "$1" ]; then
	base=$original
elif base=$(git rev-parse --verify "$1^{tree}" 2>/dev/null); then
	:
else
	base=$original
	mode=full
fi
index=$(mktemp)
trap 'rm -f "$index" "$index.lock"' EXIT
rm -f "$index"
export GIT_INDEX_FILE="$index"
git read-tree HEAD
if [ -d intent ] || [ -n "$(git ls-files -- intent)" ]; then
	git add -A -- intent
fi
tree=$(git write-tree)
printf '%s\n%s\n%s\n' "$tree" "$mode" "$original"
git diff --no-ext-diff --no-textconv "$base" "$tree" -- intent
printf '\nIDD_SNAPSHOT_COMPLETE\n'
`, "idd-intent-snapshot", input.Base, input.OriginalBase},
	}
}

func parseIddIntentSnapshot(output env.EnvRunCommandActivityOutput) (IddIntentSnapshotResult, error) {
	if output.ExitStatus != 0 {
		return IddIntentSnapshotResult{}, fmt.Errorf("intent snapshot failed (exit %d): %s", output.ExitStatus, output.Stderr)
	}
	tree, rest, ok := strings.Cut(output.Stdout, "\n")
	if !ok || strings.TrimSpace(tree) == "" {
		return IddIntentSnapshotResult{}, fmt.Errorf("intent snapshot returned no tree")
	}
	mode, rest, ok := strings.Cut(rest, "\n")
	if !ok || (mode != "full" && mode != "incremental") {
		return IddIntentSnapshotResult{}, fmt.Errorf("intent snapshot returned invalid mode %q", mode)
	}
	original, diff, ok := strings.Cut(rest, "\n")
	if !ok || strings.TrimSpace(original) == "" {
		return IddIntentSnapshotResult{}, fmt.Errorf("intent snapshot returned no original tree")
	}
	const trailer = "\nIDD_SNAPSHOT_COMPLETE\n"
	if !strings.HasSuffix(diff, trailer) {
		return IddIntentSnapshotResult{}, fmt.Errorf("intent snapshot output is incomplete or truncated")
	}
	return IddIntentSnapshotResult{
		Tree: tree, OriginalTree: original,
		Diff: strings.TrimSuffix(diff, trailer), Full: mode == "full",
	}, nil
}
