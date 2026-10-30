package dev

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sidekick/coding/lsp"
	"sidekick/coding/tree_sitter"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/workflow"
)

func editBlockText(seq int, path string, oldLines, newLines []string) string {
	fence := strings.Repeat("~", 4)
	lines := []string{fence, fmt.Sprintf("edit_block:%d", seq), path, "<<<<<<< " + "SEARCH_EXACT"}
	lines = append(lines, oldLines...)
	lines = append(lines, strings.Repeat("=", 7))
	lines = append(lines, newLines...)
	lines = append(lines, ">>>>>>> "+"REPLACE_EXACT", fence)
	return strings.Join(lines, "\n")
}

func assistantTextResponse(text string) *llm2.MessageResponse {
	return &llm2.MessageResponse{
		StopReason: "stop",
		Output: llm2.Message{
			Role:    "assistant",
			Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: text}},
		},
	}
}

func (s *AuthorEditBlocksTestSuite) appendedText() string {
	var parts []string
	for _, msg := range s.appendedMessages {
		for _, block := range msg.Content {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// mockApplyEditBlockFlags covers the flags evaluated while applying edit
// blocks, which the suite's default flag mocks don't include.
func (s *AuthorEditBlocksTestSuite) mockApplyEditBlockFlags() {
	var ffa *fflag.FFlagActivities
	s.env.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.MatchedBy(func(params fflag.EvaluateFeatureFlagParams) bool {
		return params.FlagName == fflag.CheckGoBuild
	})).Return(false, nil).Maybe()
	s.env.OnGetVersion("done-required-protocol", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
}

func (s *AuthorEditBlocksTestSuite) TestLazyVisibility_RetriesOnlyAmbiguousBlocksWithRebasedRanges() {
	s.mockApplyEditBlockFlags()
	chatHistory := &persisted_ai.ChatHistoryContainer{History: persisted_ai.NewLlm2ChatHistory("", "")}
	eagerCallsBefore := s.extractVisibleCodeBlocksCalls

	responseText := strings.Join([]string{
		editBlockText(1, "a.go", []string{"line4"}, []string{"line4", "x", "y", "z"}),
		editBlockText(2, "a.go", []string{"dup"}, []string{"changed"}),
		editBlockText(3, "b.go", []string{"unique"}, []string{"replaced"}),
	}, "\n\n")
	var la *persisted_ai.Llm2Activities
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(assistantTextResponse(responseText), nil).Once()

	var cha *persisted_ai.ChatHistoryActivities
	extractCalls := 0
	s.env.OnActivity(cha.ExtractVisibleCodeBlocksForFiles, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input persisted_ai.ExtractVisibleCodeBlocksInput) (persisted_ai.ExtractVisibleCodeBlocksOutput, error) {
			extractCalls++
			s.Equal([]string{"a.go"}, input.FilePaths)
			s.NotNil(input.ChatHistory)
			return persisted_ai.ExtractVisibleCodeBlocksOutput{
				CodeBlocks: []tree_sitter.CodeBlock{{FilePath: "a.go", StartLine: 20, EndLine: 25}},
			}, nil
		},
	).Once()

	var da *DevActivities
	applyCalls := 0
	s.env.OnActivity(da.ApplyEditBlocks, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input ApplyEditBlockActivityInput) ([]ApplyEditBlockReport, error) {
			applyCalls++
			switch applyCalls {
			case 1:
				s.Require().Len(input.EditBlocks, 3)
				for _, block := range input.EditBlocks {
					s.Nil(block.VisibleFileRanges, "first pass must not carry visibility metadata")
				}
				return []ApplyEditBlockReport{
					{
						OriginalEditBlocks: []EditBlock{input.EditBlocks[0]},
						DidApply:           true,
						InitialDiff:        "--- a/a.go\n+++ b/a.go\n@@ -5,0 +5,3 @@\n+x\n+y\n+z\n",
					},
					{
						OriginalEditBlocks: []EditBlock{input.EditBlocks[1]},
						DidApply:           false,
						AmbiguousMatch:     true,
						Error:              "Multiple matches found",
					},
					{OriginalEditBlocks: []EditBlock{input.EditBlocks[2]}, DidApply: true},
				}, nil
			case 2:
				s.Require().Len(input.EditBlocks, 1, "only the ambiguous block is retried")
				block := input.EditBlocks[0]
				s.Equal(2, block.SequenceNumber)
				s.Equal([]FileRange{{FilePath: "a.go", StartLine: 23, EndLine: 28}}, block.VisibleFileRanges,
					"ranges must be rebased past the insertion applied in the first pass")
				return []ApplyEditBlockReport{{OriginalEditBlocks: []EditBlock{block}, DidApply: true}}, nil
			default:
				s.Fail("unexpected extra ApplyEditBlocks call")
				return nil, nil
			}
		},
	).Times(2)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, chatHistory, PromptInfoContainer{InitialCodeInfo{}})
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	s.Equal(2, applyCalls)
	s.Equal(1, extractCalls)
	s.Equal(eagerCallsBefore, s.extractVisibleCodeBlocksCalls, "eager extraction must not run")
	report := s.appendedText()
	for seq := 1; seq <= 3; seq++ {
		s.Contains(report, fmt.Sprintf("edit_block:%d application succeeded", seq))
	}
	s.NotContains(report, "application failed")
}

