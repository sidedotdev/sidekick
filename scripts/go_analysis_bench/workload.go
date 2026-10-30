package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sidekick/coding/lsp"
	"sidekick/env"
)

type workload struct {
	activities *lsp.LSPActivities
	operations *json.Encoder
	latency    *latencyAccumulator
	failures   int
	timeout    time.Duration
}

func (b *workload) measure(ctx context.Context, workspaceIndex, cycle int, name string, fn func(context.Context) error) error {
	op := operation{At: time.Now(), Name: name, Workspace: workspaceIndex, Cycle: cycle}
	opCtx, cancel := context.WithTimeout(ctx, b.timeout)
	err := fn(opCtx)
	cancel()
	op.Duration = time.Since(op.At)
	if err != nil {
		op.Error = err.Error()
		b.failures++
	}
	b.latency.observe(op)
	if writeErr := b.operations.Encode(op); writeErr != nil {
		return errors.Join(err, writeErr)
	}
	if err != nil {
		return fmt.Errorf("workspace %d cycle %d %s: %w", workspaceIndex, cycle, name, err)
	}
	return nil
}

func (b *workload) definition(ctx context.Context, w workspace, relativePath, marker, symbol, expectedPath string) error {
	definitions, err := b.activities.GetSingleFileDefinitions(ctx, lsp.LSPDefinitionLocationsRequest{
		FilePath: relativePath, EnvContainer: &w.env,
		Symbols: []string{symbol}, ReferenceLine: marker,
	})
	if err != nil {
		return err
	}
	if len(definitions) != 1 || definitions[0].Error != "" {
		return fmt.Errorf("want one successful definition of %s, got %+v", symbol, definitions)
	}
	return checkLocation(definitions[0].Location, expectedPath, symbol)
}

func (b *workload) references(ctx context.Context, w workspace, symbol string) error {
	modelPath := filepath.Join(w.root, fixtureDir, "model.go")
	source, err := os.ReadFile(modelPath)
	if err != nil {
		return err
	}
	position, err := symbolPosition(string(source), "func "+symbol+"(", symbol)
	if err != nil {
		return err
	}
	locations, err := b.activities.FindReferencesActivity(ctx, lsp.FindReferencesActivityInput{
		EnvContainer: w.env, RelativeFilePath: filepath.Join(fixtureDir, "model.go"),
		SymbolText: symbol, Range: &lsp.Range{Start: position, End: lsp.Position{
			Line: position.Line, Character: position.Character + len(symbol),
		}},
	})
	if err != nil {
		return err
	}
	expected := make(map[string]referenceExpectation)
	addExpected := func(path, marker string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		pos, err := symbolPosition(string(data), marker, symbol)
		if err != nil {
			return err
		}
		expected[fileURI(path)] = referenceExpectation{path: path, position: pos}
		return nil
	}
	if symbol == "Target" {
		if err := addExpected(filepath.Join(w.root, fixtureDir, "caller.go"), `return Target() + " other"`); err != nil {
			return err
		}
	}
	if w.target == symbol {
		if err := addExpected(w.mainPath(), "return dep.Decorate("+symbol+"())"); err != nil {
			return err
		}
	}
	return checkReferenceLocations(locations, expected, symbol)
}

func (b *workload) notify(ctx context.Context, w workspace, relativePath string, content string) error {
	_, err := b.activities.SyncDocumentActivity(ctx, lsp.SyncDocumentActivityInput{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: w.root}},
		FilePath:     relativePath, Content: content,
	})
	return err
}

func (b *workload) edit(ctx context.Context, w *workspace, cycle int) error {
	target := "Alternate"
	if w.target == target {
		target = "Target"
	}
	template, err := fixtureFiles.ReadFile("testdata/fixture/use.go.txt")
	if err != nil {
		return err
	}
	content := strings.Replace(string(template), "Target()", target+"()", 1)
	content = strings.Replace(content, "func Use()", strings.Repeat("\n", cycle%3)+"func Use()", 1)
	if err := os.WriteFile(w.mainPath(), []byte(content), 0644); err != nil {
		return err
	}
	if err := b.notify(ctx, *w, filepath.Join(fixtureDir, "use.go"), content); err != nil {
		return err
	}
	w.target = target
	return nil
}

func (b *workload) autofix(ctx context.Context, w workspace) error {
	relativePath := filepath.Join(fixtureDir, "fix.go")
	path := filepath.Join(w.root, relativePath)
	content, err := fixtureFiles.ReadFile("testdata/fixture/fix.go.txt")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		return err
	}
	if err := b.notify(ctx, w, relativePath, string(content)); err != nil {
		return err
	}
	output, err := b.activities.AutofixActivity(ctx, lsp.AutofixActivityInput{
		EnvContainer: w.env, DocumentURI: fileURI(path),
	})
	if err != nil {
		return err
	}
	if len(output.FailedEdits) > 0 || len(output.AppliedEdits) == 0 {
		return fmt.Errorf("autofix did not apply successfully: %+v", output)
	}
	if err := checkFixedSource(path); err != nil {
		return err
	}
	fixed, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return b.notify(ctx, w, relativePath, string(fixed))
}

