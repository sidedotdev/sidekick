package dev /* TODO move to coding/edit_block */

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sidekick/common"
	"sidekick/diffp"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/fflag"
	"slices"
	"sort"
	"strconv"
	"strings"

	"sidekick/coding/check"
	"sidekick/coding/git"
	"sidekick/coding/lsp"
	"sidekick/coding/tree_sitter"
	"sidekick/utils"

	"github.com/rs/zerolog/log"

	"go.temporal.io/sdk/workflow"
)

type DevActivities struct {
	*lsp.LSPActivities
}

type ApplyEditBlockReport struct {
	OriginalEditBlock EditBlock `json:"originalEditBlock"`

	DidApply bool   `json:"didApply"`
	Error    string `json:"error"`

	// TODO /gen/req replace this with a slice of AutofixExecution struct
	// values. each has a name, applied edits, failed edits, error and message.
	// should work for both lsp and command-based autofixes
	AutofixResult lsp.AutofixActivityOutput `json:"autofixResult"`
	AutofixError  string                    `json:"autofixError"`

	// TODO /gen/req replace with slice of CheckResult, which should include
	// check name
	CheckResult CheckResult `json:"checkResult"`

	/* InitialDiff records the diff before autofixes are applied (if any) */
	InitialDiff string `json:"initialDiff"`
	/* FinalDiff records the diff after autofixes are applied (if any) */
	FinalDiff string `json:"finalDiff"`
}

type ApplyEditBlockActivityInput struct {
	EnvContainer  env.EnvContainer
	EditBlocks    []EditBlock
	EnabledFlags  []string
	CheckCommands []common.CommandConfig
}

func (da *DevActivities) ApplyEditBlocks(ctx context.Context, input ApplyEditBlockActivityInput) ([]ApplyEditBlockReport, error) {
	baseDir := input.EnvContainer.Env.GetWorkingDirectory()
	var reports []ApplyEditBlockReport

	for i, block := range input.EditBlocks {
		var report ApplyEditBlockReport
		var err error

		switch block.EditType {
		case "create":
			report, err = ApplyCreateEditBlock(block, baseDir)
			// TODO pass in LSP client pre-initialized so we don't have to do it
			AutofixIfEditSucceeded(input.EnvContainer, &report)
		case "update":
			report, err = ApplyUpdateEditBlock(block, baseDir)
			AutofixIfEditSucceeded(input.EnvContainer, &report)
		case "append":
			report, err = ApplyAppendEditBlock(block, baseDir)
			// TODO pass in LSP client pre-initialized so we don't have to do it
			AutofixIfEditSucceeded(input.EnvContainer, &report)
		case "delete":
			report, err = ApplyDeleteEditBlock(block, baseDir)
		default:
			report = ApplyEditBlockReport{
				OriginalEditBlock: block,
				Error:             fmt.Sprintf("Unknown edit type: %s", block.EditType),
			}
		}

		if err != nil {
			reports = append(reports, report)
			continue
		}
		report.DidApply = true

		if report.Error == "" && slices.Contains(input.EnabledFlags, fflag.CheckEdits) {
			diff, diffErr := git.GitDiffActivity(context.Background(), input.EnvContainer, git.GitDiffParams{
				FilePaths: []string{filepath.Join(baseDir, block.FilePath)},
			})
			report.FinalDiff = diff

			checkResult, err := checkAndStageOrRestoreFile(input.EnvContainer, input.CheckCommands, block.FilePath, block.EditType != "create")
			report.CheckResult = checkResult
			if !checkResult.Success {
				report.DidApply = false
				hint := fixCheckHint(report)
				report.Error = fmt.Sprintf("Checks failed: %s\nHint: %s", checkResult.Message, hint)
			}
			if err != nil {
				report.Error = report.Error + fmt.Sprintf("\nFailure when checking/staging/restoring file: %v", err)
			}
			if diffErr != nil {
				report.Error = report.Error + fmt.Sprintf("\nFailure when getting git diff: %v", diffErr)
			}
		}

		reports = append(reports, report)

		if report.DidApply {
			/*
			* We use the visible file ranges to limit the scope of the edits
			* done, in case of similar code in the same file that were not
			* visible to the LLM. But these file ranges were originally
			* calculated based on the original file and are no longer valid for
			* later edit blocks, once any one edit block for a given file has
			* been applied. Thus, we need to update the visible file ranges for
			* all subsequent edit blocks for the same file.
			*
			* The final diff is used to determine the line edits that were
			* made, because this takes into account both the edit and any
			* autofixes that were applied.
			 */
			lineEdits := getLineEditsFromDiff(report.FinalDiff)
			updateVisibleFileRanges(input.EditBlocks[i:], block.FilePath, lineEdits)

			// Notify LSP server about the file changes
			err := notifyLSPServerOfFileSave(input.EnvContainer, block.FilePath)
			if err != nil {
				log.Warn().Err(err).Str("filePath", block.FilePath).Msg("Failed to notify LSP server of file change")
			}
		}
	}

	// TODO if more than one edit blocks failed checks and were restored, it's
	// possible that they would pass checks if both are applied. this could
	// happen even across files, since checks on specific files may depend on
	// the state of other files. so in this case, we should try to apply each
	// combination of failed edits to maximize the number of successful edits.

	// FIXME for now, let's at least apply all edits without checks, then check
	// them all

	return reports, nil
}