// Two ambiguous blocks in one file where the first retry grows the file above
// the second block's target: the second retry must be sent on its own, with
// ranges rebased across the first retry's diff.
func (s *AuthorEditBlocksTestSuite) TestLazyVisibility_SecondRetryRebasedAcrossFirstRetry() {
	s.mockApplyEditBlockFlags()
	chatHistory := &persisted_ai.ChatHistoryContainer{History: persisted_ai.NewLlm2ChatHistory("", "")}

	responseText := strings.Join([]string{
		editBlockText(1, "a.go", []string{"alpha"}, []string{"ALPHA", "plus1", "plus2", "plus3", "plus4"}),
		editBlockText(2, "a.go", []string{"beta"}, []string{"BETA"}),
	}, "\n\n")
	var la *persisted_ai.Llm2Activities
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(assistantTextResponse(responseText), nil).Once()

	var cha *persisted_ai.ChatHistoryActivities
	s.env.OnActivity(cha.ExtractVisibleCodeBlocksForFiles, mock.Anything, mock.Anything).Return(
		persisted_ai.ExtractVisibleCodeBlocksOutput{CodeBlocks: []tree_sitter.CodeBlock{
			{FilePath: "a.go", StartLine: 19, EndLine: 21},
			{FilePath: "a.go", StartLine: 39, EndLine: 41},
		}}, nil,
	).Once()

	var da *DevActivities
	applyCalls := 0
	s.env.OnActivity(da.ApplyEditBlocks, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input ApplyEditBlockActivityInput) ([]ApplyEditBlockReport, error) {
			applyCalls++
			switch applyCalls {
			case 1:
				s.Require().Len(input.EditBlocks, 2)
				return utils.Map(input.EditBlocks, func(block EditBlock) ApplyEditBlockReport {
					return ApplyEditBlockReport{OriginalEditBlocks: []EditBlock{block}, AmbiguousMatch: true, Error: "Multiple matches found"}
				}), nil
			case 2:
				s.Require().Len(input.EditBlocks, 1)
				s.Equal(1, input.EditBlocks[0].SequenceNumber)
				s.Equal([]FileRange{{FilePath: "a.go", StartLine: 19, EndLine: 21}, {FilePath: "a.go", StartLine: 39, EndLine: 41}}, input.EditBlocks[0].VisibleFileRanges)
				return []ApplyEditBlockReport{{
					OriginalEditBlocks: []EditBlock{input.EditBlocks[0]},
					DidApply:           true,
					FinalDiff:          "--- a/a.go\n+++ b/a.go\n@@ -20 +20,5 @@\n-alpha\n+ALPHA\n+plus1\n+plus2\n+plus3\n+plus4\n",
				}}, nil
			case 3:
				s.Require().Len(input.EditBlocks, 1)
				s.Equal(2, input.EditBlocks[0].SequenceNumber)
				s.Equal([]FileRange{{FilePath: "a.go", StartLine: 19, EndLine: 25}, {FilePath: "a.go", StartLine: 43, EndLine: 45}}, input.EditBlocks[0].VisibleFileRanges,
					"second retry must be rebased past the first retry's four added lines")
				return []ApplyEditBlockReport{{OriginalEditBlocks: []EditBlock{input.EditBlocks[0]}, DidApply: true}}, nil
			default:
				s.Fail("unexpected extra ApplyEditBlocks call")
				return nil, nil
			}
		},
	).Times(3)

	s.env.ExecuteWorkflow(s.wrapperWorkflow, chatHistory, PromptInfoContainer{InitialCodeInfo{}})
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Equal(3, applyCalls)
	report := s.appendedText()
	s.Contains(report, "edit_block:1 application succeeded")
	s.Contains(report, "edit_block:2 application succeeded")
	s.NotContains(report, "application failed")
}

