package lsp

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sidekick/env"

	"github.com/stretchr/testify/require"
)

type semanticSessionClient struct {
	*Jsonrpc2LSPClient
	closed atomic.Int32
	kinds  *[][]CodeActionKind
}

func (c *semanticSessionClient) Close() error {
	c.closed.Add(1)
	return c.Jsonrpc2LSPClient.Close()
}

func (c *semanticSessionClient) TextDocumentCodeAction(ctx context.Context, params CodeActionParams) ([]CodeAction, error) {
	*c.kinds = append(*c.kinds, append([]CodeActionKind(nil), params.Context.Only...))
	return c.Jsonrpc2LSPClient.TextDocumentCodeAction(ctx, params)
}

func TestGoSemanticsAcrossSessionRecycling(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"workspace", "idle", "age"} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(dir, "dependency"), 0755))
			files := map[string]string{
				"go.mod":            "module example.com/fixture\n\ngo 1.23\n\nrequire example.com/dependency v0.0.0\nreplace example.com/dependency => ./dependency\n",
				"model.go":          "package fixture\n\nfunc Target() {}\nfunc Other() {}\n",
				"shadow.go":         "package fixture\n\nfunc Shadow() { Target := func() {}; Target() }\n",
				"dependency/go.mod": "module example.com/dependency\n\ngo 1.23\n",
				"dependency/dep.go": "package dependency\n\nfunc Echo() {}\n",
			}
			for path, content := range files {
				require.NoError(t, os.WriteFile(filepath.Join(dir, path), []byte(content), 0644))
			}
			var clients []*semanticSessionClient
			var requestedKinds [][]CodeActionKind
			activities := NewLSPActivities(func(language string) LSPClient {
				client := &semanticSessionClient{
					Jsonrpc2LSPClient: &Jsonrpc2LSPClient{LanguageName: language},
					kinds:             &requestedKinds,
				}
				clients = append(clients, client)
				return client
			})
			activities.IdleTimeout = time.Hour
			activities.MaxClientAge = time.Hour
			t.Cleanup(func() { require.NoError(t, activities.Close()) })
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			container := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: dir}}

			checkDefinition := func(symbol, path string) {
				t.Helper()
				definitions, err := activities.GetSingleFileDefinitions(ctx, LSPDefinitionLocationsRequest{
					EnvContainer: &container, FilePath: "use.go", Symbols: []string{symbol},
				})
				require.NoError(t, err)
				require.Len(t, definitions, 1, "%s", symbol)
				require.Empty(t, definitions[0].Error)
				uri, err := url.Parse(definitions[0].Location.URI)
				require.NoError(t, err)
				require.Equal(t, filepath.Join(dir, path), uri.Path)
				content, err := os.ReadFile(uri.Path)
				require.NoError(t, err)
				position := definitions[0].Location.Range.Start
				lines := strings.Split(string(content), "\n")
				require.Less(t, position.Line, len(lines))
				require.Contains(t, lines[position.Line], "func "+symbol+"()")
			}

			for round, symbol := range []string{"Target", "Other", "Target"} {
				source := fmt.Sprintf("package fixture\n\nimport \"example.com/dependency\"\n\n%sfunc Caller() { dependency.Echo(); %s() }\n", strings.Repeat("\n", round*3), symbol)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "use.go"), []byte(source), 0644))
				_, err := activities.SyncDocumentActivity(ctx, SyncDocumentActivityInput{
					EnvContainer: container, FilePath: "use.go", Content: source,
				})
				require.NoError(t, err)
				checkDefinition(symbol, "model.go")
				checkDefinition("Echo", "dependency/dep.go")

				for _, name := range []string{"Target", "Other"} {
					references, err := activities.FindReferencesActivity(ctx, FindReferencesActivityInput{
						EnvContainer: container, RelativeFilePath: "model.go", SymbolText: name,
					})
					require.NoError(t, err)
					if name != symbol {
						require.Empty(t, references, "old references must disappear after an edit")
						continue
					}
					line := "func Caller() { dependency.Echo(); " + symbol + "() }"
					character := strings.Index(line, symbol)
					require.Equal(t, []Location{{
						URI: (&url.URL{Scheme: "file", Path: filepath.Join(dir, "use.go")}).String(),
						Range: Range{
							Start: Position{Line: 4 + round*3, Character: character},
							End:   Position{Line: 4 + round*3, Character: character + len(symbol)},
						},
					}}, references, "same-spelled local symbols must not count as references")
				}

				broken := "package fixture\n\nimport \"fmt\"\n\nfunc Clean(s string) string { return strings.TrimSpace(s) }\n"
				require.NoError(t, os.WriteFile(filepath.Join(dir, "fix.go"), []byte(broken), 0644))
				_, err = activities.SyncDocumentActivity(ctx, SyncDocumentActivityInput{
					EnvContainer: container, FilePath: "fix.go", Content: broken,
				})
				require.NoError(t, err)
				output, err := activities.AutofixActivity(ctx, AutofixActivityInput{
					EnvContainer: container,
					DocumentURI:  (&url.URL{Scheme: "file", Path: filepath.Join(dir, "fix.go")}).String(),
				})
				require.NoError(t, err)
				require.Empty(t, output.FailedEdits)
				require.Empty(t, output.SkippedCodeActions)
				require.NotEmpty(t, output.AppliedEdits)
				fixed, err := os.ReadFile(filepath.Join(dir, "fix.go"))
				require.NoError(t, err)
				require.Contains(t, string(fixed), "import \"strings\"")
				require.NotContains(t, string(fixed), "\"fmt\"")

				if round != 0 {
					continue
				}
				first := clients[0]
				switch reason {
				case "workspace":
					other := t.TempDir()
					require.NoError(t, os.WriteFile(filepath.Join(other, "go.mod"), []byte("module example.com/other\n\ngo 1.23\n"), 0644))
					require.NoError(t, os.WriteFile(filepath.Join(other, "main.go"), []byte("package other\nfunc Target() {}\n"), 0644))
					_, err := activities.GetSingleFileDefinitions(ctx, definitionRequest(other))
					require.NoError(t, err)
				case "idle":
					_, _, release, err := activities.findOrInitClient(ctx, dir, "golang", &container)
					require.NoError(t, err)
					activities.IdleTimeout = 10 * time.Millisecond
					release()
					require.Eventually(t, func() bool { return first.closed.Load() == 1 }, 5*time.Second, time.Millisecond)
					activities.IdleTimeout = time.Hour
				case "age":
					activities.MaxClientAge = time.Nanosecond
					checkDefinition(symbol, "model.go")
					activities.MaxClientAge = time.Hour
				}
				require.EqualValues(t, 1, first.closed.Load())
			}
			require.GreaterOrEqual(t, len(clients), 2)
			require.Len(t, requestedKinds, 3)
			for _, kinds := range requestedKinds {
				require.Equal(t, []CodeActionKind{CodeActionKindSourceFixAll, CodeActionKindSourceOrganizeImports}, kinds)
			}
		})
	}
}
