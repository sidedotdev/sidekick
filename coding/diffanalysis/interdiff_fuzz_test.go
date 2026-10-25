package diffanalysis

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

type interdiffFuzzFile struct {
	mode    string
	content string
}

type interdiffFuzzTree map[string]interdiffFuzzFile

// Git supplies both the input diffs and the application oracle. Comparing tree
// IDs checks bytes, paths, existence and modes without sharing Interdiff's parser.
func FuzzInterdiffTrees(f *testing.F) {
	numbered := func(n int) string {
		var out strings.Builder
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&out, "line %d\n", i)
		}
		return out.String()
	}
	add := func(base, prior, current string, program []byte, options uint8) {
		f.Add(base, prior, current, program, options)
	}

	long := numbered(50)
	add(long,
		strings.ReplaceAll(strings.ReplaceAll(long, "line 10\n", "line 10\nprior\n"), "line 40\n", "line 40\nreverted\n"),
		strings.ReplaceAll(long, "line 10\n", "line 10\ncurrent\nextra\n"), []byte{}, 2)
	add(long,
		strings.ReplaceAll(strings.ReplaceAll(long, "line 10\n", "line 10\nprior\n"), "line 40\n", ""),
		strings.ReplaceAll(long, "line 10\n", "line 10\ncurrent\nextra\n"), []byte{}, 2)
	add("s\nA\nx\ny\nB\ne", "s\nA2\nx\ny\nB2\ne", "s\nA2\nx\ny\nB3\nB4\ne", []byte{}, 2)
	add("a\nb\nc\n", "a\nshared\nb\nold\nc\n", "a\nshared\nb\nnew\nc\n", []byte{}, 2)
	add(long, strings.ReplaceAll(long, "line 10\n", "shared\naddition\n"),
		strings.ReplaceAll(strings.ReplaceAll(long, "line 10\n", "shared\naddition\n"), "line 40\n", "changed\n"), []byte{}, 2)
	add("", long, strings.ReplaceAll(long, "line 25\n", "changed\n"), []byte{0, 6, 0, 0, 0}, 2)
	add("a\nb\n", "a\nfirst\nsecond\nb\n", "a\nsecond\nb\n", []byte{}, 2)
	add("base\n", "reviewed\n", "base\n", []byte{}, 2)
	add("base\n", "base\n", "current\n", []byte{}, 2)
	add("base\n", "reviewed\n", "reviewed\n", []byte{}, 2)
	add("", "", "filled\n", []byte{0, 6, 0, 0, 0}, 2)
	add("", "filled\n", "", []byte{0, 6, 0, 0, 0}, 2)
	add("base\n", "base\n", "edited\n", []byte{1, 7, 0, 0, 0}, 2)

	// Each operation is also a seed, so file operations do not depend on the
	// fuzzer first discovering a meaningful multi-byte instruction.
	for op := byte(0); op < 11; op++ {
		for side := byte(0); side < 4; side++ {
			add("first\n\nlast\n", "first\nreviewed\nlast\n", "first\ncurrent\nlast",
				[]byte{side, op, 0, 1, 2}, 2)
		}
	}
	add(long, long+"prior\n", long+"current\n",
		[]byte{4, 9, 0, 1, 0, 3, 7, 1, 0, 0, 1, 0, 1, 12, 2, 2, 2, 1, 35, 1}, 2)
	for _, options := range []uint8{0, 1, 3, 4, 10, 42, 74, 138} {
		add(long, strings.ReplaceAll(long, "line 10\n", "shared\naddition\n"),
			strings.ReplaceAll(long, "line 40\n", "current\n"), []byte{}, options)
		add(long, long, long, []byte{1, 8, 0, 0, 0, 2, 9, 0, 0, 1, 2, 0, 2, 20, 2}, options)
	}
	add("first int\nsecond int\n", "first int\nsecond int\nthird int\n",
		"first  int\nsecond int\nthird int\n", []byte{}, 2)
	for endings := 0; endings < 8; endings++ {
		texts := []string{"a\nx\nb", "a2\nx\nb2", "a2\nx2\nb2"}
		for i := range texts {
			if endings&(1<<i) != 0 {
				texts[i] += "\n"
			}
		}
		add(texts[0], texts[1], texts[2], []byte{}, 2)
	}
	for path := byte(0); path < byte(len(interdiffFuzzPaths)); path++ {
		add("base\n", "prior\n", "current\n",
			[]byte{4, 9, 0, 0, path, 2, 0, path, 0, 1}, 10)
	}
	for op := byte(11); op < 14; op++ {
		add("a\nb\n", "a\nprior\nb\n", "a\ncurrent\nb\n", []byte{1, op, 0, 1, 2}, 10)
	}
	interdiffFuzzFixtureSeeds(f)
	add(long, long+"prior\n", long+"current\n", []byte{}, 18)
	add("", long, strings.ReplaceAll(long, "line 25\n", "changed\n"), []byte{0, 6, 0, 0, 0}, 18)

	f.Fuzz(func(t *testing.T, base, prior, current string, program []byte, options uint8) {
		trees := [3]interdiffFuzzTree{}
		for i, content := range []string{base, prior, current} {
			trees[i] = interdiffFuzzTree{
				"file.txt": {mode: "100644", content: interdiffFuzzText(content)},
			}
		}
		interdiffFuzzProgram(trees, program)
		repo := newInterdiffFuzzRepo(t)
		ids := [3]string{}
		for i, tree := range trees {
			ids[i] = repo.tree(tree)
		}
		context := []int{0, 1, 3, 5, 20}[int(options)%5]
		rename := "--no-renames"
		if options&8 != 0 {
			rename = "--find-renames=50%"
		}
		diff := func(from, to string, context int) string {
			args := []string{"diff-tree", "-r", "-p", "--no-commit-id",
				"--no-ext-diff", "--no-textconv", "--no-color",
				"--no-indent-heuristic", fmt.Sprintf("--unified=%d", context), rename}
			if options&16 != 0 {
				args = append(args, "--abbrev=7")
			} else {
				args = append(args, "--full-index")
			}
			if options&32 != 0 {
				args = append(args, "--minimal")
			}
			if options&128 != 0 {
				args = append(args, "--diff-algorithm=histogram")
			} else {
				args = append(args, "--diff-algorithm=myers")
			}
			return repo.git("", append(args, from, to)...)
		}
		priorDiff := diff(ids[0], ids[1], context)
		if options&64 != 0 {
			context = (context + 1) % 6
		}
		currentDiff := diff(ids[0], ids[2], context)
		t.Logf("options=%d program=%x\nPRIOR:\n%s\nCURRENT:\n%s", options, program, priorDiff, currentDiff)

		// Validate the oracle on the unmodified Git patches before using it to
		// judge a synthesized patch, including zero-context patches.
		repo.assertApplies(ids[0], ids[1], priorDiff)
		repo.assertApplies(ids[0], ids[2], currentDiff)
		for _, pair := range []struct {
			name        string
			prior, next string
			from, to    string
		}{
			{"forward", priorDiff, currentDiff, ids[1], ids[2]},
			{"reverse", currentDiff, priorDiff, ids[2], ids[1]},
			{"identity", priorDiff, priorDiff, ids[1], ids[1]},
		} {
			t.Run(pair.name, func(t *testing.T) {
				repo := *repo
				repo.t = t
				result, err := Interdiff(pair.prior, pair.next)
				if err != nil {
					t.Fatalf("Interdiff: %v", err)
				}
				if pair.prior == "" {
					if result != "" {
						t.Fatalf("first review must have no incremental diff:\n%s", result)
					}
					return
				}
				repo.assertApplies(pair.from, pair.to, result)
				if pair.from == pair.to && result != "" {
					t.Fatalf("identical trees produced a change:\n%s", result)
				}
				interdiffFuzzCheckContext(t, pair.prior, pair.next, result)
			})
		}
	})
}

