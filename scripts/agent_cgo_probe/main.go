// Command agent_cgo_probe links the tree-sitter parser into a tiny binary so
// that cross-compiling a CGO side-agent can be verified without the agent's
// other dependencies getting in the way. Example:
//
//	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC="zig cc -target x86_64-linux-musl" \
//	  go build -o .side/tmp/probe ./scripts/agent_cgo_probe
package main

import (
	"fmt"
	"os"

	"sidekick/coding/tree_sitter/parser"
)

func main() {
	symbols, err := parser.GetFileSymbolsFromBytes("probe.go", "golang", []byte("package probe\n\nfunc Target() {}\n"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("parsed %d symbol(s)\n", len(symbols))
}
