//go:build ignore

// This development-only inventory extracts authored Go messages and descriptor
// labels. It does not rewrite or translate records or query results.
package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type item struct {
	Source string `json:"source"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Kind   string `json:"kind"`
}

func main() {
	set := token.NewFileSet()
	var items []item
	var concatenation func(ast.Expr) string
	concatenation = func(node ast.Expr) string {
		if binary, ok := node.(*ast.BinaryExpr); ok && binary.Op == token.ADD {
			return concatenation(binary.X) + concatenation(binary.Y)
		}
		if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
			value, _ := strconv.Unquote(literal.Value)
			return value
		}
		return "%s"
	}
	var add func(ast.Expr, string, string)
	add = func(node ast.Expr, file, kind string) {
		if binary, ok := node.(*ast.BinaryExpr); ok && binary.Op == token.ADD {
			items = append(items, item{Source: concatenation(binary), File: filepath.ToSlash(file), Line: set.Position(binary.Pos()).Line, Kind: kind})
			return
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if callee, ok := call.Fun.(*ast.SelectorExpr); ok && callee.Sel.Name == "Sprintf" && len(call.Args) > 0 {
				add(call.Args[0], file, kind)
			}
			return
		}
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil || strings.TrimSpace(value) == "" {
			return
		}
		items = append(items, item{Source: value, File: filepath.ToSlash(file), Line: set.Position(literal.Pos()).Line, Kind: kind})
	}
	err := filepath.WalkDir("internal", func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") || strings.Contains(file, "i18n") {
			return nil
		}
		parsed, err := parser.ParseFile(set, file, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.CompositeLit:
				name := ""
				switch typed := value.Type.(type) {
				case *ast.Ident:
					name = typed.Name
				case *ast.SelectorExpr:
					name = typed.Sel.Name
				}
				if name == "Invalid" && len(value.Elts) > 0 {
					if _, keyed := value.Elts[0].(*ast.KeyValueExpr); !keyed {
						add(value.Elts[0], file, "validation error")
					}
				}
			case *ast.KeyValueExpr:
				key := ""
				switch field := value.Key.(type) {
				case *ast.Ident:
					key = field.Name
				case *ast.BasicLit:
					key, _ = strconv.Unquote(field.Value)
				}
				switch key {
				case "Title", "Short", "Label", "Message", "Error", "Success", "Reason", "Explanation", "message", "warning", "reason", "error", "explanation":
					add(value.Value, file, "authored "+key)
				case "note":
					if strings.HasSuffix(file, "chores_preview.go") || strings.HasSuffix(file, "chores_series.go") {
						add(value.Value, file, "preview note")
					}
				}
			case *ast.AssignStmt:
				for index, left := range value.Lhs {
					field, ok := left.(*ast.SelectorExpr)
					if ok && field.Sel.Name == "Reason" && index < len(value.Rhs) {
						add(value.Rhs[index], file, "authored reason")
					}
				}
			case *ast.CallExpr:
				name, owner := "", ""
				switch callee := value.Fun.(type) {
				case *ast.Ident:
					name = callee.Name
				case *ast.SelectorExpr:
					name = callee.Sel.Name
					if identifier, ok := callee.X.(*ast.Ident); ok {
						owner = identifier.Name
					}
				}
				if name == "Rejection" || name == "Errorf" || name == "New" && owner == "errors" {
					if len(value.Args) > 0 {
						kind := "error pattern"
						if name == "Rejection" {
							kind = "authentication rejection"
						}
						add(value.Args[0], file, kind)
					}
				}
				if name == "Error" && owner == "http" && len(value.Args) > 1 {
					add(value.Args[1], file, "HTTP error")
				}
				if name == "page" && len(value.Args) > 1 {
					add(value.Args[1], file, "page title")
				}
				if owner == "i18n" && (name == "Text" || name == "Message") && len(value.Args) > 1 {
					add(value.Args[1], file, "translated source")
				}
				if strings.Contains(filepath.ToSlash(file), "/modules/") && len(value.Args) > 1 {
					switch name {
					case "Text", "Long", "Ref", "Qty", "Date", "Time", "Bool", "Money", "Int", "Select":
						add(value.Args[1], file, "field label")
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		panic(err)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Source < items[j].Source })
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(items); err != nil {
		panic(err)
	}
}