func interdiffFuzzText(s string) string {
	if len(s) > 64*1024 {
		s = s[:64*1024]
	}
	newlines := 0
	for i := range s {
		if s[i] == '\n' {
			newlines++
			if newlines == 2000 {
				s = s[:i+1]
				break
			}
		}
	}
	// Binary summary diffs do not contain enough information for a tree
	// application oracle. This target deliberately generates text patches.
	return strings.ReplaceAll(s, "\x00", "?")
}

var interdiffFuzzPaths = []string{
	"file.txt", "nested/other.txt", "space name.txt", "copy.txt",
	"quotes\".txt", "tabs\t.txt", "new\nline.txt", "日本語.txt",
}

// Instructions are side, operation, path, position, length. Side 3 changes both
// reviews; side 4 changes all trees. Keeping instructions local makes mutations
// and corpus minimization preserve useful shared edits.
func interdiffFuzzProgram(trees [3]interdiffFuzzTree, program []byte) {
	paths := interdiffFuzzPaths
	for i := 0; i+5 <= len(program) && i < 5*32; i += 5 {
		side, op, path, pos, count := program[i]%5, program[i+1]%14, paths[int(program[i+2])%len(paths)], int(program[i+3]), int(program[i+4])
		for treeIndex, tree := range trees {
			if side < 3 && int(side) != treeIndex || side == 3 && treeIndex == 0 {
				continue
			}
			file, exists := tree[path]
			if file.mode == "" {
				file.mode = "100644"
			}
			lines := strings.SplitAfter(file.content, "\n")
			if lines[len(lines)-1] == "" {
				lines = lines[:len(lines)-1]
			}
			start := pos % (len(lines) + 1)
			end := min(len(lines), start+1+count%8)
			insert := strings.Repeat(fmt.Sprintf("edit %d\n", count), 1+count%8)
			switch op {
			case 0, 1, 2:
				if op == 0 {
					end = start
				}
				if op == 1 {
					insert = ""
				}
				file.content = strings.Join(lines[:start], "") + insert + strings.Join(lines[end:], "")
			case 3, 4:
				block := strings.Join(lines[start:end], "")
				if op == 3 {
					lines = append(lines[:start:start], lines[end:]...)
				}
				file.content = block + strings.Join(lines, "")
			case 5:
				file.content = ""
			case 6:
				delete(tree, path)
				continue
			case 7:
				if file.mode == "100755" {
					file.mode = "100644"
				} else {
					file.mode = "100755"
				}
			case 8, 9:
				if exists {
					destination := paths[(int(program[i+2])+1+count%(len(paths)-1))%len(paths)]
					tree[destination] = file
					if op == 8 {
						delete(tree, path)
					}
				}
				continue
			case 10:
				file.mode = "120000"
				file.content = paths[count%len(paths)]
			case 11:
				if strings.HasSuffix(file.content, "\n") {
					file.content = strings.TrimSuffix(file.content, "\n")
				} else {
					file.content += "\n"
				}
			case 12:
				file.content = strings.ReplaceAll(file.content, "\n", "\r\n")
			case 13:
				file.mode = "160000"
				file.content = insert
			}
			tree[path] = file
		}
	}
}

