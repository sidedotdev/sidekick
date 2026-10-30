// Runs current fuzz seeds against a historical interdiff without changing the
// worktree. Only failing fuzz assertions count as a red check, not build errors.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

func main() {
	revision := flag.String("revision", "", "historical implementation revision")
	run := flag.String("run", "^FuzzInterdiffTrees$", "go test -run expression")
	expect := flag.String("expect", "fail", "expected result: pass or fail")
	verbose := flag.Bool("verbose", false, "print full test diagnostics")
	flag.Parse()
	if *revision == "" || (*expect != "pass" && *expect != "fail") {
		fail(fmt.Errorf("-revision and -expect=pass|fail are required"))
	}
	root, err := os.Getwd()
	check(err)
	dir, err := os.MkdirTemp(filepath.Join(root, ".side", "tmp"), "interdiff-history-")
	check(err)
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		check(os.WriteFile(path, data, 0600))
		return path
	}
	command := func(name string, args ...string) []byte {
		output, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			fail(fmt.Errorf("%s %v: %w\n%s", name, args, err, output))
		}
		return output
	}

	implementation := command("git", "show", *revision+":coding/diffanalysis/interdiff.go")
	historicalModule := command("git", "show", *revision+":go.mod")
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	check(err)
	sums, err := os.ReadFile(filepath.Join(root, "go.sum"))
	check(err)
	modfile := write("history.mod", module)
	write("history.sum", sums)
	dependency := regexp.MustCompile(`(?m)^\s*github.com/google/go-patchutils\s+(\S+)`).FindSubmatch(historicalModule)
	if len(dependency) != 2 {
		fail(fmt.Errorf("historical patchutils dependency not found in %s", *revision))
	}
	command("go", "mod", "edit", "-modfile="+modfile,
		"-require=github.com/google/go-patchutils@"+string(dependency[1]))

	empty := write("empty.go", []byte("package diffanalysis\n"))
	companion := []byte("package diffanalysis\n")
	if !bytes.Contains(implementation, []byte("interdiffContextLines")) {
		// Current tests reference this limit even when the historical code did not.
		companion = append(companion, []byte("const interdiffContextLines = 5\n")...)
	}
	overlay, err := json.Marshal(struct{ Replace map[string]string }{
		Replace: map[string]string{
			filepath.Join(root, "coding/diffanalysis/interdiff.go"):                  write("interdiff.go", implementation),
			filepath.Join(root, "coding/diffanalysis/interdiff_snapshots.go"):        empty,
			filepath.Join(root, "coding/diffanalysis/render.go"):                     write("companion.go", companion),
			filepath.Join(root, "coding/diffanalysis/rebase_review.go"):              empty,
			filepath.Join(root, "coding/diffanalysis/rebase_review_test.go"):         empty,
			filepath.Join(root, "coding/diffanalysis/interdiff_moving_base_test.go"): empty,
		},
	})
	check(err)
	overlayPath := write("overlay.json", overlay)
	cmd := exec.Command("go", "test", "-json", "-count=1", "-mod=mod", "-modfile="+modfile,
		"-overlay="+overlayPath, "./coding/diffanalysis", "-run", *run)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, testErr := cmd.Output()
	write("results.jsonl", output)

	var failed []string
	passed, assertionPasses, assertionFailures := 0, 0, 0
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	for scanner.Scan() {
		var event struct {
			Action, Test, Output string
		}
		check(json.Unmarshal(scanner.Bytes(), &event))
		if strings.HasSuffix(event.Action, "output") && (*verbose || event.Action == "build-output") {
			fmt.Print(event.Output)
		}
		if event.Test != "" {
			switch event.Action {
			case "fail":
				failed = append(failed, event.Test)
				if strings.HasPrefix(event.Test, "FuzzInterdiff") && strings.Count(event.Test, "/") >= 2 {
					assertionFailures++
				}
			case "pass":
				passed++
				if strings.HasPrefix(event.Test, "FuzzInterdiff") && strings.Count(event.Test, "/") >= 2 {
					assertionPasses++
				}
			}
		}
	}
	check(scanner.Err())
	slices.Sort(failed)
	fmt.Printf("\nrevision=%s passed=%d failed=%d assertionFailures=%d artifacts=%s\n",
		*revision, passed, len(failed), assertionFailures, dir)
	for _, name := range failed {
		fmt.Println("FAIL", name)
	}
	if stderr.Len() > 0 {
		fmt.Fprint(os.Stderr, stderr.String())
	}
	if *expect == "fail" && testErr != nil && assertionFailures > 0 {
		return
	}
	if *expect == "pass" && testErr == nil && len(failed) == 0 && assertionPasses > 0 {
		return
	}
	fail(fmt.Errorf("expected %s, observed test error=%v and %d failed tests", *expect, testErr, len(failed)))
}

func check(err error) {
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