// notifyLSPServerOfFileSave notifies the LSP server about changes to a file
// by sending appropriate textDocument notifications based on server capabilities
func notifyLSPServerOfFileSave(envContainer env.EnvContainer, filePath string) error {
	baseDir := envContainer.Env.GetWorkingDirectory()
	absoluteFilePath := filepath.Join(baseDir, filePath)

	// Determine language from file extension
	language := getLanguageFromFilePath(filePath)
	if language == "" {
		// Skip LSP notifications for files without recognized language
		return nil
	}

	// Create LSP activities instance
	lspa := lsp.LSPActivities{
		LSPClientProvider: func(languageName string) lsp.LSPClient {
			return &lsp.Jsonrpc2LSPClient{
				LanguageName: languageName,
			}
		},
		InitializedClients: map[string]lsp.LSPClient{},
	}

	ctx := context.Background()
	client, err := lspa.findOrInitClient(ctx, baseDir, language)
	if err != nil {
		return fmt.Errorf("failed to get LSP client: %w", err)
	}

	// Get server capabilities to determine which notifications to send
	capabilities := client.GetServerCapabilities()

	// Check textDocumentSync capabilities
	var syncOptions *lsp.TextDocumentSyncOptions
	switch sync := capabilities.TextDocumentSync.(type) {
	case *lsp.TextDocumentSyncOptions:
		syncOptions = sync
	case lsp.TextDocumentSyncKind:
		// no save notification if save sync option not explicitly set
		return nil
	}

	documentURI := "file://" + absoluteFilePath

	// Try didSave notification first (most common and simple)
	if syncOptions != nil && syncOptions.Save != nil {
		// Server supports save notifications
		params := lsp.DidSaveTextDocumentParams{
			TextDocument: lsp.TextDocumentIdentifier{
				URI: documentURI,
			},
		}

		// Check if server wants file content included
		if saveOpts, ok := syncOptions.Save.(*lsp.SaveOptions); ok && saveOpts != nil && saveOpts.IncludeText != nil && *saveOpts.IncludeText {
			// Read file content
			content, err := os.ReadFile(absoluteFilePath)
			if err != nil {
				return fmt.Errorf("failed to read file content for didSave: %w", err)
			}
			text := string(content)
			params.Text = &text
		}

		err = client.TextDocumentDidSave(ctx, params)
		if err != nil {
			return fmt.Errorf("failed to send didSave notification: %w", err)
		}
		return nil
	}

	// Fallback to didOpen/didClose cycle if save is not supported but openClose is
	if syncOptions != nil && syncOptions.OpenClose != nil && *syncOptions.OpenClose {
		// Read file content
		content, err := os.ReadFile(absoluteFilePath)
		if err != nil {
			return fmt.Errorf("failed to read file content for didOpen: %w", err)
		}

		// Send didClose first (in case file was already open)
		closeParams := lsp.DidCloseTextDocumentParams{
			TextDocument: lsp.TextDocumentIdentifier{
				URI: documentURI,
			},
		}
		_ = client.TextDocumentDidClose(ctx, closeParams) // Ignore errors for close

		// Send didOpen with updated content
		openParams := lsp.DidOpenTextDocumentParams{
			TextDocument: lsp.TextDocumentItem{
				URI:        documentURI,
				LanguageID: language,
				Version:    1,
				Text:       string(content),
			},
		}
		err = client.TextDocumentDidOpen(ctx, openParams)
		if err != nil {
			return fmt.Errorf("failed to send didOpen notification: %w", err)
		}
		return nil
	}

	// If neither save nor openClose is supported, we can't notify the server
	log.Debug().Str("filePath", filePath).Msg("LSP server does not support textDocument sync notifications")
	return nil
}

// getLanguageFromFilePath determines the language identifier from a file path
func getLanguageFromFilePath(filePath string) string {
	ext := filepath.Ext(filePath)
	switch ext {
	case ".go":
		return "go"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".py":
		return "python"
	case ".java":
		return "java"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	case ".c":
		return "c"
	case ".cs":
		return "csharp"
	case ".php":
		return "php"
	case ".rb":
		return "ruby"
	case ".rs":
		return "rust"
	case ".vue":
		return "vue"
	case ".html":
		return "html"
	case ".css":
		return "css"
	case ".json":
		return "json"
	case ".xml":
		return "xml"
	case ".yaml", ".yml":
		return "yaml"
	default:
		return ""
	}
}

type lineEdit struct {
	editStartLineNumber int
	numLinesAdded       int // removed represented with a negative number here
}

func updateVisibleFileRanges(editBlocks []EditBlock, filePath string, lineEdits []lineEdit) {
	for _, block := range editBlocks {
		if block.FilePath != filePath {
			continue
		}
		for i := range block.VisibleFileRanges {
			fileRange := &block.VisibleFileRanges[i]
			for _, lineEdit := range lineEdits {
				if fileRange.StartLine >= lineEdit.editStartLineNumber {
					fileRange.StartLine += lineEdit.numLinesAdded
				}
				if fileRange.EndLine >= lineEdit.editStartLineNumber {
					fileRange.EndLine += lineEdit.numLinesAdded
				}
			}
		}
	}
}

