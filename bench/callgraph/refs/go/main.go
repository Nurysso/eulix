// Reference call sites for a Go module, from go/types.
//
// Usage: go-ref <module root>   (writes JSON lines to stdout)
//
// Every call expression in the module (tests included) is resolved with the
// type checker. Static calls point at the *types.Func they name. A call on a
// project interface lists every project method that implements it (class
// hierarchy analysis); a call on an interface from outside the module is
// "external". Conversions are skipped; builtins are "external"; calls on
// function values are "unknown".
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

type target struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Name string `json:"name"`
}

type site struct {
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Col      int      `json:"col"`
	Lines    []int    `json:"lines"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Status   string   `json:"status"`
	Dispatch string   `json:"dispatch"`
	Targets  []target `json:"targets"`
}

var root string

func rel(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	r, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(r, "..") {
		return "", false
	}
	return filepath.ToSlash(r), true
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go-ref <module root>")
		os.Exit(2)
	}
	var err error
	root, err = filepath.Abs(os.Args[1])
	must(err)
	root, err = filepath.EvalSymlinks(root)
	must(err)

	cfg := &packages.Config{
		Mode:  packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Dir:   root,
		Tests: true,
		Env:   append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off"),
	}
	pkgs, err := packages.Load(cfg, "./...")
	must(err)
	nerr := 0
	packages.Visit(pkgs, nil, func(p *packages.Package) { nerr += len(p.Errors) })
	if nerr > 0 {
		fmt.Fprintf(os.Stderr, "go-ref: %d package errors (calls in broken code are 'unknown')\n", nerr)
	}

	// Concrete project types, for interface dispatch.
	var named []*types.Named
	seenT := map[*types.TypeName]bool{}
	for _, p := range pkgs {
		if p.Types == nil {
			continue
		}
		sc := p.Types.Scope()
		for _, n := range sc.Names() {
			tn, ok := sc.Lookup(n).(*types.TypeName)
			if !ok || seenT[tn] {
				continue
			}
			if _, inProj := rel(p.Fset.Position(tn.Pos()).Filename); !inProj {
				continue
			}
			seenT[tn] = true
			if nt, ok := tn.Type().(*types.Named); ok && !types.IsInterface(nt) {
				named = append(named, nt)
			}
		}
	}
	implCache := map[*types.Func][]*types.Func{}
	implementors := func(m *types.Func, iface *types.Interface) []*types.Func {
		if r, ok := implCache[m]; ok {
			return r
		}
		var out []*types.Func
		for _, nt := range named {
			var t types.Type = nt
			if !types.Implements(t, iface) {
				t = types.NewPointer(nt)
				if !types.Implements(t, iface) {
					continue
				}
			}
			obj, _, _ := types.LookupFieldOrMethod(t, true, m.Pkg(), m.Name())
			if f, ok := obj.(*types.Func); ok {
				out = append(out, f)
			}
		}
		implCache[m] = out
		return out
	}

	enc := json.NewEncoder(os.Stdout)
	seen := map[string]bool{}
	for _, p := range pkgs {
		if p.TypesInfo == nil {
			continue
		}
		info, fset := p.TypesInfo, p.Fset
		for _, f := range p.Syntax {
			fname, ok := rel(fset.Position(f.Pos()).Filename)
			if !ok {
				continue
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				fun := ast.Unparen(call.Fun)
				switch x := fun.(type) {
				case *ast.IndexExpr:
					fun = x.X
				case *ast.IndexListExpr:
					fun = x.X
				}
				var id *ast.Ident
				switch x := fun.(type) {
				case *ast.Ident:
					id = x
				case *ast.SelectorExpr:
					id = x.Sel
				default:
					return true // func literal, call result, etc.
				}
				pos := fset.Position(id.Pos())
				key := fmt.Sprintf("%s:%d:%d", fname, pos.Line, pos.Column)
				if seen[key] {
					return true // same file in the package and its test variant
				}
				s := site{File: fname, Line: pos.Line, Col: pos.Column, Name: id.Name, Kind: "function", Dispatch: "static",
					Lines: []int{fset.Position(call.Pos()).Line}, Targets: []target{}}
				obj := info.Uses[id]
				switch o := obj.(type) {
				case *types.TypeName:
					// T(x) is a conversion. A project type is skipped; an outside one
					// (http.Dir(root)) is an external site, so linking it is an FP.
					if _, inProj := rel(fset.Position(o.Pos()).Filename); inProj || o.Pkg() == nil {
						return true
					}
					s.Status = "external"
				case *types.Builtin:
					s.Status = "external"
				case *types.Func:
					o = o.Origin()
					sig := o.Type().(*types.Signature)
					if sig.Recv() != nil {
						s.Kind = "method"
					}
					var fns []*types.Func
					if recv := sig.Recv(); recv != nil && types.IsInterface(recv.Type()) {
						s.Dispatch = "virtual"
						if _, inProj := rel(fset.Position(o.Pos()).Filename); inProj {
							// the interface method itself is a fair static target too
							fns = append([]*types.Func{o}, implementors(o, recv.Type().Underlying().(*types.Interface))...)
						}
					} else {
						fns = []*types.Func{o}
					}
					for _, fn := range fns {
						fp := fset.Position(fn.Pos())
						if r, ok := rel(fp.Filename); ok {
							s.Targets = append(s.Targets, target{r, fp.Line, fn.Name()})
						}
					}
					if len(s.Targets) > 0 {
						s.Status = "project"
					} else if _, inProj := rel(fset.Position(o.Pos()).Filename); inProj {
						s.Status = "unknown" // project interface with no implementor
					} else {
						s.Status = "external"
					}
				default:
					s.Status = "unknown" // function value, or broken code
				}
				seen[key] = true
				must(enc.Encode(s))
				return true
			})
		}
	}
	_ = token.NoPos
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "go-ref:", err)
		os.Exit(1)
	}
}
