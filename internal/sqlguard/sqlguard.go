// Package sqlguard is the standing SQL-injection guard for store packages:
// the crud engine and the generators only ever build SQL from literals and
// sanctioned builders (Args.Add placeholders, Dialect.QuoteIdent,
// strings.Join over built clauses), so any other dynamic value concatenated
// into SQL-bearing strings under core/repositories/ is worth flagging before
// it ships. Heuristic by design — it inspects syntax, not taint.
package sqlguard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

// sqlKeyword marks a string literal as SQL-bearing.
var sqlKeyword = regexp.MustCompile(`(?i)\b(select|insert|update|delete|where|from)\b`)

// sanctionedCalls are builder methods whose results are safe to concatenate
// into SQL: placeholder generators, identifier quoting, and clause joining.
var sanctionedCalls = map[string]bool{
	"Add":        true, // crud.Args.Add — renders a placeholder
	"QuoteIdent": true, // crud.Dialect.QuoteIdent
	"Join":       true, // strings.Join over already-built clauses
	"String":     true, // bytes.Buffer.String / strings.Builder.String
}

// Finding is one suspicious SQL construction site.
type Finding struct {
	Pos     string // file:line
	Kind    string // "concat" or "raw"
	Message string
}

// Scan walks every non-test Go file under root/core/repositories and reports
// unsanctioned dynamic SQL concatenation and crud.Pred Raw usage. Files are
// grouped per package directory so package-level constants — compile-time
// values, injection-safe by definition — are sanctioned across the package.
func Scan(root string) ([]Finding, error) {
	reposDir := filepath.Join(root, "core", "repositories")
	dirs := map[string][]string{}

	err := filepath.WalkDir(reposDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d == nil {
				return nil // core/repositories absent — nothing to scan
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		dirs[dir] = append(dirs[dir], path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	var findings []Finding
	for _, files := range dirs {
		dirFindings, err := scanPackageDir(files)
		if err != nil {
			return nil, err
		}
		findings = append(findings, dirFindings...)
	}
	return findings, nil
}

// scanPackageDir parses every file of one package directory, collects its
// package-level const names, then inspects each file with that allowlist.
func scanPackageDir(paths []string) ([]Finding, error) {
	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(paths))
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		files = append(files, file)
	}

	consts := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					for _, name := range vs.Names {
						consts[name.Name] = true
					}
				}
			}
		}
	}

	var findings []Finding
	for _, file := range files {
		findings = append(findings, inspectFile(fset, file, consts)...)
	}
	return findings, nil
}

func inspectFile(fset *token.FileSet, file *ast.File, consts map[string]bool) []Finding {
	var findings []Finding
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.BinaryExpr:
			if node.Op != token.ADD {
				return true
			}
			operands := flattenConcat(node)
			if !containsSQLLiteral(operands) {
				return true
			}
			for _, op := range operands {
				if expr := unsanctionedOperand(op, consts); expr != "" {
					findings = append(findings, Finding{
						Pos:  fset.Position(op.Pos()).String(),
						Kind: "concat",
						Message: fmt.Sprintf(
							"dynamic value %q concatenated into SQL — use a placeholder (args.Add) or QuoteIdent",
							expr,
						),
					})
				}
			}
			return false // operands already inspected
		case *ast.KeyValueExpr:
			if key, ok := node.Key.(*ast.Ident); ok && key.Name == "Raw" {
				findings = append(findings, Finding{
					Pos:     fset.Position(node.Pos()).String(),
					Kind:    "raw",
					Message: "Pred.Raw escape hatch in use — verify the raw SQL takes no request-derived input",
				})
			}
		}
		return true
	})
	return findings
}

// flattenConcat returns the leaf operands of a (possibly nested) + chain.
func flattenConcat(expr ast.Expr) []ast.Expr {
	if bin, ok := expr.(*ast.BinaryExpr); ok && bin.Op == token.ADD {
		return append(flattenConcat(bin.X), flattenConcat(bin.Y)...)
	}
	return []ast.Expr{expr}
}

func containsSQLLiteral(operands []ast.Expr) bool {
	for _, op := range operands {
		if lit, ok := op.(*ast.BasicLit); ok && lit.Kind == token.STRING && sqlKeyword.MatchString(lit.Value) {
			return true
		}
	}
	return false
}

// unsanctionedOperand returns a printable form of the operand when it is a
// dynamic value outside the sanctioned builder set, or "" when it is safe.
// Package-level consts are safe: they are compile-time values.
func unsanctionedOperand(op ast.Expr, consts map[string]bool) string {
	switch v := op.(type) {
	case *ast.BasicLit:
		return ""
	case *ast.Ident:
		if consts[v.Name] {
			return ""
		}
		return v.Name
	case *ast.CallExpr:
		if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sanctionedCalls[sel.Sel.Name] {
			return ""
		}
		return exprString(v)
	default:
		return exprString(op)
	}
}

func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	case *ast.CallExpr:
		return exprString(v.Fun) + "(...)"
	default:
		return fmt.Sprintf("%T", e)
	}
}