func fixCheckHint(report ApplyEditBlockReport) string {
	hint := ""

	hasBalanceIssues := false
	oldParens := countUnbalanced(report.OriginalEditBlock.OldLines, "(", ")")
	newParens := countUnbalanced(report.OriginalEditBlock.NewLines, "(", ")")
	if oldParens != newParens {
		hasBalanceIssues = true
		hint = hint + fmt.Sprintf("The net number of unbalanced parentheses should be the same in the new lines vs old lines. But there are %d unbalanced parentheses in the old lines and %d in the new lines.\n", oldParens, newParens)
	}

	oldBraces := countUnbalanced(report.OriginalEditBlock.OldLines, "{", "}")
	newBraces := countUnbalanced(report.OriginalEditBlock.NewLines, "{", "}")
	if oldBraces != newBraces {
		hasBalanceIssues = true
		hint = hint + fmt.Sprintf("The net number of unbalanced braces should be the same in the new lines vs old lines. But there are %d unbalanced braces in the old lines and %d in the new lines.\n", oldBraces, newBraces)
	}

	oldSquares := countUnbalanced(report.OriginalEditBlock.OldLines, "[", "]")
	newSquares := countUnbalanced(report.OriginalEditBlock.NewLines, "[", "]")
	if oldSquares != newSquares {
		hasBalanceIssues = true
		hint = hint + fmt.Sprintf("The net number of unbalanced square brackets should be the same in the new lines vs old lines. But there are %d unbalanced square brackets in the old lines and %d in the new lines.\n", oldSquares, newSquares)
	}

	if hasBalanceIssues {
		hint = hint + fmt.Sprintf("Balance all the parentheses, braces, and square brackets within the %s section - keep going until closing any delimiters opened. Do the same for the %s section.\n", search, replace)

		/*
			lastChar := report.OriginalEditBlock.OldLines[len(report.OriginalEditBlock.OldLines)-1]
			// TODO custom hint for case where closing delimiters show up first
			// early on without the corresponding opening delimiters in the old
			// lines
			if lastChar == "{" || lastChar == "(" || lastChar == "[" {
				hint = hint + fmt.Sprintf("Try to add more lines to close the last '%s' within the %s section to avoid the balancing problems.\n", lastChar, search)
			} else {
				hint = hint + fmt.Sprintf("Balance all the parentheses, braces, and square brackets within the %s section - keep going until closing any delimiters opened. Do the same for the %s section.\n", search, replace)
			}
		*/
	}

	if report.OriginalEditBlock.EditType == "update" && len(report.OriginalEditBlock.OldLines) <= 3 {
		hint = hint + "Make sure to add enough context in the old lines, more than just 2 or 3 lines, at least 5 if available.\n"
	}

	if hint == "" {
		if strings.Contains(report.CheckResult.Message, check.SyntaxError) {
			hint = "Ensure the replacement of old lines with new lines results in good syntax, and make sure to do something different than what failed.\n"
		} else {
			hint = "Just make sure to do something different than what failed.\n"
		}
	}

	return hint
}

func countUnbalanced(lines []string, openingDelimiter, closingDelimiter string) int {
	count := 0
	for _, line := range lines {
		count += strings.Count(line, openingDelimiter)
		count -= strings.Count(line, closingDelimiter)
	}
	return count
}

// Checks the file after applying the edit. If the checks fail, the file is
// restored, otherwise it is staged, so that future restores don't affect this
// change.
func checkAndStageOrRestoreFile(envContainer env.EnvContainer, checkCommands []common.CommandConfig, filePath string, isExistingFile bool) (CheckResult, error) {
	checkOutput, checkErr := check.CheckFileActivity(check.CheckFileActivityInput{
		EnvContainer:  envContainer,
		FilePath:      filePath,
		CheckCommands: checkCommands,
	})

	if checkErr != nil && checkOutput.Output == "" {
		return CheckResult{}, checkErr
	}

	// if checks failed, restore the file to its previous state
	if !checkOutput.AllPassed {
		checkResult := CheckResult{Success: false, Message: fmt.Sprintf("Checks failed:\n%s", checkOutput.Output)}
		if isExistingFile {
			// Restoring the file to its previous state in case of an error
			err := git.GitRestoreActivity(context.Background(), envContainer, filePath)
			if err != nil {
				return checkResult, fmt.Errorf("%v\nFailed to git restore: %v", checkErr, err)
			}
		} else {
			// If the file that failed checks was just created, we should remove it since git restore won't work
			err := os.Remove(filepath.Join(envContainer.Env.GetWorkingDirectory(), filePath))
			if err != nil {
				return checkResult, fmt.Errorf("%v\nFailed to remove file: %v", checkErr, err)
			}
		}
		return checkResult, nil
	}
	checkResult := CheckResult{Success: true, Message: ""}

	// if checks pass, git add the changes to the staging area so other restores
	// don't affect this change
	input := git.GitAddActivityInput{EnvContainer: envContainer, Path: filePath}
	err := git.GitAddActivity(context.Background(), input)
	if err != nil {
		return checkResult, fmt.Errorf("Failed to git add: %v", err)
	}

	return checkResult, nil
}

func ApplyCreateEditBlock(block EditBlock, baseDir string) (ApplyEditBlockReport, error) {
	report := ApplyEditBlockReport{
		OriginalEditBlock: block,
	}

	absoluteFilePath := filepath.Join(baseDir, block.FilePath)
	dirPath := filepath.Dir(absoluteFilePath)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		report.Error = fmt.Errorf("failed to create necessary directories %s: %v", dirPath, err).Error()
		return report, err
	}
	newContents := strings.TrimSuffix(strings.Join(block.NewLines, "\n"), "\n")
	if _, err := os.Stat(absoluteFilePath); err == nil {
		report.Error = fmt.Sprintf("file already exists: %s", absoluteFilePath)
		return report, errors.New(report.Error)
	} else if !os.IsNotExist(err) {
		report.Error = fmt.Sprintf("failed to check if file exists %s: %v", absoluteFilePath, err)
		return report, errors.New(report.Error)
	}
	err := os.WriteFile(absoluteFilePath, []byte(newContents), 0644)
	if err != nil {
		report.Error = fmt.Errorf("failed to create new file %s: %v", absoluteFilePath, err).Error()
		return report, err
	}
	report.InitialDiff = string(diffp.Diff("", []byte{}, block.FilePath, []byte(newContents)))

	return report, nil
}

func ApplyUpdateEditBlock(block EditBlock, baseDir string) (ApplyEditBlockReport, error) {
	report := ApplyEditBlockReport{
		OriginalEditBlock: block,
	}

	absoluteFilePath := filepath.Join(baseDir, block.FilePath)
	block.AbsoluteFilePath = absoluteFilePath // FIXME temporary hack so we get symbols for visible ranges stuff
	originalContents, err := os.ReadFile(absoluteFilePath)
	if err != nil {
		report.Error = fmt.Errorf("failed to read file %s: %v", absoluteFilePath, err).Error()
		return report, err
	}

	modifiedContents, err := getUpdatedContents(block, string(originalContents))
	if err != nil {
		report.Error = fmt.Errorf("Failed to apply edit block for file %s: %v", block.FilePath, err).Error()
		return report, err
	}

	err = os.WriteFile(absoluteFilePath, []byte(modifiedContents), 0644)
	if err != nil {
		report.Error = fmt.Errorf("Failed to write modified content to file %s: %v", absoluteFilePath, err).Error()
		return report, err
	}
	report.InitialDiff = string(diffp.Diff(block.FilePath, []byte(originalContents), block.FilePath, []byte(modifiedContents)))

	return report, nil
}

