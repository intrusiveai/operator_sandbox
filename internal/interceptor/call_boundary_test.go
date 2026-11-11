package interceptor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A new native call must inherit capture/primary-before-retention semantics.
// NativeSteps.Execute is the sole separate transaction boundary: it owns durable
// dispatch and outcome records and attaches Failure to its Result centrally.
func TestNativeCallsUseSharedDiagnosticBoundary(t *testing.T) {
	for _, dir := range []string{"../campaignservice", "../nativerecovery", "../nativeexec"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			name := filepath.Join(dir, entry.Name())
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				var stack []ast.Node
				ast.Inspect(fn, func(n ast.Node) bool {
					if n == nil {
						stack = stack[:len(stack)-1]
						return false
					}
					stack = append(stack, n)
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Execute" && sel.Sel.Name != "ExecuteLifecycle" {
						return true
					}
					// Only the Interceptor peer, not model providers or tool executors.
					peer := false
					switch receiver := sel.X.(type) {
					case *ast.Ident:
						peer = receiver.Name == "peer"
					case *ast.SelectorExpr:
						peer = receiver.Sel.Name == "peer" || receiver.Sel.Name == "Peer"
					}
					if !peer {
						return true
					}
					if dir == "../nativeexec" && entry.Name() == "executor.go" && fn.Name.Name == "Execute" {
						return true
					}
					for _, parent := range stack[:len(stack)-1] {
						if c, ok := parent.(*ast.CallExpr); ok {
							if s, ok := c.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Call" {
								if p, ok := s.X.(*ast.Ident); ok && p.Name == "interceptor" {
									return true
								}
							}
						}
					}
					t.Errorf("%s: native call bypasses interceptor.Call", set.Position(call.Pos()))
					return true
				})
			}
		}
	}
}
