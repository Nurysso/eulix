/*
[!] AI SLOP VIBE CODE

Exists Just to get how many keys from kb.json are we actually using in eulix_cli
exists cause on large code bases kb.json is too huge naturally this leads to too much memory
being used at retrieval stage.

It's designed to be called from the Python script and uses only the
Go standard library - no external dependencies.

Usage:

	go run astdump.go file1.go file2.go ...
	# or
	astdump file1.go file2.go ...

Output format (JSON array):

	[
		{
		"path": "path/to/file.go",
		"funcs": [
			{
			"name": "FunctionName",
			"recv_name": "r",
			"recv_type": "*SomeType",
			"params": [{"name": "p", "type": "string"}],
			"returns": ["error"],
			"var_decls": [{"name": "v", "type": "int"}],
			"ranges": [{"x": "slice", "value": "item", "line": 42}],
			"assigns": [{"lhs": ["x"], "rhs": ["y"], "line": 43}],
			"selectors": [{"base": "obj", "field": "Field", "line": 44}]
			}
		]
		}
	]
*/
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

type FunctionInfo struct {
	Name      string         `json:"name"`
	RecvName  string         `json:"recv_name,omitempty"`
	RecvType  string         `json:"recv_type,omitempty"`
	Params    []ParamInfo    `json:"params,omitempty"`
	Returns   []string       `json:"returns,omitempty"`
	VarDecls  []VarDeclInfo  `json:"var_decls,omitempty"`
	Ranges    []RangeInfo    `json:"ranges,omitempty"`
	Assigns   []AssignInfo   `json:"assigns,omitempty"`
	Selectors []SelectorInfo `json:"selectors,omitempty"`
}

type ParamInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type VarDeclInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type RangeInfo struct {
	X     string `json:"x"`
	Value string `json:"value"`
	Key   string `json:"key"`
	Line  int    `json:"line"`
}

type AssignInfo struct {
	LHS  []string `json:"lhs"`
	RHS  []string `json:"rhs"`
	Line int      `json:"line"`
}

type SelectorInfo struct {
	Base  string `json:"base"`
	Field string `json:"field"`
	Line  int    `json:"line"`
}

type FileReport struct {
	Path  string         `json:"path"`
	Funcs []FunctionInfo `json:"funcs"`
}

func typeToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeToString(t.X)
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + typeToString(t.Elt)
		}
		return "[...]" + typeToString(t.Elt)
	case *ast.MapType:
		return "map[" + typeToString(t.Key) + "]" + typeToString(t.Value)
	case *ast.SelectorExpr:
		return typeToString(t.X) + "." + t.Sel.Name
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.ChanType:
		return "chan " + typeToString(t.Value)
	case *ast.FuncType:
		return "func(...)"
	case *ast.IndexExpr:
		return typeToString(t.X) + "[" + typeToString(t.Index) + "]"
	case *ast.IndexListExpr:
		var indices []string
		for _, idx := range t.Indices {
			indices = append(indices, typeToString(idx))
		}
		return typeToString(t.X) + "[" + strings.Join(indices, ", ") + "]"
	default:
		return fmt.Sprintf("?%T", expr)
	}
}

func fieldListToParams(fl *ast.FieldList) []ParamInfo {
	if fl == nil {
		return nil
	}
	var params []ParamInfo
	for _, field := range fl.List {
		typeStr := typeToString(field.Type)
		if len(field.Names) == 0 {
			params = append(params, ParamInfo{Name: "", Type: typeStr})
		} else {
			for _, name := range field.Names {
				params = append(params, ParamInfo{
					Name: name.Name,
					Type: typeStr,
				})
			}
		}
	}
	return params
}

func fieldListToReturns(fl *ast.FieldList) []string {
	if fl == nil {
		return nil
	}
	var returns []string
	for _, field := range fl.List {
		typeStr := typeToString(field.Type)
		if len(field.Names) == 0 {
			returns = append(returns, typeStr)
		} else {
			for _, name := range field.Names {
				returns = append(returns, name.Name+" "+typeStr)
			}
		}
	}
	return returns
}

func extractSelectors(node ast.Node, fset *token.FileSet) []SelectorInfo {
	var selectors []SelectorInfo
	ast.Inspect(node, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if _, ok := sel.X.(*ast.Ident); ok {
				base := exprToString(sel.X)
				if base != "" {
					selectors = append(selectors, SelectorInfo{
						Base:  base,
						Field: sel.Sel.Name,
						Line:  fset.Position(sel.Pos()).Line,
					})
				}
			}
		}
		return true
	})
	return selectors
}

func exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprToString(e.X) + "." + e.Sel.Name
	case *ast.IndexExpr:
		return exprToString(e.X) + "[...]"
	case *ast.StarExpr:
		return "*" + exprToString(e.X)
	case *ast.ParenExpr:
		return "(" + exprToString(e.X) + ")"
	case *ast.CallExpr:
		return exprToString(e.Fun) + "(...)"
	default:
		return ""
	}
}

func extractAssigns(node ast.Node, fset *token.FileSet) []AssignInfo {
	var assigns []AssignInfo
	ast.Inspect(node, func(n ast.Node) bool {
		switch assign := n.(type) {
		case *ast.AssignStmt:
			var lhs []string
			var rhs []string
			for _, expr := range assign.Lhs {
				lhs = append(lhs, exprToString(expr))
			}
			for _, expr := range assign.Rhs {
				rhs = append(rhs, exprToString(expr))
			}
			if len(lhs) > 0 && len(rhs) > 0 {
				assigns = append(assigns, AssignInfo{
					LHS:  lhs,
					RHS:  rhs,
					Line: fset.Position(assign.Pos()).Line,
				})
			}
		}
		return true
	})
	return assigns
}

func extractRanges(node ast.Node, fset *token.FileSet) []RangeInfo {
	var ranges []RangeInfo
	ast.Inspect(node, func(n ast.Node) bool {
		if rangeStmt, ok := n.(*ast.RangeStmt); ok {
			var key, value string
			if rangeStmt.Key != nil {
				if ident, ok := rangeStmt.Key.(*ast.Ident); ok {
					key = ident.Name
				}
			}
			if rangeStmt.Value != nil {
				if ident, ok := rangeStmt.Value.(*ast.Ident); ok {
					value = ident.Name
				}
			}
			ranges = append(ranges, RangeInfo{
				X:     exprToString(rangeStmt.X),
				Key:   key,
				Value: value,
				Line:  fset.Position(rangeStmt.Pos()).Line,
			})
		}
		return true
	})
	return ranges
}

func extractVarDecls(node ast.Node, fset *token.FileSet) []VarDeclInfo {
	var varDecls []VarDeclInfo
	ast.Inspect(node, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.GenDecl:
			if decl.Tok == token.VAR {
				for _, spec := range decl.Specs {
					if valSpec, ok := spec.(*ast.ValueSpec); ok {
						typeStr := typeToString(valSpec.Type)
						for _, name := range valSpec.Names {
							varDecls = append(varDecls, VarDeclInfo{
								Name: name.Name,
								Type: typeStr,
							})
						}
					}
				}
			}
		case *ast.AssignStmt:
		}
		return true
	})
	return varDecls
}

func processFunction(fn *ast.FuncDecl, fset *token.FileSet) FunctionInfo {
	info := FunctionInfo{
		Name: fn.Name.Name,
	}

	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		recv := fn.Recv.List[0]
		if len(recv.Names) > 0 {
			info.RecvName = recv.Names[0].Name
		}
		info.RecvType = typeToString(recv.Type)
	}

	info.Params = fieldListToParams(fn.Type.Params)

	info.Returns = fieldListToReturns(fn.Type.Results)

	if fn.Body != nil {
		info.VarDecls = extractVarDecls(fn.Body, fset)
		info.Ranges = extractRanges(fn.Body, fset)
		info.Assigns = extractAssigns(fn.Body, fset)
		info.Selectors = extractSelectors(fn.Body, fset)
	}

	return info
}

func processFile(path string) (*FileReport, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	report := &FileReport{
		Path:  path,
		Funcs: []FunctionInfo{},
	}

	ast.Inspect(node, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok {
			report.Funcs = append(report.Funcs, processFunction(fn, fset))
		}
		return true
	})

	return report, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <go-file> [go-file...]\n", os.Args[0])
		os.Exit(1)
	}

	var reports []FileReport

	for _, arg := range os.Args[1:] {
		var files []string
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s - %v\n", arg, err)
			continue
		}
		if info.IsDir() {
			err = filepath.Walk(arg, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if !info.IsDir() && strings.HasSuffix(path, ".go") {
					files = append(files, path)
				}
				return nil
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: walking %s - %v\n", arg, err)
				continue
			}
		} else {
			files = []string{arg}
		}

		for _, file := range files {
			report, err := processFile(file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: parsing %s - %v\n", file, err)
				continue
			}
			reports = append(reports, *report)
		}
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(reports); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}
}
