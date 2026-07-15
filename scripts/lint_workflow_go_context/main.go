package main

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/singlechecker"
)

const temporalWorkflowPackage = "go.temporal.io/sdk/workflow"

var Analyzer = &analysis.Analyzer{
	Name: "workflowgocontext",
	Doc:  "reports workflow.Go calls whose parent is a custom workflow context wrapper",
	Run:  run,
}

func main() {
	singlechecker.Main(Analyzer)
}

func run(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}

			goFunc := calledFunction(pass.TypesInfo, call.Fun)
			if !isTemporalWorkflowGo(goFunc) {
				return true
			}

			signature, ok := goFunc.Type().(*types.Signature)
			if !ok || signature.Params().Len() == 0 {
				return true
			}

			parentType := pass.TypesInfo.TypeOf(call.Args[0])
			workflowContextType := signature.Params().At(0).Type()
			if parentType == nil || types.Identical(parentType, workflowContextType) {
				return true
			}

			pass.Reportf(
				call.Args[0].Pos(),
				"workflow.Go parent has type %s; pass the embedded workflow.Context explicitly",
				types.TypeString(parentType, types.RelativeTo(pass.Pkg)),
			)
			return true
		})
	}
	return nil, nil
}

func calledFunction(info *types.Info, expression ast.Expr) *types.Func {
	switch expression := expression.(type) {
	case *ast.Ident:
		fn, _ := info.ObjectOf(expression).(*types.Func)
		return fn
	case *ast.SelectorExpr:
		fn, _ := info.ObjectOf(expression.Sel).(*types.Func)
		return fn
	default:
		return nil
	}
}

func isTemporalWorkflowGo(fn *types.Func) bool {
	return fn != nil &&
		fn.Name() == "Go" &&
		fn.Pkg() != nil &&
		fn.Pkg().Path() == temporalWorkflowPackage
}
