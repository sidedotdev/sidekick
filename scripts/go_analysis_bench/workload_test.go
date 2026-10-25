package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sidekick/coding/lsp"
)

func TestPrepareWorkspaceAndLocationChecks(t *testing.T) {
	t.Parallel()
	w, err := prepareWorkspace(context.Background(), filepath.Join(t.TempDir(), "workspace with spaces"), "")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(w.mainPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"example.com/analysis-dependency"`) {
		t.Fatal("fixture must exercise a separate dependency module")
	}
	model, err := os.ReadFile(filepath.Join(w.root, fixtureDir, "model.go"))
	if err != nil {
		t.Fatal(err)
	}
	position, err := symbolPosition(string(model), "func Target(", "Target")
	if err != nil {
		t.Fatal(err)
	}
	valid := lsp.Location{
		URI: fileURI(filepath.Join(w.root, fixtureDir, "model.go")),
		Range: lsp.Range{
			Start: position,
			End:   lsp.Position{Line: position.Line, Character: position.Character + len("Target")},
		},
	}
	if err := checkLocation(valid, filepath.Join(w.root, fixtureDir, "model.go"), "Target"); err != nil {
		t.Fatal(err)
	}
	wrong := valid
	wrong.Range.Start.Character++
	if err := checkLocation(wrong, filepath.Join(w.root, fixtureDir, "model.go"), "Target"); err == nil {
		t.Fatal("accepted a definition pointing inside the wrong token")
	}
	wrong = valid
	wrong.URI = fileURI(filepath.Join(w.root, fixtureDir, "caller.go"))
	if err := checkLocation(wrong, filepath.Join(w.root, fixtureDir, "model.go"), "Target"); err == nil {
		t.Fatal("accepted a definition in the wrong file")
	}
}

func TestSymbolPositionRequiresUnambiguousMarker(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"package p\n", "Target()\nTarget()\n", "Target(); Target()\n"} {
		if _, err := symbolPosition(source, "Target()", "Target"); err == nil {
			t.Fatalf("accepted absent or ambiguous marker in %q", source)
		}
	}
}

func TestBenchmarkIntegration(t *testing.T) {
	if os.Getenv("SIDEKICK_GO_ANALYSIS_INTEGRATION") != "1" {
		t.Skip("set SIDEKICK_GO_ANALYSIS_INTEGRATION=1 to exercise real desktop Go analysis")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cfg := defaultConfig()
	cfg.Output = filepath.Join(t.TempDir(), "run")
	cfg.Workspaces = 2
	cfg.Cycles = 2
	cfg.IdleEvery = 1
	cfg.Idle = 100 * time.Millisecond
	cfg.FinalIdle = 300 * time.Millisecond
	cfg.SampleInterval = 50 * time.Millisecond
	cfg.SteadyWindow = 200 * time.Millisecond
	report, err := runBenchmark(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if report.CorrectnessFailures != 0 || report.CompletedCycles != 2 {
		t.Fatalf("incomplete workload: %+v", report)
	}
	for _, name := range []string{"definition.crossFile", "definition.dependency", "references", "autofix", "freshness.definition", "freshness.references"} {
		if report.Latency[name].Count < 4 {
			t.Errorf("%s did not run in both working directories and cycles: %+v", name, report.Latency[name])
		}
	}
	if report.Memory.PeakRSSBytes == 0 || report.Memory.SteadySamples == 0 {
		t.Fatalf("missing process accounting: %+v", report.Memory)
	}
	for _, name := range []string{"report.json", "memory.jsonl", "operations.jsonl"} {
		info, err := os.Stat(filepath.Join(cfg.Output, name))
		if err != nil || info.Size() == 0 {
			t.Fatalf("missing artifact %s: %v", name, err)
		}
	}
}

func TestReferenceChecksRejectShadowedAndDuplicateResults(t *testing.T) {
	t.Parallel()
	w, err := prepareWorkspace(context.Background(), filepath.Join(t.TempDir(), "workspace"), "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.root, fixtureDir, "caller.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	position, err := symbolPosition(string(data), `return Target() + " other"`, "Target")
	if err != nil {
		t.Fatal(err)
	}
	valid := lsp.Location{
		URI: fileURI(path),
		Range: lsp.Range{
			Start: position,
			End:   lsp.Position{Line: position.Line, Character: position.Character + len("Target")},
		},
	}
	shadowPosition, err := symbolPosition(string(data), `return Target() + " shadow"`, "Target")
	if err != nil {
		t.Fatal(err)
	}
	shadow := valid
	shadow.Range = lsp.Range{Start: shadowPosition, End: lsp.Position{
		Line: shadowPosition.Line, Character: shadowPosition.Character + len("Target"),
	}}
	tests := []struct {
		name      string
		locations []lsp.Location
		fail      bool
	}{
		{"semantic reference", []lsp.Location{valid}, false},
		{"missing reference", nil, true},
		{"duplicate reference", []lsp.Location{valid, valid}, true},
		{"shadowed reference", []lsp.Location{shadow}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			expected := map[string]referenceExpectation{
				fileURI(path): {path: path, position: position},
			}
			err := checkReferenceLocations(tt.locations, expected, "Target")
			if (err != nil) != tt.fail {
				t.Fatalf("unexpected reference validation result: %v", err)
			}
		})
	}
}

func TestCanceledBenchmarkCleansOwnedProcesses(t *testing.T) {
	if os.Getenv("SIDEKICK_GO_ANALYSIS_INTEGRATION") != "1" {
		t.Skip("set SIDEKICK_GO_ANALYSIS_INTEGRATION=1 to run real gopls")
	}
	cfg := defaultConfig()
	cfg.Output = filepath.Join(t.TempDir(), "canceled")
	cfg.Workspaces, cfg.Cycles = 2, 100
	cfg.IdleEvery, cfg.Idle = 1, time.Minute
	cfg.FinalIdle, cfg.SteadyWindow, cfg.SampleInterval = 300*time.Millisecond, 100*time.Millisecond, 25*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := runBenchmark(ctx, cfg)
		result <- err
	}()
	var token string
	timer := time.NewTicker(10 * time.Millisecond)
	defer timer.Stop()
waitForAnalysis:
	for {
		select {
		case err := <-result:
			t.Fatalf("benchmark ended before cancellation: %v", err)
		case <-ctx.Done():
			t.Fatal("analysis never started")
		case <-timer.C:
			data, err := os.ReadFile(filepath.Join(cfg.Output, "operations.jsonl"))
			if err == nil && strings.Contains(string(data), `"name":"definition.crossFile"`) {
				token = os.Getenv(runTokenEnv)
				break waitForAnalysis
			}
		}
	}
	cancel()
	if err := <-result; err == nil {
		t.Fatal("canceled run succeeded")
	}
	if token == "" {
		t.Fatal("did not capture run ownership token")
	}
	all, err := readProcesses()
	if err != nil {
		t.Fatal(err)
	}
	owned, err := ownedProcesses(all, os.Getpid(), token, readProcessEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 0 {
		t.Fatalf("canceled run leaked owned analysis processes: %+v", owned)
	}
}