func ApplyAppendEditBlock(block EditBlock, baseDir string) (ApplyEditBlockReport, error) {
	report := ApplyEditBlockReport{
		OriginalEditBlock: block,
	}

	absoluteFilePath := filepath.Join(baseDir, block.FilePath)
	originalContents, err := os.ReadFile(absoluteFilePath)
	if err != nil {
		report.Error = fmt.Errorf("failed to read file %s: %v", absoluteFilePath, err).Error()
		return report, err
	}

	updatedContents := string(originalContents)
	if updatedContents != "" && !strings.HasSuffix(updatedContents, "\n") {
		updatedContents += "\n"
	}
	updatedContents += strings.Join(block.NewLines, "\n")
	err = os.WriteFile(absoluteFilePath, []byte(updatedContents), 0644)
	if err != nil {
		report.Error = fmt.Errorf("failed to append new lines to end of file %s: %v", absoluteFilePath, err).Error()
		return report, err
	}
	report.InitialDiff = string(diffp.Diff(block.FilePath, []byte(originalContents), block.FilePath, []byte(updatedContents)))

	return report, nil
}

// TODO /gen write tests for this
func validateAndApplyEditBlocks(dCtx DevContext, editBlocks []EditBlock) ([]ApplyEditBlockReport, error) {
	actionParams := map[string]interface{}{
		"editBlocks": editBlocks,
	}
	actionCtx := dCtx.NewActionContext("Apply Edit Blocks")
	actionCtx.ActionParams = actionParams
	reports, err := Track(actionCtx, func(flowAction domain.FlowAction) ([]ApplyEditBlockReport, error) {
		validEditBlocks, invalidReports := validateEditBlocks(editBlocks)

		enabledFlags := make([]string, 0)
		if fflag.IsEnabled(dCtx, fflag.CheckEdits) {
			enabledFlags = append(enabledFlags, fflag.CheckEdits)
		}

		applyEditBlockInput := ApplyEditBlockActivityInput{
			EnvContainer:  *dCtx.EnvContainer,
			EditBlocks:    validEditBlocks,
			EnabledFlags:  enabledFlags,
			CheckCommands: dCtx.RepoConfig.CheckCommands,
		}

		noRetryCtx := utils.NoRetryCtx(dCtx)
		var validReports []ApplyEditBlockReport
		err := workflow.ExecuteActivity(noRetryCtx, "DevActivities.ApplyEditBlocks", applyEditBlockInput).Get(noRetryCtx, &validReports)
		if err != nil {
			return nil, err
		}

		reports := append(validReports, invalidReports...)
		sort.Slice(reports, func(i, j int) bool {
			return reports[i].OriginalEditBlock.SequenceNumber < reports[j].OriginalEditBlock.SequenceNumber
		})
		return reports, nil
	})
	return reports, err
}

func validateEditBlocks(editBlocks []EditBlock) (validEditBlocks []EditBlock, invalidReports []ApplyEditBlockReport) {
	// for each edit block, check if it's valid by checking if the old lines are
	// present within any SourceCodeBlock in the chat history if not, add it to
	// the invalidReports. if valid, add it to the validEditBlocks
	for _, editBlock := range editBlocks {
		if len(editBlock.OldLines) == 0 {
			// creating a file or appending to a file doesn't require old lines. we
			// only validate old lines for now, so let's just consider these valid.
			validEditBlocks = append(validEditBlocks, editBlock)
			continue
		}

		anyAcceptableMatch := false
		finalClosestMatch := match{}
		for _, codeBlock := range editBlock.VisibleCodeBlocks {
			codeLines := regexp.MustCompile(`\r?\n`).Split(codeBlock.Code, -1)
			_, allMatches := FindAcceptableMatch(editBlock, codeLines, false)
			if len(allMatches) > 0 {
				anyAcceptableMatch = true
				break
			} else {
				// TODO we're ignoring the other matches here, but we should probably
				// show them in the error message maybe
				currentMatch, _ := FindClosestMatch(editBlock, codeLines, false)

				// NOTE this logic is taken from FindClosestMatch, we need to do
				// it ourselves because we are doing our own loop over code
				// blocks
				isNewBest := currentMatch.successfulMatch && currentMatch.score > finalClosestMatch.score
				isNewBest = isNewBest || (!finalClosestMatch.successfulMatch && currentMatch.successfulMatch)
				isNewBest = isNewBest || (!finalClosestMatch.successfulMatch && currentMatch.score > finalClosestMatch.score)
				if isNewBest {
					finalClosestMatch = currentMatch
				}
			}
		}

		if anyAcceptableMatch {
			validEditBlocks = append(validEditBlocks, editBlock)
		} else if finalClosestMatch.score > 0.2 {
			extra := ""
			if len(finalClosestMatch.failedToMatch) > 0 {
				extra = extra + fmt.Sprintf("\nFailed to match these lines:\n\n%s\n", firstLines(finalClosestMatch.failedToMatch, 5))
				if len(finalClosestMatch.foundInstead) > 0 {
					extra = extra + fmt.Sprintf("\nInstead, found these lines in the closest match in the code context:\n\n%s\n", firstLines(finalClosestMatch.foundInstead, 5))
				}
			}

			invalidReports = append(invalidReports, ApplyEditBlockReport{
				OriginalEditBlock: editBlock,
				DidApply:          false,
				Error: fmt.Sprintf(`
No code context found in the chat history that matches the edit
block's old lines, which I'll repeat here:

%s
%s

You must ensure the old lines are present in the code context by using one of
the tools before making an edit block.`, strings.Join(editBlock.OldLines, "\n"), extra),
			})
		} else {
			invalidReports = append(invalidReports, ApplyEditBlockReport{
				OriginalEditBlock: editBlock,
				DidApply:          false,
				Error:             "No code context found in the chat history that matches this edit block's old lines. You must ensure the old lines are present in the code context by using one of the tools before making an edit block.",
			})
		}
	}
	return validEditBlocks, invalidReports
}

