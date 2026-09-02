package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

type event struct {
	Action  string
	Package string
	Test    string
	Elapsed float64
	Output  string
}

type pkgRow struct {
	label, pkg, action string
	elapsed            float64
	tests              int
}

type testRow struct {
	label, pkg, test, action string
	elapsed                  float64
}

type failRow struct {
	label, pkg, test string
	out              []string
}

type counts struct {
	pass, fail, skip, pkgs, pkgFail int
	sumPkgElapsed                   float64
}

const maxFailLines = 40

// runBreakdown parses one `go test -json` stream per label=path argument and
// writes markdown tables: per-suite totals, slowest packages, slowest
// top-level tests, slowest subtests and failure details.
func runBreakdown(args []string, w io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: -breakdown label=path [label=path ...]")
	}

	var (
		pkgs   []pkgRow
		tests  []testRow
		fails  []failRow
		labels []string
	)
	stats := map[string]*counts{}

	for _, arg := range args {
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("bad arg %q (want label=path)", arg)
		}
		label, path := parts[0], parts[1]
		labels = append(labels, label)
		st := &counts{}
		stats[label] = st

		f, err := os.Open(path)
		if err != nil {
			// A missing stream (e.g. an interrupted run) should not hide the
			// other suites' results.
			fmt.Fprintf(os.Stderr, "open %s: %v\n", path, err)
			continue
		}
		testsPerPkg := map[string]int{}
		pendingOut := map[string][]string{}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<26)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var ev event
			if json.Unmarshal([]byte(line), &ev) != nil {
				continue
			}
			key := ev.Package + "\x00" + ev.Test
			switch ev.Action {
			case "output":
				if ev.Test == "" {
					continue
				}
				if len(pendingOut[key]) < maxFailLines {
					pendingOut[key] = append(pendingOut[key], strings.TrimRight(ev.Output, "\n"))
				}
			case "pass", "fail", "skip":
				if ev.Test == "" {
					st.pkgs++
					if ev.Action == "fail" {
						st.pkgFail++
					}
					st.sumPkgElapsed += ev.Elapsed
					pkgs = append(pkgs, pkgRow{label: label, pkg: ev.Package, action: ev.Action, elapsed: ev.Elapsed})
					continue
				}
				testsPerPkg[ev.Package]++
				tests = append(tests, testRow{label: label, pkg: ev.Package, test: ev.Test, action: ev.Action, elapsed: ev.Elapsed})
				switch ev.Action {
				case "pass":
					st.pass++
				case "skip":
					st.skip++
				case "fail":
					st.fail++
					fails = append(fails, failRow{label: label, pkg: ev.Package, test: ev.Test, out: pendingOut[key]})
				}
				delete(pendingOut, key)
			}
		}
		if err := sc.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", path, err)
		}
		f.Close()
		for i := range pkgs {
			if pkgs[i].label == label {
				pkgs[i].tests = testsPerPkg[pkgs[i].pkg]
			}
		}
	}

	fmt.Fprintln(w, "## Go test totals")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| command | packages | failed pkgs | tests passed | failed | skipped | Σ package elapsed |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|")
	for _, l := range labels {
		st := stats[l]
		fmt.Fprintf(w, "| %s | %d | %d | %d | %d | %d | %.1fs |\n", l, st.pkgs, st.pkgFail, st.pass, st.fail, st.skip, st.sumPkgElapsed)
	}
	fmt.Fprintln(w)

	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].elapsed != pkgs[j].elapsed {
			return pkgs[i].elapsed > pkgs[j].elapsed
		}
		return pkgs[i].pkg < pkgs[j].pkg
	})
	fmt.Fprintln(w, "## Slowest packages (descending, elapsed >= 0.5s)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| elapsed | command | package | result | tests |")
	fmt.Fprintln(w, "|---|---|---|---|---|")
	hidden := 0
	for _, p := range pkgs {
		if p.elapsed < 0.5 {
			hidden++
			continue
		}
		fmt.Fprintf(w, "| %.2fs | %s | %s | %s | %d |\n", p.elapsed, p.label, p.pkg, p.action, p.tests)
	}
	fmt.Fprintf(w, "\n%d package result(s) below 0.5s omitted.\n\n", hidden)

	topN := func(title string, rows []testRow, limit int) {
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].elapsed != rows[j].elapsed {
				return rows[i].elapsed > rows[j].elapsed
			}
			return rows[i].test < rows[j].test
		})
		fmt.Fprintf(w, "## %s\n\n", title)
		fmt.Fprintln(w, "| elapsed | command | package | test | result |")
		fmt.Fprintln(w, "|---|---|---|---|---|")
		for i, r := range rows {
			if i >= limit {
				fmt.Fprintf(w, "\n%d more test(s) at or below %.2fs omitted.\n", len(rows)-limit, r.elapsed)
				break
			}
			fmt.Fprintf(w, "| %.2fs | %s | %s | %s | %s |\n", r.elapsed, r.label, r.pkg, r.test, r.action)
		}
		fmt.Fprintln(w)
	}

	var parents, subs []testRow
	for _, t := range tests {
		if strings.Contains(t.test, "/") {
			subs = append(subs, t)
		} else {
			parents = append(parents, t)
		}
	}
	topN("Slowest top-level tests (includes subtest time)", parents, 100)
	topN("Slowest subtests", subs, 60)

	if len(fails) == 0 {
		fmt.Fprintln(w, "## Failures")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "None.")
		return nil
	}
	fmt.Fprintf(w, "## Failures (%d)\n\n", len(fails))
	for _, f := range fails {
		fmt.Fprintf(w, "### %s · %s · %s\n\n```\n%s\n```\n\n", f.label, f.pkg, f.test, strings.Join(f.out, "\n"))
	}
	return nil
}