type interdiffFuzzRepo struct {
	t     testing.TB
	dir   string
	env   []string
	blobs map[string]string
}

func newInterdiffFuzzRepo(t testing.TB) *interdiffFuzzRepo {
	t.Helper()
	dir := t.TempDir()
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GIT_") || key == "HOME" || key == "XDG_CONFIG_HOME" || key == "LC_ALL" {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "HOME="+dir, "XDG_CONFIG_HOME="+dir, "LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_INDEX_FILE="+filepath.Join(dir, "oracle.index"))
	repo := &interdiffFuzzRepo{t: t, dir: dir, env: env, blobs: map[string]string{}}
	repo.git("", "init", "--bare", "--quiet")
	return repo
}

func (r *interdiffFuzzRepo) git(stdin string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env, cmd.Stdin = r.dir, r.env, strings.NewReader(stdin)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s\nINPUT:\n%s", args, err, stderr.String(), stdin)
	}
	return string(out)
}

func (r *interdiffFuzzRepo) tree(tree interdiffFuzzTree) string {
	r.t.Helper()
	r.git("", "read-tree", "--empty")
	var index strings.Builder
	for path, file := range tree {
		kind, content := "blob", file.content
		if file.mode == "160000" {
			kind = "commit"
			emptyTree := strings.TrimSpace(r.git("", "hash-object", "-t", "tree", "-w", "--stdin"))
			content = "tree " + emptyTree + "\nauthor Fuzz <fuzz@example.invalid> 0 +0000\ncommitter Fuzz <fuzz@example.invalid> 0 +0000\n\n" + content
		}
		key := kind + ":" + content
		hash, ok := r.blobs[key]
		if !ok {
			hash = strings.TrimSpace(r.git(content, "hash-object", "-t", kind, "-w", "--stdin"))
			r.blobs[key] = hash
		}
		fmt.Fprintf(&index, "%s %s\t%s\x00", file.mode, hash, path)
	}
	r.git(index.String(), "update-index", "-z", "--index-info")
	return strings.TrimSpace(r.git("", "write-tree"))
}