func (s *AuthorEditBlocksTestSuite) TestLazyVisibility_NoExtractionWithoutAmbiguity() {
	cases := []struct {
		name   string
		report func(block EditBlock) ApplyEditBlockReport
	}{
		{
			name: "applied",
			report: func(block EditBlock) ApplyEditBlockReport {
				return ApplyEditBlockReport{OriginalEditBlocks: []EditBlock{block}, DidApply: true}
			},
		},
		{
			name: "no match failure",
			report: func(block EditBlock) ApplyEditBlockReport {
				return ApplyEditBlockReport{OriginalEditBlocks: []EditBlock{block}, Error: "no good match found"}
			},
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.mockApplyEditBlockFlags()
			chatHistory := &persisted_ai.ChatHistoryContainer{History: persisted_ai.NewLlm2ChatHistory("", "")}
			eagerCallsBefore := s.extractVisibleCodeBlocksCalls

			var la *persisted_ai.Llm2Activities
			s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(
				assistantTextResponse(editBlockText(1, "a.go", []string{"old"}, []string{"new"})), nil).Once()
			s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(assistantTextResponse("No edit blocks"), nil).Maybe()

			var da *DevActivities
			applyCalls := 0
			s.env.OnActivity(da.ApplyEditBlocks, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input ApplyEditBlockActivityInput) ([]ApplyEditBlockReport, error) {
					applyCalls++
					s.Require().Len(input.EditBlocks, 1)
					return []ApplyEditBlockReport{tc.report(input.EditBlocks[0])}, nil
				},
			).Once()

			s.env.ExecuteWorkflow(s.wrapperWorkflow, chatHistory, PromptInfoContainer{InitialCodeInfo{}})
			s.True(s.env.IsWorkflowCompleted())
			s.NoError(s.env.GetWorkflowError())
			s.Equal(1, applyCalls)
			s.Equal(eagerCallsBefore, s.extractVisibleCodeBlocksCalls)
		})
	}
}

// When the context visibility check is still enforced, edit blocks must carry
// visibility data for validation, so extraction stays eager.
func (s *AuthorEditBlocksTestSuite) TestLazyVisibility_FallsBackToEagerWhenVisibilityCheckEnforced() {
	s.flagOverrides = map[string]bool{fflag.DisableContextCodeVisibilityCheck: false}
	defer func() { s.flagOverrides = nil }()
	s.SetupTest()
	s.mockApplyEditBlockFlags()
	chatHistory := &persisted_ai.ChatHistoryContainer{History: persisted_ai.NewLlm2ChatHistory("", "")}
	eagerCallsBefore := s.extractVisibleCodeBlocksCalls

	// the update touches code never shown in the chat history, so validation
	// must reject it before it reaches the apply activity
	var la *persisted_ai.Llm2Activities
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(
		assistantTextResponse(editBlockText(1, "a.go", []string{"old"}, []string{"new"})), nil).Once()
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(assistantTextResponse("No edit blocks"), nil).Once()

	var da *DevActivities
	var appliedBlocks []EditBlock
	s.env.OnActivity(da.ApplyEditBlocks, mock.Anything, mock.Anything).Return(
		func(_ context.Context, input ApplyEditBlockActivityInput) ([]ApplyEditBlockReport, error) {
			appliedBlocks = append(appliedBlocks, input.EditBlocks...)
			return utils.Map(input.EditBlocks, func(block EditBlock) ApplyEditBlockReport {
				return ApplyEditBlockReport{OriginalEditBlocks: []EditBlock{block}, DidApply: true}
			}), nil
		},
	).Maybe()

	s.env.ExecuteWorkflow(s.wrapperWorkflow, chatHistory, PromptInfoContainer{InitialCodeInfo{}})
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.Equal(eagerCallsBefore+2, s.extractVisibleCodeBlocksCalls, "eager extraction runs once per response")
	s.Empty(appliedBlocks, "an edit to unseen code must be rejected by validation")
	s.Contains(s.appendedText(), "edit_block:1 application failed")
}

func TestApplyUpdateEditBlock_FlagsAmbiguousMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: dir})
	require.NoError(t, err)
	envContainer := env.EnvContainer{Env: devEnv}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dup.txt"), []byte("alpha\nbeta\ngamma\nalpha\nbeta\ndelta\n"), 0644))

	cases := []struct {
		name          string
		oldLines      []string
		wantErr       bool
		wantAmbiguous bool
	}{
		{name: "ambiguous", oldLines: []string{"alpha", "beta"}, wantErr: true, wantAmbiguous: true},
		{name: "unique", oldLines: []string{"gamma"}},
		{name: "no match", oldLines: []string{"zzzzzz"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block := EditBlock{FilePath: "dup.txt", EditType: "update", OldLines: tc.oldLines, NewLines: []string{"replacement"}}
			report, err := ApplyUpdateEditBlock(context.Background(), envContainer, block, dir)
			assert.Equal(t, tc.wantErr, err != nil, "err: %v", err)
			assert.Equal(t, tc.wantAmbiguous, report.AmbiguousMatch)
		})
	}
}