// go should really get generics. or have a better way to do this.
func firstLines(strs []string, n int) string {
	if n > len(strs) {
		n = len(strs)
	}
	return strings.Join(strs[:n], "\n")
}

var multipleMatchesMessage = "Multiple matches found for the given edit block %s section, but expected only one match. Here are the matches with sufficient additional context from the current state of the file to disambiguate. Provide the edit block again with the specific full expanded context:\n\n%s"

func getUpdatedContents(block EditBlock, originalContents string) (string, error) {
	originalLines := strings.Split(originalContents, "\n")
	bestMatch, allMatches := FindAcceptableMatch(block, originalLines, true)

	if len(allMatches) > 1 {
		// TODO while all the matches have met the "threshold" for similarity,
		// we should check if the best one is way ahead of the others in terms
		// of its score. Eg if the best one has a perfect score, but the others
		// are just at the threshold, we might want to forgo the error message
		// and proceed. We'll need to see how this plays out in practice before
		// making such changes.

		unambiguousMatches := expandUntilUnambiguous(allMatches, originalLines)
		matchContents := strings.Join(
			utils.Map(unambiguousMatches, func(m match) string {
				return fmt.Sprintf("File: %s\nLines: %d-%d\n```\n%s\n```", block.FilePath, m.index+1, m.index+len(m.lines), strings.Join(m.lines, "\n"))
			}),
			"\n\n",
		)

		return "", fmt.Errorf(multipleMatchesMessage, search, matchContents)
	}

	if bestMatch.score == 0 {
		// FIXME we're ignoring the other matches here, but we should probably
		// show them in the error message maybe
		closestMatch, _ := FindClosestMatch(block, originalLines, true)
		extra := ""
		if len(closestMatch.failedToMatch) > 0 {
			extra = extra + fmt.Sprintf("\nFailed to match these lines:\n\n%s\n", firstLines(closestMatch.failedToMatch, 5))
			if len(closestMatch.foundInstead) > 0 {
				extra = extra + fmt.Sprintf("\nInstead, found these lines:\n\n%s\n", firstLines(closestMatch.foundInstead, 5))
			}
		}
		return "", fmt.Errorf("no good match found for the following edit block old lines:\n\n%s\n%s", strings.Join(block.OldLines, "\n"), extra)
	}

	startIndex := bestMatch.index
	endIndex := startIndex + len(bestMatch.lines) - 1

	// Create a new slice for the modified contents with enough capacity.
	newContents := make([]string, 0, len(originalLines)+len(block.NewLines)-len(bestMatch.lines))
	newContents = append(newContents, originalLines[:startIndex]...)
	newContents = append(newContents, block.NewLines...)
	newContents = append(newContents, originalLines[endIndex+1:]...)

	return strings.Join(newContents, "\n"), nil
}

const expandRate = 1

// Given a list of matches that are all "acceptable", we expand each of them
// until each is disambiguated from the others wrt the originalLines
func expandUntilUnambiguous(matches []match, originalLines []string) []match {
	for i := range matches {
		match := &matches[i]

		for !singleAcceptableMatch(match.lines, originalLines) {
			originalIndex := match.index
			match.index = max(0, originalIndex-expandRate)
			end := min(len(originalLines), originalIndex+len(match.lines)+expandRate)
			match.lines = originalLines[match.index:end]
		}

		// expand once more for good measure
		originalIndex := match.index
		match.index = max(0, originalIndex-expandRate)
		end := min(len(originalLines), originalIndex+len(match.lines)+expandRate)
		match.lines = originalLines[match.index:end]

		// TODO: uncomment below and adjust tests to handle this new logic
		// // expanding once more could have made it ambiguous again, so we need to
		// // do it again. this helps us expand enough so that the LLM can tell
		// // what's going on, since differences may be minute otherwise.
		// for !singleAcceptableMatch(match.lines, originalLines) {
		// 	originalIndex := match.index
		// 	match.index = max(0, originalIndex-expandRate)
		// 	end := min(len(originalLines), originalIndex+len(match.lines)+expandRate)
		// 	match.lines = originalLines[match.index:end]
		// }
	}
	return matches
}

func singleAcceptableMatch(lines []string, originalLines []string) bool {
	editBlock := EditBlock{OldLines: lines}
	_, allMatches := FindAcceptableMatch(editBlock, originalLines, true)
	if len(allMatches) == 1 {
		return true
	} else if len(allMatches) == 0 {
		panic("singleAcceptableMatch called with no matches")
	} else {
		return false
	}
}

func ApplyDeleteEditBlock(block EditBlock, baseDir string) (ApplyEditBlockReport, error) {
	filePath := filepath.Join(baseDir, block.FilePath)

	originalContents, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return ApplyEditBlockReport{
				OriginalEditBlock: block,
				Error:             fmt.Sprintf("File does not exist: %s", block.FilePath),
			}, nil
		} else {
			return ApplyEditBlockReport{
				OriginalEditBlock: block,
				Error:             fmt.Sprintf("Failed to read file %s: %v", block.FilePath, err),
			}, nil
		}
	}

	// Delete the file
	err = os.Remove(filePath)
	if err != nil {
		return ApplyEditBlockReport{
			OriginalEditBlock: block,
			Error:             fmt.Sprintf("Failed to delete file: %s", block.FilePath),
		}, nil
	}

	return ApplyEditBlockReport{
		OriginalEditBlock: block,
		InitialDiff:       string(diffp.Diff(block.FilePath, []byte(originalContents), block.FilePath, []byte{})),
	}, nil
}

