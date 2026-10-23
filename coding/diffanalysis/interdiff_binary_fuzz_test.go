package diffanalysis

import (
	"strings"
	"testing"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

// Binary summaries promise metadata and object identities, not applicable
// payloads. Compare those promises with Git rather than pretending git apply
// can reconstruct binary content omitted from both review patches.
func FuzzInterdiffBinarySummaries(f *testing.F) {
	for flags := uint8(0); flags < 64; flags++ {
		f.Add("base", "reviewed", "current", flags)
	}
	f.Add("same", "same", "same", uint8(63))
	f.Add("base", "same", "same", uint8(7))
	f.Fuzz(func(t *testing.T, base, prior, current string, flags uint8) {
		t.Parallel()
		repo := newInterdiffFuzzRepo(t)
		var ids [3]string
		for i, text := range []string{base, prior, current} {
			tree := interdiffFuzzTree{}
			if flags&(1<<i) != 0 {
				mode := "100644"
				if flags&(1<<(i+3)) != 0 {
					mode = "100755"
				}
				tree["binary.dat"] = interdiffFuzzFile{
					mode: mode, content: "\x00" + text[:min(len(text), 4096)],
				}
			}
			ids[i] = repo.tree(tree)
		}
		diff := func(from, to string) string {
			return repo.git("", "diff-tree", "-r", "-p", "--no-commit-id",
				"--no-ext-diff", "--no-textconv", "--no-renames", "--full-index", from, to)
		}
		before, after := diff(ids[0], ids[1]), diff(ids[0], ids[2])
		for _, direction := range []struct {
			name, prior, current, want string
			from, to                   string
		}{
			{"forward", before, after, diff(ids[1], ids[2]), ids[1], ids[2]},
			{"reverse", after, before, diff(ids[2], ids[1]), ids[2], ids[1]},
			{"identity", before, before, "", ids[1], ids[1]},
		} {
			t.Run(direction.name, func(t *testing.T) {
				got, err := Interdiff(direction.prior, direction.current)
				if err != nil {
					t.Fatal(err)
				}
				if direction.prior == "" {
					if got != "" {
						t.Fatalf("first review must not invent an incremental comparison:\n%s", got)
					}
					return
				}
				expected, _, err := gitdiff.Parse(strings.NewReader(direction.want))
				if err != nil {
					t.Fatal(err)
				}
				if len(expected) == 0 || !expected[0].IsBinary {
					oracle := *repo
					oracle.t = t
					oracle.assertApplies(direction.from, direction.to, got)
					if direction.from == direction.to && got != "" {
						t.Fatalf("identical trees produced a change:\n%s", got)
					}
					return
				}
				type summary struct {
					oldName, newName, oldOID, newOID string
					oldMode, newMode                 uint32
					created, deleted, binary         bool
				}
				metadata := func(file *gitdiff.File) summary {
					oldMode, newMode := file.OldMode, file.NewMode
					if oldMode == newMode {
						oldMode, newMode = 0, 0
					}
					return summary{
						oldName: file.OldName, newName: file.NewName,
						oldOID: file.OldOIDPrefix, newOID: file.NewOIDPrefix,
						oldMode: uint32(oldMode), newMode: uint32(newMode),
						created: file.IsNew, deleted: file.IsDelete, binary: file.IsBinary,
					}
				}
				actual, _, err := gitdiff.Parse(strings.NewReader(got))
				if err != nil {
					t.Fatal(err)
				}
				if len(expected) != 1 || len(actual) != 1 ||
					metadata(expected[0]) != metadata(actual[0]) || len(actual[0].TextFragments) != 0 {
					t.Fatalf("binary summary disagrees with Git\nPRIOR:\n%s\nCURRENT:\n%s\nWANT:\n%s\nGOT:\n%s",
						direction.prior, direction.current, direction.want, got)
				}
			})
		}
	})
}