// Exercises the real activity twice, the way the lazy path does: a first pass
// with no visibility data, then a retry of only the ambiguous block whose
// visibility ranges must be rebased past the insertion the first pass made
// earlier in the same file.
func TestVisibilityForRetry_RebasesPastFirstPassEditsInSameFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runGitCommand(t, dir, "init")
	runGitCommand(t, dir, "config", "user.email", "test@example.com")
	runGitCommand(t, dir, "config", "user.name", "Test User")

	var lines []string
	lines = append(lines, "header", "alpha", "beta", "gamma")
	for i := 5; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("filler %d", i))
	}
	lines = append(lines, "alpha", "beta") // lines 21-22
	for i := 23; i <= 29; i++ {
		lines = append(lines, fmt.Sprintf("filler %d", i))
	}
	lines = append(lines, "alpha", "beta", "footer") // lines 30-32
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dup.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0644))
	runGitCommand(t, dir, "add", ".")
	runGitCommand(t, dir, "commit", "-m", "init")

	da := &DevActivities{
		LSPActivities: &lsp.LSPActivities{
			LSPClientProvider: func(languageName string) lsp.LSPClient {
				return &lsp.Jsonrpc2LSPClient{LanguageName: languageName}
			},
			InitializedClients: map[string]lsp.LSPClient{},
		},
	}
	envContainer := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: dir}}
	blocks := []EditBlock{
		{SequenceNumber: 1, FilePath: "dup.txt", EditType: "update", OldLines: []string{"gamma"}, NewLines: []string{"gamma", "ins1", "ins2", "ins3"}},
		{SequenceNumber: 2, FilePath: "dup.txt", EditType: "update", OldLines: []string{"alpha", "beta"}, NewLines: []string{"ALPHA", "BETA"}},
	}

	firstPass, err := da.ApplyEditBlocks(context.Background(), ApplyEditBlockActivityInput{
		EditBlocks:   blocks,
		EnvContainer: envContainer,
	})
	require.NoError(t, err)
	require.Len(t, firstPass, 2)
	require.True(t, firstPass[0].DidApply, firstPass[0].Error)
	require.False(t, firstPass[1].DidApply)
	require.True(t, firstPass[1].AmbiguousMatch, "duplicate old lines must be flagged as ambiguous")

	kept, retry := splitAmbiguousReports(firstPass)
	require.Len(t, kept, 1)
	require.Len(t, retry, 1)

	// the LLM saw the last duplicate (original lines 30-31) via a search hit
	seen := []tree_sitter.CodeBlock{{FilePath: "dup.txt", StartLine: 28, EndLine: 32}}
	retryBlock := visibilityForRetry(retry[0], seen, firstPass)
	assert.Equal(t, []FileRange{{FilePath: "dup.txt", StartLine: 31, EndLine: 35}}, retryBlock.VisibleFileRanges)

	retryReports, err := da.ApplyEditBlocks(context.Background(), ApplyEditBlockActivityInput{
		EditBlocks:   []EditBlock{retryBlock},
		EnvContainer: envContainer,
	})
	require.NoError(t, err)
	require.Len(t, retryReports, 1)
	require.True(t, retryReports[0].DidApply, retryReports[0].Error)

	content, err := os.ReadFile(filepath.Join(dir, "dup.txt"))
	require.NoError(t, err)
	got := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	assert.Equal(t, []string{"gamma", "ins1", "ins2", "ins3"}, got[3:7], "first-pass edit is not reapplied")
	assert.Equal(t, []string{"alpha", "beta"}, got[1:3], "first duplicate untouched")
	assert.Equal(t, []string{"alpha", "beta"}, got[23:25], "second duplicate untouched")
	assert.Equal(t, []string{"ALPHA", "BETA", "footer"}, got[32:35], "only the duplicate the LLM saw is changed")
}

