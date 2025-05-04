package dev

import (
	"io/ioutil"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFindAcceptableMatchBasicCases(t *testing.T) {
	originalLines := []string{
		"Line 1: Nothing special",
		"Line 2: Start of block",
		"Line 3: Middle of block",
		"Line 4: End of block",
		"Line 5: After block",
		"Line 6: Whitespace only below",
		"    ",
		"Line 8: Some other content",
		"    Line 9: Whitespace match    ",
		"Lone 10: Partially similar to line 9",
		"Line 11: Before an empty line",
		"",
		"Line 13: After an empty line",
		"Line 14: Comments only below",
		"    // some comment here",
		"    # some comment here",
		"Line 17: Some other content",
		"Line 18: Multiple whitespace lines below",
		"    ",
		"    ",
		"Line 21: Between",
		"    ",
		"Line 23: Some other content",
	}

	tests := []struct {
		name         string
		block        EditBlock
		expectedBest int // expected index
		expectedAll  []int
	}{
		{
			"Exact match at start",
			EditBlock{OldLines: []string{originalLines[0]}},
			0, []int{0},
		},
		{
			"Exact Match in middle",
			EditBlock{OldLines: originalLines[1:4]},
			1, []int{1},
		},
		{
			"Exact match at end",
			EditBlock{OldLines: []string{originalLines[22]}},
			22, []int{22},
		},
		{
			"Exact match full",
			EditBlock{OldLines: originalLines},
			0, []int{0},
		},
		{
			"Whitespace Trim Match",
			EditBlock{OldLines: []string{"    Line 9: Whitespace match    "}},
			8, []int{8},
		},
		{
			"Similarity Match",
			EditBlock{OldLines: []string{"Lone 10: Partially similar to line 10"}},
			9, []int{9},
		},
		{
			"Whitespace line missing in edit block Match",
			EditBlock{OldLines: []string{"Line 6: Whitespace only below", "Line 8: Some other content"}},
			5, []int{5},
		},
		{
			"Extra whitespace line in edit block Match",
			EditBlock{OldLines: []string{"Line 2: Start of block", "     ", "Line 3: Middle of block"}},
			1, []int{1},
		},
		{
			"Empty line inclusive Match",
			EditBlock{OldLines: []string{"Line 11: Before an empty line", "", "Line 13: After an empty line"}},
			10, []int{10},
		},
		{
			"Empty line missing Match",
			EditBlock{OldLines: []string{"Line 11: Before an empty line", "Line 13: After an empty line"}},
			10, []int{10},
		},
		{
			"Extra empty line in edit block Match",
			EditBlock{OldLines: []string{"Line 2: Start of block", "", "Line 3: Middle of block"}},
			1, []int{1},
		},
		{
			"Extra empty line at end of edit block Match",
			EditBlock{OldLines: []string{"Line 2: Start of block", "Line 3: Middle of block", ""}},
			1, []int{1},
		},
		{
			"Comment lines missing Match",
			EditBlock{OldLines: []string{"Line 14: Comments only below", "Line 17: Some other content"}},
			13, []int{13},
		},
		{
			"Extra comment line in edit block Match",
			EditBlock{OldLines: []string{"Line 2: Start of block", "    // another comment", "Line 3: Middle of block"}},
			1, []int{1},
		},
		{
			"Extra # comment line in edit block Match",
			EditBlock{OldLines: []string{"Line 2: Start of block", "    # another comment", "Line 3: Middle of block"}},
			1, []int{1},
		},
		{
			"Ignore multiple missing and added whitespace lines",
			EditBlock{OldLines: []string{"", "Line 18: Multiple whitespace lines below", "Line 21: Between", "Line 23: Some other content"}},
			17, []int{17},
		},
		{
			"Added empty line before start of original",
			EditBlock{OldLines: []string{"", originalLines[0]}},
			0, []int{0},
		},
		{
			"Added empty line after end of original",
			EditBlock{OldLines: []string{originalLines[len(originalLines)-1], ""}},
			len(originalLines) - 1, []int{len(originalLines) - 1},
		},
		{
			"Added a line matching nothing at start, but still a match since everything else matches",
			EditBlock{OldLines: append([]string{"this matches none of the lines", "\n", "\n"}, originalLines...)},
			0, []int{0},
		},
		{
			"No match",
			EditBlock{OldLines: []string{"this matches none of the lines", "\n", "\n"}},
			0, []int{},
		},
		{
			"No Match again",
			EditBlock{OldLines: []string{"This line does not exist in the original."}},
			-99, // Indicates no match
			[]int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bestMatch, allMatches := FindAcceptableMatch(tt.block, originalLines, false)
			assert.Equal(t, tt.expectedAll, utils.Map(allMatches, func(m match) int { return m.index }))
			if tt.expectedBest == -99 {
				if bestMatch.score != 0 {
					t.Errorf("Expected score: 0, match:%v", bestMatch)
				}
			} else {
				if bestMatch.index != tt.expectedBest {
					t.Errorf("Expected index: %d, Got index: %d, match: %v", tt.expectedBest, bestMatch.index, utils.PrettyJSON(bestMatch))
				}
			}
		})
	}
}