func (r *interdiffFuzzRepo) assertApplies(from, to, patch string) {
	r.t.Helper()
	r.git("", "read-tree", from)
	if patch != "" {
		r.git(patch, "apply", "--cached", "--unidiff-zero", "--whitespace=nowarn", "-")
	}
	got := strings.TrimSpace(r.git("", "write-tree"))
	if got != to {
		delta := r.git("", "diff-tree", "-r", "-p", to, got)
		r.t.Fatalf("patch produced tree %s, want %s\nPATCH:\n%s\nTREE MISMATCH:\n%s", got, to, patch, delta)
	}
}

func interdiffFuzzCheckContext(t *testing.T, prior, current, result string) {
	t.Helper()
	files, _, err := gitdiff.Parse(strings.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		for _, hunk := range file.TextFragments {
			if hunk.LeadingContext > 5 || hunk.TrailingContext > 5 {
				t.Fatalf("excessive unchanged context: leading=%d trailing=%d\n%s",
					hunk.LeadingContext, hunk.TrailingContext, result)
			}
		}
	}
}

// Historical patches become ordinary source/program seeds, so mutations can
// explore their neighborhoods rather than just replaying fixed diff strings.
func interdiffFuzzFixtureSeeds(f *testing.F) {
	f.Helper()
	for _, fixture := range []struct{ directory, path string }{
		{"interdiff_reverted_hunk", "api/provider_auth.go"},
		{"interdiff_counted_omitted_lines", "dev/basic_dev_workflow.go"},
		{"interdiff_omitted_common_line", "frontend/src/components/DiffFile.vue"},
	} {
		patches := [2]*gitdiff.File{}
		known := map[int64]string{}
		length := int64(0)
		for i, name := range []string{"prior.diff", "current.diff"} {
			data, err := os.ReadFile(filepath.Join("testdata", fixture.directory, name))
			if err != nil {
				f.Fatal(err)
			}
			files, _, err := gitdiff.Parse(strings.NewReader(string(data)))
			if err != nil {
				f.Fatal(err)
			}
			for _, file := range files {
				if file.NewName != fixture.path {
					continue
				}
				patches[i] = file
				for _, hunk := range file.TextFragments {
					position := hunk.OldPosition
					for _, line := range hunk.Lines {
						if line.Old() {
							if previous, exists := known[position]; exists && previous != line.Line {
								f.Fatalf("inconsistent fixture base: %s line %d", fixture.directory, position)
							}
							known[position] = line.Line
							length = max(length, position)
							position++
						}
					}
				}
			}
			if patches[i] == nil {
				f.Fatalf("missing fixture file: %s/%s", fixture.directory, fixture.path)
			}
		}
		var base strings.Builder
		for line := int64(1); line <= length; line++ {
			if text, exists := known[line]; exists {
				base.WriteString(text)
			} else {
				fmt.Fprintf(&base, "unobserved base line %d\n", line)
			}
		}
		repo := newInterdiffFuzzRepo(f)
		baseID := repo.tree(interdiffFuzzTree{fixture.path: {mode: "100644", content: base.String()}})
		var results [2]string
		for i, patch := range patches {
			repo.git("", "read-tree", baseID)
			repo.git(patch.String(), "apply", "--cached", "--unidiff-zero", "--whitespace=nowarn", "-")
			tree := strings.TrimSpace(repo.git("", "write-tree"))
			results[i] = repo.git("", "show", tree+":"+fixture.path)
		}
		if interdiffFuzzText(base.String()) != base.String() ||
			interdiffFuzzText(results[0]) != results[0] || interdiffFuzzText(results[1]) != results[1] {
			f.Fatalf("fixture exceeds fuzz input bounds: %s", fixture.directory)
		}
		f.Add(base.String(), results[0], results[1], []byte{}, uint8(2))
	}
}

func assertEquivalentReviewPatch(t *testing.T, want, got string) {
	t.Helper()
	canonical := func(patch string) string {
		files, _, err := gitdiff.Parse(strings.NewReader(patch))
		if err != nil {
			t.Fatal(err)
		}
		var result strings.Builder
		for _, file := range files {
			file.OldOIDPrefix, file.NewOIDPrefix = "", ""
			result.WriteString(file.String())
		}
		return result.String()
	}
	if canonical(want) != canonical(got) {
		t.Fatalf("different patch semantics\nWANT:\n%s\nGOT:\n%s", want, got)
	}
}
