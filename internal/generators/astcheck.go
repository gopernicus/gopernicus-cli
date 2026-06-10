package generators

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
)

// MethodExistsOnType reports whether a method with the given name is defined on
// the named receiver type anywhere in the file. This checks for method
// declarations like `func (r *TypeName) MethodName(...)` or `func (r TypeName) MethodName(...)`.
//
// Used by the flat generator to skip producing a generated method when the
// developer has already written a custom version in their own file.
func MethodExistsOnType(filePath, typeName, methodName string) (bool, error) {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return false, nil
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, nil, 0)
	if err != nil {
		return false, err
	}

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != methodName {
			continue
		}

		// Check receiver type matches.
		for _, field := range fn.Recv.List {
			if matchesTypeName(field.Type, typeName) {
				return true, nil
			}
		}
	}

	return false, nil
}

// matchesTypeName checks if an AST expression matches the given type name,
// handling both pointer (*T) and value (T) receivers.
func matchesTypeName(expr ast.Expr, name string) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == name
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name == name
		}
	}
	return false
}