func TestFindAcceptableMatchAdvancedCases(t *testing.T) {
	t.Run("Ignore leading hallucinated comment", func(t *testing.T) {
		originalLines := strings.Split(`
// sidekick-managed worktrees.
func determineManagedWorktreeBranches(workspace *domain.Workspace, gitWorktrees map[string]string) (map[string]struct{}, error) {
	managedBranches := make(map[string]struct{})

	sidekickDataHome, err := common.GetSidekickDataHome()
`, "\n")

		block := EditBlock{
			FilePath: "some/file.go",
			OldLines: strings.Split(`
// sidekick-managed worktrees.
func determineManagedWorktreeBranches(workspace *domain.Workspace, gitWorktrees map[string]string) (map[string]struct{}, error) {
	managedBranches := make(map[string]struct{})

	sidekickDataHome, err := common.GetSidekickDataHome()
`, "\n"),
			EditType: "update",
		}

		bestMatch, allAcceptableMatches := FindAcceptableMatch(block, originalLines, false)

		utils.PrettyPrint(allAcceptableMatches)
		assert.True(t, len(allAcceptableMatches) == 1, "Expected an acceptable match despite the leading mismatched line")
		assert.True(t, bestMatch.successfulMatch, "Expected a successful match despite the leading mismatched line")
	})

	t.Run("bad leading closing delimiter line NOT ignored", func(t *testing.T) {
		originalLines := strings.Split(`
}

func TestGetWorkspaceByIdHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
	db := ctrl.service
`, "\n")

		block := EditBlock{
			FilePath: "some/file.go",
			OldLines: strings.Split(`)

func TestGetWorkspaceByIdHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctrl := NewMockController(t)
`, "\n"),
			EditType: "update",
		}

		_, allAcceptableMatches := FindAcceptableMatch(block, originalLines, false)

		utils.PrettyPrint(allAcceptableMatches)
		assert.True(t, len(allAcceptableMatches) == 0, "Expected no acceptable match due to bad delimiter match")
	})
}

// bigger test, catches more issues
// TODO use a generative test instead, eg by performing small random edits on
// the originalLines vs large random edits on random code snippets
func TestFindAcceptableMatchOneOff(t *testing.T) {
	editBlock := EditBlock{
		FilePath: "dev/test_files/extract_go_function_bodies_activity.txt",
		OldLines: strings.Split(`func extractFunctionBody(filename, signature string, contextLines int) (string, error) {
	source, err := ioutil.ReadFile(filename)
	if err != nil {
		return "", err
	}

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, source, parser.AllErrors|parser.ParseComments)
	if err != nil {
		return "", err
	}

	commentMap := ast.NewCommentMap(fset, node, node.Comments)

	for _, decl := range node.Decls {
		if funcDecl, ok := decl.(*ast.FuncDecl); ok {
			extractedSig := ExtractFunctionSignature(fset, funcDecl)
			if extractedSig == signature {
				startPos := fset.Position(funcDecl.Pos()).Line - contextLines
				endPos := fset.Position(funcDecl.End()).Line + contextLines
				if comments, exists := commentMap[funcDecl]; exists {
					commentStartPos := fset.Position(comments[0].Pos()).Line
					if commentStartPos < startPos {
						startPos = commentStartPos
					}
				}
				totalLines := len(strings.Split(string(source), "\n"))
				if startPos < 1 {
					startPos = 1
				}
				if endPos > totalLines {
					endPos = totalLines
				}
				contextAndBodyLines := strings.Split(string(source), "\n")[startPos-1 : endPos]
				contextAndBodyStr := strings.Join(contextAndBodyLines, "\n")
				return fmt.Sprintf("Start Line: %v\n`+"```"+`"go\n%s\n`+"```"+`", startPos, contextAndBodyStr), nil
			}
		}
	}

	return "", fmt.Errorf("Function with signature %s not found in %s", signature, filename)
}`, "\n"),
	}
	originalContents, err := ioutil.ReadFile("../" + editBlock.FilePath)
	if err != nil {
		t.Errorf("can't read file: %v", err)
	}
	originalLines := strings.Split(string(originalContents), "\n")
	acceptableMatch, _ := FindAcceptableMatch(editBlock, originalLines, true)
	if acceptableMatch.score == 0 {
		t.Errorf("Expected score > 0, match:%v", acceptableMatch)
	}
}
