package main

import (
	"context"
	"embed"
	"fmt"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"sidekick/coding/lsp"
	"sidekick/env"
)

//go:embed testdata/fixture/* testdata/dependency/*
var fixtureFiles embed.FS

const fixtureDir = "analysisbenchfixture"
const dependencyDir = "analysisbenchdependency"

type workspace struct {
	root   string
	target string
	goroot string
	env    env.EnvContainer
}

func commandOutput(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %q: %w\n%s", name, args, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

func prepareWorkspace(ctx context.Context, root, seed string) (workspace, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return workspace{}, err
	}
	if seed != "" {
		if _, err := commandOutput(ctx, "", "git", "clone", "--quiet", "--shared", seed, root); err != nil {
			return workspace{}, err
		}
	} else {
		if err := os.MkdirAll(root, 0755); err != nil {
			return workspace{}, err
		}
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/sidekick-analysis-bench\n\ngo 1.23.0\n"), 0644); err != nil {
			return workspace{}, err
		}
	}
	for _, mapping := range [][2]string{
		{"testdata/fixture", fixtureDir},
		{"testdata/dependency", dependencyDir},
	} {
		destination := filepath.Join(root, mapping[1])
		if err := os.Mkdir(destination, 0755); err != nil {
			return workspace{}, err
		}
		entries, err := fixtureFiles.ReadDir(mapping[0])
		if err != nil {
			return workspace{}, err
		}
		for _, entry := range entries {
			data, err := fixtureFiles.ReadFile(mapping[0] + "/" + entry.Name())
			if err != nil {
				return workspace{}, err
			}
			name := strings.TrimSuffix(entry.Name(), ".txt")
			if err := os.WriteFile(filepath.Join(destination, name), data, 0644); err != nil {
				return workspace{}, err
			}
		}
	}
	if _, err := commandOutput(ctx, root, "go", "mod", "edit",
		"-require=example.com/analysis-dependency@v0.0.0",
		"-replace=example.com/analysis-dependency=./"+dependencyDir); err != nil {
		return workspace{}, err
	}
	goroot, err := commandOutput(ctx, root, "go", "env", "GOROOT")
	if err != nil {
		return workspace{}, err
	}
	return workspace{
		root: root, target: "Target", goroot: goroot,
		env: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: root}},
	}, nil
}

func (w workspace) mainPath() string {
	return filepath.Join(w.root, fixtureDir, "use.go")
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func symbolPosition(source, marker, symbol string) (lsp.Position, error) {
	if marker == "" || strings.Count(source, marker) != 1 {
		return lsp.Position{}, fmt.Errorf("marker %q must identify exactly one occurrence", marker)
	}
	var positions []lsp.Position
	for lineNumber, line := range strings.Split(source, "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		offset := strings.Index(line, marker)
		inMarker := strings.Index(marker, symbol)
		if inMarker < 0 {
			return lsp.Position{}, fmt.Errorf("marker %q does not contain %q", marker, symbol)
		}
		prefix := line[:offset+inMarker]
		positions = append(positions, lsp.Position{
			Line: lineNumber, Character: len(utf16.Encode([]rune(prefix))),
		})
	}
	if len(positions) != 1 {
		return lsp.Position{}, fmt.Errorf("marker %q matched %d lines, want exactly one", marker, len(positions))
	}
	return positions[0], nil
}

func checkLocation(location lsp.Location, expectedPath, symbol string) error {
	uri, err := url.Parse(location.URI)
	if err != nil {
		return err
	}
	if uri.Scheme != "file" || (uri.Host != "" && uri.Host != "localhost") ||
		filepath.Clean(uri.Path) != filepath.Clean(expectedPath) {
		return fmt.Errorf("definition/reference for %s points to %s, want %s", symbol, location.URI, expectedPath)
	}
	source, err := os.ReadFile(expectedPath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(source), "\n")
	start := location.Range.Start
	if start.Line < 0 || start.Line >= len(lines) || start.Character < 0 {
		return fmt.Errorf("out-of-bounds location for %s: %+v", symbol, location.Range)
	}
	line := lines[start.Line]
	byteOffset, column := len(line), 0
	for offset, r := range line {
		if column == start.Character {
			byteOffset = offset
			break
		}
		column += len(utf16.Encode([]rune{r}))
	}
	if !strings.HasPrefix(line[byteOffset:], symbol) {
		return fmt.Errorf("location for %s does not start at its token: %+v", symbol, location.Range)
	}
	isIdentifier := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	if byteOffset > 0 {
		r, _ := utf8.DecodeLastRuneInString(line[:byteOffset])
		if isIdentifier(r) {
			return fmt.Errorf("location for %s starts inside another identifier", symbol)
		}
	}
	endOffset := byteOffset + len(symbol)
	if endOffset < len(line) {
		r, _ := utf8.DecodeRuneInString(line[endOffset:])
		if isIdentifier(r) {
			return fmt.Errorf("location for %s ends inside another identifier", symbol)
		}
	}
	end := location.Range.End
	if end.Line != start.Line || end.Character != start.Character+len(utf16.Encode([]rune(symbol))) {
		return fmt.Errorf("location for %s has an unexpected range: %+v", symbol, location.Range)
	}
	return nil
}

func checkFixedSource(path string) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return err
	}
	if len(file.Imports) != 1 || file.Imports[0].Path.Value != `"strings"` {
		return fmt.Errorf("autofix did not remove fmt and add strings:\n%s", source)
	}
	if !strings.Contains(string(source), `strings.TrimSpace(" bench ")`) {
		return fmt.Errorf("autofix changed the fixture's behavior:\n%s", source)
	}
	return nil
}
