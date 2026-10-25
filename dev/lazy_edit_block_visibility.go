package dev

import (
	"fmt"
	"sidekick/coding/tree_sitter"
	"sidekick/flow_action"
	"sidekick/persisted_ai"
	"sidekick/utils"
	"sort"

	"go.temporal.io/sdk/workflow"
)

// lazyEditBlockVisibilityEnabled reports whether visibility filtering is
// resolved on demand (only for edit blocks whose old lines match several
// places) rather than extracted from the chat history for every response.
func lazyEditBlockVisibilityEnabled(ctx workflow.Context) bool {
	return workflow.GetVersion(ctx, "lazy-edit-block-visibility", workflow.DefaultVersion, 1) >= 1
}

// applyEditBlocksWithLazyVisibility applies edit blocks without visibility
// data first, then retries only the blocks that failed on ambiguous matches
// with visibility restricted to what the LLM saw in visibleChatHistory.
func applyEditBlocksWithLazyVisibility(dCtx DevContext, visibleChatHistory *persisted_ai.ChatHistoryContainer, editBlocks []EditBlock) (applyEditBlocksResult, error) {
	checkDisabled := true
	opts := applyEditBlocksOptions{ContextVisibilityCheckDisabled: &checkDisabled}
	reports, err := validateAndApplyEditBlocks(dCtx, editBlocks, opts)
	if err != nil {
		return applyEditBlocksResult{}, err
	}

	kept, ambiguousBlocks := splitAmbiguousReports(reports)
	if len(ambiguousBlocks) > 0 {
		filePaths := utils.Unique(utils.Map(ambiguousBlocks, func(block EditBlock) string { return block.FilePath }))
		sort.Strings(filePaths)
		visibleCodeBlocks, err := extractVisibleCodeBlocksForFiles(dCtx, visibleChatHistory, filePaths)
		if err != nil {
			return applyEditBlocksResult{}, err
		}

		// Retries go one block at a time: each block's visibility ranges are
		// rebased here across every edit applied so far, including earlier
		// retries, rather than relying on the activity's in-batch shifting.
		reports = kept
		for _, block := range ambiguousBlocks {
			retryBlock := visibilityForRetry(block, visibleCodeBlocks, reports)
			retryReports, err := validateAndApplyEditBlocks(dCtx, []EditBlock{retryBlock}, opts)
			if err != nil {
				return applyEditBlocksResult{}, err
			}
			reports = append(reports, retryReports...)
		}
		sort.SliceStable(reports, func(i, j int) bool {
			return reports[i].SequenceNumbers()[0] < reports[j].SequenceNumbers()[0]
		})
	}

	return summarizeApplyEditBlocks(dCtx, reports)
}

// splitAmbiguousReports separates unapplied reports caused by ambiguous
// matches, whose edit blocks are worth retrying with visibility filtering.
func splitAmbiguousReports(reports []ApplyEditBlockReport) (kept []ApplyEditBlockReport, ambiguousBlocks []EditBlock) {
	for _, report := range reports {
		if report.AmbiguousMatch && !report.DidApply {
			ambiguousBlocks = append(ambiguousBlocks, report.OriginalEditBlocks...)
		} else {
			kept = append(kept, report)
		}
	}
	return kept, ambiguousBlocks
}

func extractVisibleCodeBlocksForFiles(dCtx DevContext, visibleChatHistory *persisted_ai.ChatHistoryContainer, filePaths []string) ([]tree_sitter.CodeBlock, error) {
	var cha *persisted_ai.ChatHistoryActivities
	var output persisted_ai.ExtractVisibleCodeBlocksOutput
	extractCtx := dCtx.ExecContext
	extractCtx.Context = utils.WithStorageActivityOptions(dCtx)
	err := flow_action.PerformActivityWithUserRetry(
		extractCtx,
		"Extract visible code blocks",
		cha.ExtractVisibleCodeBlocksForFiles,
		&output,
		persisted_ai.ExtractVisibleCodeBlocksInput{ChatHistory: visibleChatHistory, FilePaths: filePaths},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to extract visible code blocks: %w", err)
	}
	return output.CodeBlocks, nil
}

// visibilityForRetry annotates an edit block with the code the LLM saw for its
// file. Those ranges describe the file before any edits from this response, so
// they are rebased across the already-applied reports, in application order.
func visibilityForRetry(block EditBlock, visibleCodeBlocks []tree_sitter.CodeBlock, appliedReports []ApplyEditBlockReport) EditBlock {
	block.VisibleCodeBlocks = utils.Filter(visibleCodeBlocks, func(cb tree_sitter.CodeBlock) bool {
		return cb.FilePath == block.FilePath
	})
	fileRanges := codeBlocksToMergedFileRanges(block.FilePath, visibleCodeBlocks)

	for _, report := range appliedReports {
		if !report.DidApply || report.FilePath() != block.FilePath {
			continue
		}
		createdFile := false
		for _, applied := range report.OriginalEditBlocks {
			if applied.EditType == "create" {
				createdFile = true
				fileRanges = append(fileRanges, FileRange{
					FilePath:  block.FilePath,
					StartLine: 1,
					EndLine:   1 + len(applied.NewLines),
				})
			}
		}
		if createdFile {
			// the diff of a created file is relative to nothing, so there is
			// no earlier coordinate space to rebase from
			continue
		}
		// FinalDiff includes autofix changes when edits were staged
		diff := report.FinalDiff
		if diff == "" {
			diff = report.InitialDiff
		}
		fileRanges = shiftFileRangesByDiff(fileRanges, block.FilePath, diff)
	}

	// a non-nil (possibly empty) slice is what turns visibility filtering on
	block.VisibleFileRanges = mergedRangesForFile(block.FilePath, fileRanges)
	if block.VisibleFileRanges == nil {
		block.VisibleFileRanges = []FileRange{}
	}
	return block
}