var whitespaceOrEndingDelimiterPattern = regexp.MustCompile(`^\s*([})\]]*\s*)+$`)

func isWhitespaceOrEndingDelimiter(s string) bool {
	return whitespaceOrEndingDelimiterPattern.MatchString(s)
}

var whitespacePattern = regexp.MustCompile(`^\s*$`)

func isWhitespace(s string) bool {
	return whitespacePattern.MatchString(s)
}

// FIXME: only works for double-slash and # comment languages
// FIXME doesn't handle slash-star comments, fix this using tree-sitter comment ranges overlap
var commentPattern = regexp.MustCompile(`^\s*(\/\/|#).*$`)

func isComment(s string) bool {
	return commentPattern.MatchString(s)
}

func isWhitespaceOrComment(s string) bool {
	return isWhitespace(s) || isComment(s)
}

const similarityThreshold = 0.85 // controls similarity per line for fuzzy edit block matching
type match struct {
	index           int
	successfulMatch bool
	lines           []string
	score           float64
	highScoreRatio  float64
	failedToMatch   []string
	foundInstead    []string
}

const minimumAcceptableHighScoreRatio = 0.95

func FindAcceptableMatch(block EditBlock, originalLines []string, isOriginalLinesFromActualFile bool) (match, []match) {
	closestMatch, closestMatches := FindClosestMatch(block, originalLines, isOriginalLinesFromActualFile)
	if closestMatch.successfulMatch && closestMatch.highScoreRatio > minimumAcceptableHighScoreRatio {
		acceptableMatches := utils.Filter(closestMatches, func(m match) bool {
			return m.successfulMatch && m.highScoreRatio > minimumAcceptableHighScoreRatio
		})
		return closestMatch, acceptableMatches
	} else {
		return match{}, []match{}
	}
}

var minimumFileRangeVisibilityMargin = 5

func FindPotentialMatches(block EditBlock, originalLines []string, startingLineIndex int, isOriginalLinesFromActualFile bool) []match {
	var potentialMatches []match

	// Return no potential matches if there are no lines in the EditBlock
	if len(block.OldLines) == 0 {
		return potentialMatches
	}
	startingLine := block.OldLines[startingLineIndex]

	// Look for exact matches first
	for idx, line := range originalLines {
		if line == startingLine {
			potentialMatches = append(potentialMatches, match{index: idx, score: 1.0})
		}
	}

	// Then look for matches without leading/trailing whitespace
	if len(potentialMatches) == 0 {
		trimmedStartingLine := strings.TrimSpace(startingLine)
		for idx, line := range originalLines {
			if strings.TrimSpace(line) == trimmedStartingLine {
				potentialMatches = append(potentialMatches, match{index: idx, score: 0.999})
			}
		}
	}

	// If still no matches, use similarity score comparison
	if len(potentialMatches) == 0 {
		for idx, line := range originalLines {
			score := utils.StringSimilarity(line, startingLine)
			if score >= similarityThreshold {
				potentialMatches = append(potentialMatches, match{index: idx, score: score})
			}
		}
	}

	// Filter out matches that are not visible
	if block.VisibleFileRanges != nil && isOriginalLinesFromActualFile {
		mergedFileRanges := mergedRangesForFile(block.FilePath, block.VisibleFileRanges)

		// try to re-find the symbol and get new visible ranges if possible, in
		// case the symbols moved around
		// NOTE: this may not be necessary normally as we adjust file ranges on
		// applying edits, however this does handle when a human makes edits
		// in an "out-of-band" manner.
		// FIXME that said, we should be doing this logic outside of this
		// function, somewhere in the caller heirarchy
		if block.VisibleCodeBlocks != nil {
			for _, codeBlock := range block.VisibleCodeBlocks {
				if codeBlock.Symbol != "" && codeBlock.FilePath == block.FilePath {
					symbols, err := tree_sitter.GetSymbolDefinitions(block.AbsoluteFilePath, codeBlock.Symbol, 0)
					if err != nil {
						log.Warn().Err(err).Msg("Failed to get new symbol definitions when checking visibility")
						continue
					}
					for _, symbol := range symbols {
						mergedFileRanges = append(mergedFileRanges, FileRange{
							FilePath:  block.FilePath,
							StartLine: int(symbol.Range.StartPoint.Row + 1),
							EndLine:   int(symbol.Range.EndPoint.Row + 1),
						})
					}
				}
			}
			mergedFileRanges = mergedRangesForFile(block.FilePath, mergedFileRanges)
		}

		potentialMatches = utils.Filter(potentialMatches, func(m match) bool {
			for _, visibleFileRange := range mergedFileRanges {
				// we use a margin because lines move around from other edits blocks.
				// also, the main reason for this check is to ensure we have the
				// right match when there are multiple, thus we don't need to be
				// too strict. we already have checks ensuring the llm is no:
				// hallucinating by ensuring old lines are in the chat history.
				margin := (visibleFileRange.EndLine - visibleFileRange.StartLine) / 8
				margin = min(margin, minimumFileRangeVisibilityMargin)

				startIndex := visibleFileRange.StartLine - 1 - margin // 0-based index
				endIndex := visibleFileRange.EndLine - 1 + margin     // 0-based index
				if visibleFileRange.FilePath == block.FilePath && startIndex <= m.index && m.index+len(block.OldLines)-1 <= endIndex {
					return true
				}
			}
			return false
		})
	}

	return potentialMatches
}

// Find the first non-whitespace line in block's old lines
func calculateStartingLineIndex(lines []string) int {
	startingLineIndex := 0
	for startingLineIndex < len(lines)-1 && isWhitespaceOrEndingDelimiter(lines[startingLineIndex]) {
		startingLineIndex++
	}
	return startingLineIndex
}