func (b *workload) cycle(ctx context.Context, w *workspace, workspaceIndex, cycle int) error {
	measure := func(name string, fn func(context.Context) error) error {
		return b.measure(ctx, workspaceIndex, cycle, name, fn)
	}
	modelPath := filepath.Join(w.root, fixtureDir, "model.go")
	if err := measure("definition.crossFile", func(ctx context.Context) error {
		return b.definition(ctx, *w, filepath.Join(fixtureDir, "use.go"),
			"return dep.Decorate("+w.target+"())", w.target, modelPath)
	}); err != nil {
		return err
	}
	if err := measure("definition.dependency", func(ctx context.Context) error {
		return b.definition(ctx, *w, filepath.Join(fixtureDir, "use.go"),
			"return dep.Decorate(", "Decorate", filepath.Join(w.root, dependencyDir, "dependency.go"))
	}); err != nil {
		return err
	}
	if err := measure("references", func(ctx context.Context) error {
		return b.references(ctx, *w, "Target")
	}); err != nil {
		return err
	}
	if err := measure("edit.sync", func(ctx context.Context) error {
		return b.edit(ctx, w, cycle)
	}); err != nil {
		return err
	}
	if err := measure("freshness.definition", func(ctx context.Context) error {
		return b.definition(ctx, *w, filepath.Join(fixtureDir, "use.go"),
			"return dep.Decorate("+w.target+"())", w.target, modelPath)
	}); err != nil {
		return err
	}
	if err := measure("freshness.references", func(ctx context.Context) error {
		if err := b.references(ctx, *w, "Target"); err != nil {
			return err
		}
		return b.references(ctx, *w, "Alternate")
	}); err != nil {
		return err
	}
	if err := measure("autofix", func(ctx context.Context) error {
		return b.autofix(ctx, *w)
	}); err != nil {
		return err
	}
	return measure("definition.stdlib", func(ctx context.Context) error {
		return b.definition(ctx, *w, filepath.Join(fixtureDir, "fix.go"),
			`return strings.TrimSpace(" bench ")`, "TrimSpace",
			filepath.Join(w.goroot, "src", "strings", "strings.go"))
	})
}

type actionRecordingClient struct {
	lsp.LSPClient
	log *json.Encoder
}

func (c *actionRecordingClient) TextDocumentCodeAction(ctx context.Context, params lsp.CodeActionParams) ([]lsp.CodeAction, error) {
	actions, err := c.LSPClient.TextDocumentCodeAction(ctx, params)
	record := struct {
		At      time.Time            `json:"at"`
		Request lsp.CodeActionParams `json:"request"`
		Actions []lsp.CodeAction     `json:"actions"`
		Error   string               `json:"error,omitempty"`
	}{At: time.Now(), Request: params, Actions: actions}
	if err != nil {
		record.Error = err.Error()
	}
	if writeErr := c.log.Encode(record); writeErr != nil {
		return nil, errors.Join(err, writeErr)
	}
	want := []lsp.CodeActionKind{lsp.CodeActionKindSourceFixAll, lsp.CodeActionKindSourceOrganizeImports}
	if len(params.Context.Only) != len(want) || params.Context.Only[0] != want[0] || params.Context.Only[1] != want[1] {
		return nil, fmt.Errorf("desktop autofix action kinds changed: %v", params.Context.Only)
	}
	return actions, err
}

type referenceExpectation struct {
	path     string
	position lsp.Position
}

func checkReferenceLocations(locations []lsp.Location, expected map[string]referenceExpectation, symbol string) error {
	if len(locations) != len(expected) {
		return fmt.Errorf("%s references: got %+v, want %v", symbol, locations, expected)
	}
	for _, location := range locations {
		want, exists := expected[location.URI]
		if !exists || location.Range.Start != want.position {
			return fmt.Errorf("unexpected reference for %s: %+v; want %v", symbol, location, expected)
		}
		if err := checkLocation(location, want.path, symbol); err != nil {
			return err
		}
		delete(expected, location.URI)
	}
	return nil
}

func (c *actionRecordingClient) Initialize(ctx context.Context, params lsp.InitializeParams) (lsp.InitializeResponse, error) {
	// The activity injects environments only into concrete JSON-RPC clients.
	if client, ok := c.LSPClient.(*lsp.Jsonrpc2LSPClient); ok {
		root, err := url.Parse(params.RootURI)
		if err != nil {
			return lsp.InitializeResponse{}, err
		}
		if root.Scheme != "file" || root.Host != "" {
			return lsp.InitializeResponse{}, fmt.Errorf("benchmark requires a local file workspace: %s", params.RootURI)
		}
		client.EnvContainer = &env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: root.Path}}
	}
	return c.LSPClient.Initialize(ctx, params)
}

func (c *actionRecordingClient) Close() error {
	if client, ok := c.LSPClient.(interface{ Close() error }); ok {
		return client.Close()
	}
	return nil
}
