package response

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigratedHandlersUseEnvelope 约束已迁移的文件和管理接口，禁止重新手写业务信封。
// 新增响应必须经过公共封装，格式由类型及测试保证，不依赖人工记住约定。
func TestMigratedHandlersUseEnvelope(t *testing.T) {
	for _, dir := range []string{"../../internal/handler/file", "../../internal/admin"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "JSON" {
					return true
				}
				for _, arg := range call.Args {
					literal, ok := arg.(*ast.CompositeLit)
					if !ok {
						continue
					}
					for _, element := range literal.Elts {
						field, ok := element.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						key, ok := field.Key.(*ast.BasicLit)
						if ok && key.Value == `"code"` {
							t.Errorf("%s：请使用 response 包返回业务消息", fset.Position(call.Pos()))
						}
					}
				}
				return true
			})
		}
	}
}