// Two ambiguous blocks in the same file: the first retry grows the file above
// the second block's intended match, so the second retry's ranges must be
// rebased across the first retry as well as the first pass.
func TestLazyRetries_RebaseAcrossEarlierRetriesInSameFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runGitCommand(t, dir, "init")
	runGitCommand(t, dir, "config", "user.email", "test@example.com")
	runGitCommand(t, dir, "config", "user.name", "Test User")

	var lines []string
	lines = append(lines, "header")
	for i := 2; i <= 9; i++ {
		lines = append(lines, fmt.Sprintf("filler %d", i))
	}
	lines = append(lines, "alpha") // line 10
	for i := 11; i <= 19; i++ {
		lines = append(lines, fmt.Sprintf("filler %d", i))
	}
	lines = append(lines, "alpha") // line 20
	for i := 21; i <= 29; i++ {
		lines = append(lines, fmt.Sprintf("filler %d", i))
	}
	lines = append(lines, "beta") // line 30
	for i := 31; i <= 39; i++ {
		lines = append(lines, fmt.Sprintf("filler %d", i))
	}
	lines = append(lines, "beta", "footer") // lines 40-41
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dup.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0644))
	runGitCommand(t, dir, "add", ".")
	runGitCommand(t, dir, "commit", "-m", "init")

	da := &DevActivities{
		LSPActivities: &lsp.LSPActivities{
			LSPClientProvider: func(languageName string) lsp.LSPClient {
				return &lsp.Jsonrpc2LSPClient{LanguageName: languageName}
			},
			InitializedClients: map[string]lsp.LSPClient{},
		},
	}
	envContainer := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: dir}}
	apply := func(blocks []EditBlock) []ApplyEditBlockReport {
		reports, err := da.ApplyEditBlocks(context.Background(), ApplyEditBlockActivityInput{
			EditBlocks:   blocks,
			EnvContainer: envContainer,
		})
		require.NoError(t, err)
		return reports
	}

	firstPass := apply([]EditBlock{
		{SequenceNumber: 1, FilePath: "dup.txt", EditType: "update", OldLines: []string{"alpha"}, NewLines: []string{"ALPHA", "plus1", "plus2", "plus3", "plus4"}},
		{SequenceNumber: 2, FilePath: "dup.txt", EditType: "update", OldLines: []string{"beta"}, NewLines: []string{"BETA"}},
	})
	require.Len(t, firstPass, 2)
	kept, ambiguous := splitAmbiguousReports(firstPass)
	require.Empty(t, kept)
	require.Len(t, ambiguous, 2)

	// the LLM saw the second alpha (line 20) and the second beta (line 40)
	seen := []tree_sitter.CodeBlock{
		{FilePath: "dup.txt", StartLine: 19, EndLine: 21},
		{FilePath: "dup.txt", StartLine: 39, EndLine: 41},
	}

	applied := kept
	for _, block := range ambiguous {
		retryBlock := visibilityForRetry(block, seen, applied)
		retryReports := apply([]EditBlock{retryBlock})
		require.Len(t, retryReports, 1)
		require.True(t, retryReports[0].DidApply, "block %d: %s", block.SequenceNumber, retryReports[0].Error)
		applied = append(applied, retryReports...)
	}

	content, err := os.ReadFile(filepath.Join(dir, "dup.txt"))
	require.NoError(t, err)
	got := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	require.Len(t, got, 45)
	assert.Equal(t, "alpha", got[9], "first alpha untouched")
	assert.Equal(t, []string{"ALPHA", "plus1", "plus2", "plus3", "plus4"}, got[19:24], "seen alpha replaced")
	assert.Equal(t, "beta", got[33], "first beta untouched (shifted by 4)")
	assert.Equal(t, []string{"BETA", "footer"}, got[43:45], "seen beta replaced despite the earlier retry's line growth")
}

func TestSplitAmbiguousReports(t *testing.T) {
	t.Parallel()
	applied := ApplyEditBlockReport{OriginalEditBlocks: []EditBlock{{SequenceNumber: 1}}, DidApply: true}
	ambiguous := ApplyEditBlockReport{OriginalEditBlocks: []EditBlock{{SequenceNumber: 2}}, AmbiguousMatch: true}
	otherFailure := ApplyEditBlockReport{OriginalEditBlocks: []EditBlock{{SequenceNumber: 3}}, Error: "no match"}
	appliedDespiteFlag := ApplyEditBlockReport{OriginalEditBlocks: []EditBlock{{SequenceNumber: 4}}, DidApply: true, AmbiguousMatch: true}

	kept, retry := splitAmbiguousReports([]ApplyEditBlockReport{applied, ambiguous, otherFailure, appliedDespiteFlag})
	assert.Equal(t, []ApplyEditBlockReport{applied, otherFailure, appliedDespiteFlag}, kept)
	assert.Equal(t, []EditBlock{{SequenceNumber: 2}}, retry)
}
