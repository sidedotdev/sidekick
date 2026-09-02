package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunBreakdown(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		`{"Action":"run","Package":"example.com/pkg","Test":"TestSlow"}`,
		`{"Action":"pass","Package":"example.com/pkg","Test":"TestSlow/sub","Elapsed":1.5}`,
		`{"Action":"pass","Package":"example.com/pkg","Test":"TestSlow","Elapsed":2.5}`,
		`{"Action":"output","Package":"example.com/pkg","Test":"TestBroken","Output":"boom details\n"}`,
		`{"Action":"fail","Package":"example.com/pkg","Test":"TestBroken","Elapsed":0.1}`,
		`{"Action":"skip","Package":"example.com/pkg","Test":"TestSkipped","Elapsed":0}`,
		`{"Action":"fail","Package":"example.com/pkg","Elapsed":2.7}`,
		`{"Action":"pass","Package":"example.com/fast","Elapsed":0.2}`,
		"some non-json noise",
	}, "\n")
	path := filepath.Join(t.TempDir(), "it.json")
	if err := os.WriteFile(path, []byte(stream), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runBreakdown([]string{"it=" + path}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		// totals: 2 packages (1 failed), 2 passed, 1 failed, 1 skipped tests
		"| it | 2 | 1 | 2 | 1 | 1 | 2.9s |",
		// slowest packages includes per-package test count
		"| 2.70s | it | example.com/pkg | fail | 4 |",
		// top-level tests and subtests are ranked separately
		"| 2.50s | it | example.com/pkg | TestSlow | pass |",
		"| 1.50s | it | example.com/pkg | TestSlow/sub | pass |",
		"## Failures (1)",
		"boom details",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "| example.com/fast |") {
		t.Errorf("sub-0.5s package should be omitted from the slowest table:\n%s", out)
	}

	if err := runBreakdown([]string{"nonsense"}, &buf); err == nil {
		t.Error("expected error for malformed label=path arg")
	}
	if err := runBreakdown(nil, &buf); err == nil {
		t.Error("expected error when no streams are given")
	}
}