// lint_temporal_test_ports reports test files that resolve the address of
// the host's Temporal server. Inside sandboxes only its read-only proxy is
// reverse-forwarded, so such tests fail there, and elsewhere they pollute a
// shared server. Tests that need a real server start a throwaway one via
// sidekick/temporal/temporaltest; read-only tests resolve the proxy via
// common.GetTemporalDiagnosticHostPort.
package main

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/singlechecker"
)

const commonPackage = "sidekick/common"

// hostServerResolvers are the common helpers that yield the full-API address
// of the host's Temporal server.
var hostServerResolvers = map[string]bool{
	"GetTemporalServerHostPort": true,
	"GetTemporalClientHostPort": true,
	"GetTemporalServerPort":     true,
}

var Analyzer = &analysis.Analyzer{
	Name: "temporaltestports",
	Doc:  "reports tests that resolve the host Temporal server address instead of starting a throwaway server",
	Run:  run,
}

func main() {
	singlechecker.Main(Analyzer)
}

func run(pass *analysis.Pass) (interface{}, error) {
	// The package that defines the resolvers unit-tests them.
	if pass.Pkg.Path() == commonPackage {
		return nil, nil
	}
	for _, file := range pass.Files {
		if !strings.HasSuffix(pass.Fset.Position(file.Pos()).Filename, "_test.go") {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			fn, ok := pass.TypesInfo.Uses[ident].(*types.Func)
			if !ok || fn.Pkg() == nil || fn.Pkg().Path() != commonPackage || !hostServerResolvers[fn.Name()] {
				return true
			}
			pass.Reportf(
				ident.Pos(),
				"%s resolves the host Temporal server, which tests must not dial; start a throwaway server with temporaltest.StartDevServer instead",
				fn.Name(),
			)
			return true
		})
	}
	return nil, nil
}