func FindClosestMatch(block EditBlock, originalLines []string, isOriginalLinesFromActualFile bool) (match, []match) {
	startingLineIndex := calculateStartingLineIndex(block.OldLines)
	potentialMatches := FindPotentialMatches(block, originalLines, startingLineIndex, isOriginalLinesFromActualFile)

	// if no potential matches based on first line, go to the next line and try again
	skippedOldLines := 0
	if len(potentialMatches) == 0 && startingLineIndex+1 < len(block.OldLines) {
		skippedOldLines = 1
		startingLineIndex = startingLineIndex + 1 + calculateStartingLineIndex(block.OldLines[startingLineIndex+1:])
		potentialMatches = FindPotentialMatches(block, originalLines, startingLineIndex, isOriginalLinesFromActualFile)
	}

	var allMatches []match
	var bestMatch match
	for _, potentialMatch := range potentialMatches {
		totalScore := potentialMatch.score
		successfulMatch := true
		numHighScoreLines := 0
		numScoredLines := 0
		failedToMatch := []string{}
		foundInstead := []string{}

		offset := 0
		for offset < startingLineIndex && potentialMatch.index-offset-1 > 0 && potentialMatch.index-offset-1 < len(originalLines) && isWhitespaceOrEndingDelimiter(originalLines[potentialMatch.index-offset-1]) {
			offset++
		}

		adjustedIndex := potentialMatch.index - offset
		var matchedLines []string

		oldLinesOffset := 0
		originalLinesOffset := 0
		for i := 0; i+oldLinesOffset < len(block.OldLines); i++ {
			oldLine := block.OldLines[i+oldLinesOffset]

			//fmt.Printf("i:%v, oldLinesOffset: %v, originalLinesOffset: %v\n", i, oldLinesOffset, originalLinesOffset)
			// bounds check
			if adjustedIndex+i+originalLinesOffset >= len(originalLines) {
				if isWhitespaceOrComment(oldLine) {
					continue
				} else {
					// not sure the `successfulMatch` variable is needed as we have highScoreRatio
					successfulMatch = false
					break
				}
			}
			originalLine := originalLines[adjustedIndex+i+originalLinesOffset]

			//fmt.Printf("comparing:\n    orig: %s\n     old: %s\n", originalLine, oldLine)
			score := utils.StringSimilarity(originalLine, oldLine)
			//fmt.Printf("score: %v\n", score)

			// skip whitespace-only or comment-only lines when on one side only
			// TODO ignore changes or added comments on the end of an existing line
			if score < similarityThreshold {
				if (isWhitespaceOrComment(originalLine) && !isWhitespaceOrComment(oldLine)) ||
					(isWhitespace(originalLine) && !isWhitespace(oldLine)) {
					matchedLines = append(matchedLines, originalLine)
					//fmt.Println("offsetting original lines")
					originalLinesOffset += 1
					i--
					continue
				} else if (isWhitespaceOrComment(oldLine) && !isWhitespaceOrComment(originalLine)) ||
					(isWhitespace(oldLine) && !isWhitespace(originalLine)) {
					//fmt.Println("offsetting old lines")
					oldLinesOffset += 1
					i--
					continue
				} else if numScoredLines == 0 && skippedOldLines > 0 {
					// we haven't scored lines, but it's a potential match, so
					// that means startLineIndex is not at 0, so we'll offset
					// old lines further. note: this is not the same as setting
					// oldLinesOffset to startLineIndex, since we might find
					// matches with closing delimiters, which startLineIndex
					// does try to skip initially
					oldLinesOffset += 1
					i--
					continue
				} else {
					// TODO add some good test cases for below before bring in
					// this additional logic, if we think we still want it after
					// writing out those test cases
					/*
						nextOriginalLinesOffset := originalLinesOffset + 1
						if adjustedIndex+i+nextOriginalLinesOffset < len(originalLines) {
							nextOriginalLine := originalLines[adjustedIndex+i+nextOriginalLinesOffset]
							nextScore := utils.StringSimilarity(nextOriginalLine, oldLine)
							// skipping original line gives us a better match, so we
							// should skip, but account for this as a scored line
							// that got a bad score
							if nextScore >= similarityThreshold {
								numScoredLines++
								totalScore += score
								originalLinesOffset += 1
								i--
								continue
							}
						}

						nextOldLinesOffset := oldLinesOffset + 1
						if i+nextOldLinesOffset < len(block.OldLines) {
							nextOldLine := block.OldLines[i+nextOldLinesOffset]
							nextScore := utils.StringSimilarity(originalLine, nextOldLine)
							// skipping old line gives us a better match, so we
							// should skip, but account for this as a scored line
							// that got a bad score
							if nextScore >= similarityThreshold {
								numScoredLines++
								totalScore += score
								oldLinesOffset += 1
								i--
								continue
							}
						}
					*/

					// fmt.Println("offsetting nothing")
				}
			}

			matchedLines = append(matchedLines, originalLine)

			numScoredLines++
			if score > 0.925 {
				numHighScoreLines++
			} else {
				failedToMatch = append(failedToMatch, oldLine)
				foundInstead = append(foundInstead, originalLine)
			}
			totalScore += score
		}

		// Ensure the block match is of acceptable quality (95% of lines have a "high score")
		var highScoreRatio float64
		var avgScore float64
		if successfulMatch {
			//fmt.Println("successfulMatch")
			// different denominator so that we can use a consistent threshold
			// when skipping whitespace or comment lines
			highScoreRatio = float64(numHighScoreLines) / float64(numScoredLines+skippedOldLines)
			avgScore = float64(totalScore) / float64(numScoredLines+skippedOldLines)
			//fmt.Printf("highScoreRatio: %v\n", highScoreRatio)
			//fmt.Printf("avgScore: %v\n", avgScore)
		} else {
			//fmt.Println("NOT successfulMatch")
			// we don't score all lines when unsuccessful, so for parity across
			// multiple unsuccessful matches, we need to use a consistent
			// denominator
			highScoreRatio = float64(numHighScoreLines) / float64(len(block.OldLines))
			avgScore = float64(totalScore) / float64(len(block.OldLines))
		}
		//fmt.Printf("highScoreRatio: %v\n", highScoreRatio)
		//fmt.Printf("numScoredLines: %v\n", numScoredLines)
		thisMatch := match{index: adjustedIndex, successfulMatch: successfulMatch, highScoreRatio: highScoreRatio, score: avgScore, lines: matchedLines, failedToMatch: failedToMatch, foundInstead: foundInstead}

		allMatches = append(allMatches, thisMatch)

		isNewBest := successfulMatch && avgScore > bestMatch.score
		isNewBest = isNewBest || (!bestMatch.successfulMatch && successfulMatch)
		isNewBest = isNewBest || (!bestMatch.successfulMatch && avgScore > bestMatch.score)
		if isNewBest {
			bestMatch = thisMatch
		}
	}

	return bestMatch, allMatches
}

