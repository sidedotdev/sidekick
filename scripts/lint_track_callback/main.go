package main

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/singlechecker"
)

// trackFuncNames are the function names whose callbacks must not reference
// outer context variables.
var trackFuncNames = map[string]bool{
	"Track":            true,
	"TrackHuman":       true,
	"TrackFailureOnly": true,
	"TrackWithOptions": true,
}

// contextTypeNames are type names that should not be captured from the outer
// scope inside Track callbacks.
var contextTypeNames = map[string]bool{
	"ExecContext":      true,
	"ActionContext":    true,
	"DevContext":       true,
	"DevActionContext": true,
}

// contextQualifiedSuffixes are qualified type suffixes (e.g. "workflow.Context")
// checked against the full type string to catch types that are too generic to
// match by short name alone.
var contextQualifiedSuffixes = []string{
	"workflow.Context",
}

var Analyzer = &analysis.Analyzer{
	Name: "noouterctrxintrack",
	Doc:  "reports outer context variables (ExecContext, ActionContext, DevContext, DevActionContext, workflow.Context) referenced inside Track callbacks",
	Run:  run,
}

func main() {
	singlechecker.Main(Analyzer)
}

func run(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		checkFile(pass, file)
	}
	return nil, nil
}

func checkFile(pass *analysis.Pass, file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		funcName := trackCallName(call, pass.TypesInfo)
		if funcName == "" {
			return true
		}

		funcLit := extractCallbackArg(call)
		if funcLit == nil {
			return true
		}

		// Collect the callback's own parameter objects so we don't flag them
		callbackParams := make(map[*ast.Object]bool)
		if funcLit.Type.Params != nil {
			for _, field := range funcLit.Type.Params.List {
				for _, name := range field.Names {
					if name.Obj != nil {
						callbackParams[name.Obj] = true
					}
				}
			}
		}

		// Find the enclosing function to collect outer context-typed variables
		enclosing := findEnclosingFunc(file, call)
		if enclosing == nil {
			return true
		}

		// Skip Track wrapper functions that intentionally capture the outer
		// context to re-wrap it with the tracked context
		if isTrackWrapper(enclosing, pass.TypesInfo) {
			return true
		}

		outerCtxVars := collectOuterContextVars(enclosing, funcLit, pass.TypesInfo, callbackParams)
		if len(outerCtxVars) == 0 {
			return true
		}

		// Walk the callback body for references to outer context vars
		ast.Inspect(funcLit.Body, func(inner ast.Node) bool {
			ident, ok := inner.(*ast.Ident)
			if !ok {
				return true
			}
			if outerCtxVars[ident.Obj] {
				pass.Reportf(ident.Pos(), "%q referenced in Track callback (in %s); use the tracked context parameter instead", ident.Name, funcName)
			}
			return true
		})

		return true
	})
}

// trackCallName returns the Track* function name if this call targets one,
// or empty string otherwise. Uses type info to resolve the actual function.
func trackCallName(call *ast.CallExpr, info *types.Info) string {
	var funcObj types.Object

	switch fn := call.Fun.(type) {
	case *ast.Ident:
		funcObj = info.ObjectOf(fn)
	case *ast.SelectorExpr:
		funcObj = info.ObjectOf(fn.Sel)
	default:
		return ""
	}

	if funcObj == nil {
		return ""
	}

	name := funcObj.Name()
	if !trackFuncNames[name] {
		return ""
	}

	// Verify it's from one of our packages
	if funcObj.Pkg() == nil {
		return ""
	}
	pkgPath := funcObj.Pkg().Path()
	if pkgPath != "sidekick/flow_action" && pkgPath != "sidekick/dev" {
		return ""
	}

	return name
}

// extractCallbackArg finds the func literal argument in a Track* call.
func extractCallbackArg(call *ast.CallExpr) *ast.FuncLit {
	for i := len(call.Args) - 1; i >= 0; i-- {
		if lit, ok := call.Args[i].(*ast.FuncLit); ok {
			return lit
		}
	}
	return nil
}

