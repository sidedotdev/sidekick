package diffanalysis

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Recovered from a real flow whose second review round degraded to the full
// diff: the interdiff library omitted an import line that both reviews added
// identically, and dropped the no-newline marker on DiffFile.vue.
func TestInterdiffRealReviewPairWithOmittedSharedLine(t *testing.T) {
	t.Parallel()

	fixtureDir := filepath.Join("testdata", "interdiff_omitted_common_line")
	prior, err := os.ReadFile(filepath.Join(fixtureDir, "prior.diff"))
	require.NoError(t, err)
	current, err := os.ReadFile(filepath.Join(fixtureDir, "current.diff"))
	require.NoError(t, err)

	result, err := Interdiff(string(prior), string(current))
	require.NoError(t, err)
	require.NotEqual(t, string(current), result)

	files, err := ParseUnifiedDiff(result)
	require.NoError(t, err)
	var diffFile *FileDiff
	for i := range files {
		if files[i].NewPath == "frontend/src/components/DiffFile.vue" {
			diffFile = &files[i]
		}
	}
	require.NotNil(t, diffFile)

	linesOfType := func(lineType LineType) []string {
		var out []string
		for _, hunk := range diffFile.Hunks {
			for _, line := range hunk.Lines {
				if line.Type == lineType {
					out = append(out, line.Content)
				}
			}
		}
		return out
	}
	// Work already reviewed is context, while what changed since is a change.
	assert.Contains(t, linesOfType(LineContext), "import { computed, ref, inject, onErrorCaptured, watch } from 'vue'")
	assert.Contains(t, linesOfType(LineContext), "const rawPatch = computed(() => props.fileData.hunks.join('\\n'))")
	assert.Contains(t, linesOfType(LineRemoved), "import { isCombinedDiff, type ParsedDiff } from '../lib/diffUtils'")
	assert.Contains(t, linesOfType(LineAdded), "import SegmentedControl from './SegmentedControl.vue'")
	assert.NotContains(t, linesOfType(LineAdded), "const rawPatch = computed(() => props.fileData.hunks.join('\\n'))")

	priorFiles, err := ParseUnifiedDiff(string(prior))
	require.NoError(t, err)
	currentFiles, err := ParseUnifiedDiff(string(current))
	require.NoError(t, err)
	for _, file := range files {
		t.Run(file.NewPath, func(t *testing.T) {
			t.Parallel()
			assertInterdiffApplies(t, findFile(priorFiles, file.NewPath), findFile(currentFiles, file.NewPath), file)
		})
	}
}

func findFile(files []FileDiff, path string) *FileDiff {
	for i := range files {
		if files[i].NewPath == path {
			return &files[i]
		}
	}
	return nil
}

// assertInterdiffApplies reconstructs a base file consistent with the pre-image
// lines of both review diffs, applies each review diff to it, and checks that
// the interdiff carries the prior review's result to the current one.
func assertInterdiffApplies(t *testing.T, prior, current *FileDiff, interdiff FileDiff) {
	t.Helper()
	require.NotNil(t, current)

	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	git := func(stdin string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(stdin)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s\n%s", strings.Join(args, " "), output, stdin)
	}
	git("", "init")

	rename := func(file *FileDiff) string {
		if file == nil {
			return ""
		}
		section := file.RawContent
		section = strings.ReplaceAll(section, "a/"+file.OldPath, "a/file.txt")
		section = strings.ReplaceAll(section, "b/"+file.NewPath, "b/file.txt")
		return ensureTrailingNewline(section)
	}
	writeBase := func() {
		if current.IsNewFile {
			require.NoError(t, os.RemoveAll(path))
			return
		}
		require.NoError(t, os.WriteFile(path, []byte(reconstructPreImage(prior, current)), 0600))
	}
	apply := func(file *FileDiff) string {
		t.Helper()
		if file != nil {
			git(rename(file), "apply", "--whitespace=nowarn", "-")
		}
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		return string(content)
	}

	writeBase()
	afterPrior := apply(prior)
	writeBase()
	afterCurrent := apply(current)

	require.NoError(t, os.WriteFile(path, []byte(afterPrior), 0600))
	require.Equal(t, afterCurrent, apply(&interdiff))
}

// reconstructPreImage builds a file whose lines covered by either review diff
// are taken from their pre-image lines and whose remaining lines are fillers.
// A no-newline marker after a pre-image line means the file ends there.
func reconstructPreImage(files ...*FileDiff) string {
	lines := map[int]string{}
	length, noNewline := 0, false
	for _, file := range files {
		if file == nil {
			continue
		}
		inHunk := false
		oldLine, lastOldLine := 0, -1
		for _, raw := range strings.Split(file.RawContent, "\n") {
			if matches := hunkHeaderRegex.FindStringSubmatch(raw); matches != nil {
				inHunk = true
				oldLine = parseInt(matches[1])
				lastOldLine = -1
				continue
			}
			if !inHunk || raw == "" {
				continue
			}
			switch raw[0] {
			case ' ', '-':
				lines[oldLine] = raw[1:]
				lastOldLine = oldLine
				oldLine++
				length = max(length, lastOldLine)
			case '+':
				lastOldLine = -1
			case '\\':
				if lastOldLine >= 0 {
					noNewline = true
				}
			}
		}
	}
	var out strings.Builder
	for i := 1; i <= length; i++ {
		content, ok := lines[i]
		if !ok {
			content = fmt.Sprintf("filler line %d", i)
		}
		out.WriteString(content)
		if i < length || !noNewline {
			out.WriteString("\n")
		}
	}
	return out.String()
}