var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+),\d+ \+\d+,\d+ @@`)

// one lineEdit per consecutive run of "+" or "-" lines in the diff. each
// lineEdit has a start line and a number of lines added (removed lines is
// represented by a negative value). the start line corresponds to the first
// line number in the original file from each consecutive run of "+" or "-".
func getLineEditsFromDiff(diff string) []lineEdit {
	var lineEdits []lineEdit
	if diff == "" {
		return lineEdits
	}

	lines := strings.Split(diff, "\n")
	var hunks []string
	var currentHunk []string

	for _, line := range lines {
		if hunkHeaderPattern.MatchString(line) {
			if currentHunk != nil {
				hunks = append(hunks, strings.Join(currentHunk, "\n"))
			}
			currentHunk = []string{line}
		} else if currentHunk != nil {
			currentHunk = append(currentHunk, line)
		}
	}
	if len(currentHunk) > 0 {
		hunks = append(hunks, strings.Join(currentHunk, "\n"))
	}

	for _, hunk := range hunks {
		hunkLines := strings.Split(hunk, "\n")
		hunkHeader := hunkLines[0]
		hunkHeaderMatch := hunkHeaderPattern.FindStringSubmatch(hunkHeader)

		if len(hunkHeaderMatch) < 2 {
			panic("unexpected hunk header non-match after already matching. hunk: " + utils.PanicJSON(hunk))
		}
		startLine, err := strconv.Atoi(hunkHeaderMatch[1])
		if err != nil {
			panic(fmt.Sprintf("unexpected hunk header integer parsing after already matching regex: %v", err))
		}
		startLine-- // want 0-based index

		var currentEdit *lineEdit
		for i, line := range hunkLines[1:] {
			if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
				if currentEdit == nil {
					currentEdit = &lineEdit{editStartLineNumber: startLine + i}
				}
				if strings.HasPrefix(line, "+") {
					currentEdit.numLinesAdded++
				} else {
					currentEdit.numLinesAdded--
				}
			} else if currentEdit != nil {
				// only consecutive runs of "+" or "-" lines go in a single lineEdit
				lineEdits = append(lineEdits, *currentEdit)
				currentEdit = nil
			}
		}
		if currentEdit != nil {
			lineEdits = append(lineEdits, *currentEdit)
		}
	}

	return lineEdits
}

func AutofixIfEditSucceeded(envContainer env.EnvContainer, report *ApplyEditBlockReport) {
	if report.Error != "" {
		return
	}
	runAutofixCommands(envContainer, report)
	runAutofixViaLSP(envContainer, report)
}

// command-based autofix
func runAutofixCommands(envContainer env.EnvContainer, report *ApplyEditBlockReport) {
	var combinedOutput string
	// FIXME don't lookup config, instead have AutofixCommands as a field in the
	// ApplyEditBlockActivityInput and in this function's arguments etc
	repoConfig, err := GetRepoConfigActivity(envContainer)
	if err != nil {
		combinedOutput = fmt.Sprintf("failed to get coding config: %v", err)
	}
	autofixCommands := repoConfig.AutofixCommands
	for _, command := range autofixCommands {
		// allow the file path to be used in the command
		shellCommand := strings.ReplaceAll(command.Command, "{file}", report.OriginalEditBlock.FilePath)
		output, err := envContainer.Env.RunCommand(context.Background(), env.EnvRunCommandInput{
			RelativeWorkingDir: command.WorkingDir,
			Command:            "/usr/bin/env",
			Args:               []string{"sh", "-c", shellCommand},
		})
		if err != nil {
			// Append the error message to combinedOutput and continue with the next autofix command
			combinedOutput += fmt.Sprintf("failed to run autofix command '%s': %v\n", command.Command, err)
			continue
		}
		if output.ExitStatus != 0 {
			combinedOutput += fmt.Sprintf("autofix command: %s\n", command.Command)
			combinedOutput += output.Stdout + "\n" + output.Stderr
		}
	}
	report.AutofixError += combinedOutput
}

// LSP-based autofix
func runAutofixViaLSP(envContainer env.EnvContainer, report *ApplyEditBlockReport) {
	absoluteFilePath := filepath.Join(envContainer.Env.GetWorkingDirectory(), report.OriginalEditBlock.FilePath)
	lspa := lsp.LSPActivities{
		LSPClientProvider: func(languageName string) lsp.LSPClient {
			return &lsp.Jsonrpc2LSPClient{
				LanguageName: languageName,
			}
		},
		InitializedClients: map[string]lsp.LSPClient{},
	}
	ctx := context.Background()
	autofixInput := lsp.AutofixActivityInput{
		DocumentURI:  "file://" + absoluteFilePath,
		EnvContainer: envContainer,
	}
	result, autofixErr := lspa.AutofixActivity(ctx, autofixInput)
	if autofixErr != nil {
		report.AutofixError += fmt.Sprintf("\nLSP autofix error: %+v", autofixErr)
	}
	report.AutofixResult = result
}

// CheckResult defines the structure to hold results of checks performed during edit application.
type CheckResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}
