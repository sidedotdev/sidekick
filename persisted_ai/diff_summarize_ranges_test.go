package persisted_ai

import (
	"fmt"
	"strings"
	"testing"

	"sidekick/coding/diffanalysis"

	"github.com/stretchr/testify/require"
)

func TestSplitHunkRangesMatchExcerptContents(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"added", "removed", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			var body strings.Builder
			oldCount, newCount := 0, 0
			for i := 0; i < 90; i++ {
				switch {
				case kind == "added":
					fmt.Fprintf(&body, "+new line %03d\n", i)
					newCount++
				case kind == "removed":
					fmt.Fprintf(&body, "-old line %03d\n", i)
					oldCount++
				case i%3 == 0:
					fmt.Fprintf(&body, " context %03d\n", i)
					oldCount++
					newCount++
				case i%3 == 1:
					fmt.Fprintf(&body, "-old line %03d\n", i)
					oldCount++
				default:
					fmt.Fprintf(&body, "+new line %03d\n", i)
					newCount++
				}
			}
			oldStart, newStart := 10, 20
			if oldCount == 0 {
				oldStart = 0
			}
			if newCount == 0 {
				newStart = 0
			}
			header := "diff --git a/log.txt b/log.txt\n--- a/log.txt\n+++ b/log.txt\n"
			raw := header + fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount) + body.String()
			files, err := diffanalysis.ParseUnifiedDiff(raw)
			require.NoError(t, err)
			require.Len(t, files, 1)
			chunks := splitLargeFileDiff(files[0], 250)
			require.Greater(t, len(chunks), 1)

			oldNext, newNext := oldStart, newStart
			if oldCount == 0 {
				oldNext++
			}
			if newCount == 0 {
				newNext++
			}
			var reconstructed strings.Builder
			for _, chunk := range chunks {
				parsed, err := diffanalysis.ParseUnifiedDiff(chunk.Content)
				require.NoError(t, err)
				require.Len(t, parsed, 1)
				require.Len(t, parsed[0].Hunks, 1)
				hunk := parsed[0].Hunks[0]
				gotOld, gotNew := 0, 0
				for _, line := range hunk.Lines {
					if line.Type != diffanalysis.LineAdded {
						gotOld++
					}
					if line.Type != diffanalysis.LineRemoved {
						gotNew++
					}
				}
				require.Equal(t, gotOld, hunk.OldCount)
				require.Equal(t, gotNew, hunk.NewCount)
				expectedOld, expectedNew := oldNext, newNext
				if gotOld == 0 {
					expectedOld--
				}
				if gotNew == 0 {
					expectedNew--
				}
				require.Equal(t, expectedOld, hunk.OldStart)
				require.Equal(t, expectedNew, hunk.NewStart)
				oldNext += gotOld
				newNext += gotNew
				_, excerpt, ok := strings.Cut(strings.TrimPrefix(chunk.Content, header), "\n")
				require.True(t, ok)
				reconstructed.WriteString(excerpt)
			}
			require.Equal(t, body.String(), reconstructed.String())
		})
	}
}