// findEnclosingFunc finds the FuncDecl or FuncLit that directly contains the node.
func findEnclosingFunc(file *ast.File, target ast.Node) ast.Node {
	var result ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if containsNode(fn, target) {
				result = fn
			}
		case *ast.FuncLit:
			if containsNode(fn, target) {
				result = fn
			}
		}
		return true
	})
	return result
}

// isTrackWrapper returns true when the enclosing function is itself a Track
// wrapper (e.g. dev.Track, dev.TrackHuman) that intentionally captures the
// outer context to re-wrap it.
func isTrackWrapper(enclosing ast.Node, info *types.Info) bool {
	decl, ok := enclosing.(*ast.FuncDecl)
	if !ok {
		return false
	}
	if !trackFuncNames[decl.Name.Name] {
		return false
	}
	obj := info.ObjectOf(decl.Name)
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	pkgPath := obj.Pkg().Path()
	return pkgPath == "sidekick/flow_action" || pkgPath == "sidekick/dev"
}

func containsNode(parent, child ast.Node) bool {
	return parent.Pos() <= child.Pos() && child.End() <= parent.End()
}

// collectOuterContextVars collects ast.Objects of variables declared in the
// enclosing function (but not inside the callback) whose type is one of the
// context types we care about.
func collectOuterContextVars(enclosing ast.Node, callback *ast.FuncLit, info *types.Info, callbackParams map[*ast.Object]bool) map[*ast.Object]bool {
	result := make(map[*ast.Object]bool)

	// Collect from the enclosing function's params
	var params *ast.FieldList
	switch fn := enclosing.(type) {
	case *ast.FuncDecl:
		params = fn.Type.Params
		// Also check receiver
		if fn.Recv != nil {
			for _, field := range fn.Recv.List {
				for _, name := range field.Names {
					if name.Obj != nil && !callbackParams[name.Obj] && isContextType(info, name) {
						result[name.Obj] = true
					}
				}
			}
		}
	case *ast.FuncLit:
		params = fn.Type.Params
	}

	if params != nil {
		for _, field := range params.List {
			for _, name := range field.Names {
				if name.Obj != nil && !callbackParams[name.Obj] && isContextType(info, name) {
					result[name.Obj] = true
				}
			}
		}
	}

	// Walk the enclosing function body for local variable declarations,
	// but skip anything inside the callback itself
	var body *ast.BlockStmt
	switch fn := enclosing.(type) {
	case *ast.FuncDecl:
		body = fn.Body
	case *ast.FuncLit:
		body = fn.Body
	}
	if body == nil {
		return result
	}

	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			return true
		}
		// Don't descend into the callback itself
		if n == callback {
			return false
		}
		// Don't descend into other func literals
		if _, ok := n.(*ast.FuncLit); ok {
			if n != enclosing {
				return false
			}
		}

		switch node := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok {
					if ident.Obj != nil && !callbackParams[ident.Obj] && isContextType(info, ident) {
						result[ident.Obj] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, name := range node.Names {
				if name.Obj != nil && !callbackParams[name.Obj] && isContextType(info, name) {
					result[name.Obj] = true
				}
			}
		}
		return true
	})

	return result
}

// isContextType checks whether the identifier's type (from type info) is one
// of the context types we flag.
func isContextType(info *types.Info, ident *ast.Ident) bool {
	obj := info.ObjectOf(ident)
	if obj == nil {
		return false
	}
	return isContextTypeName(obj.Type())
}

func isContextTypeName(t types.Type) bool {
	if t == nil {
		return false
	}

	typStr := t.String()
	// Check for pointer types too
	if ptr, ok := t.(*types.Pointer); ok {
		typStr = ptr.Elem().String()
	}

	// Extract the type name (last component after '/')
	name := typStr
	if idx := strings.LastIndex(typStr, "."); idx >= 0 {
		name = typStr[idx+1:]
	}
	if contextTypeNames[name] {
		return true
	}

	for _, suffix := range contextQualifiedSuffixes {
		if strings.HasSuffix(typStr, suffix) {
			return true
		}
	}
	return false
}