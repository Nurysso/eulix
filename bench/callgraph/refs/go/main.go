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
	Why      string   `json:"why,omitempty"`
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
	// `./...` skips testdata/ and directories starting with `_` or `.`; eulix
	// parses those files too, so load each such directory as its own pattern.
	patterns := []string{"./..."}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == root {
			return nil
		}
		name := d.Name()
		if name == ".git" || name == "node_modules" || name == "vendor" {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(root, p)
		hidden := false
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			if part == "testdata" || strings.HasPrefix(part, "_") || strings.HasPrefix(part, ".") {
				hidden = true
			}
		}
		if hidden {
			if gos, _ := filepath.Glob(filepath.Join(p, "*.go")); len(gos) > 0 {
				patterns = append(patterns, "./"+filepath.ToSlash(rel))
			}
		}
		return nil
	})
	pkgs, err := packages.Load(cfg, patterns...)
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
	type implKey struct {
		m     *types.Func
		iface *types.Interface
	}
	implCache := map[implKey][]*types.Func{}
	implementors := func(m *types.Func, iface *types.Interface) []*types.Func {
		if r, ok := implCache[implKey{m, iface}]; ok {
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
		implCache[implKey{m, iface}] = out
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
				var selExpr *ast.SelectorExpr
				switch x := fun.(type) {
				case *ast.Ident:
					id = x
				case *ast.SelectorExpr:
					id = x.Sel
					selExpr = x
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
						} else if pi := projectInterface(info, selExpr, fset, rel); pi != nil {
							// A method of an outside interface (http.Flusher.Flush) called
							// through a project interface that embeds it: only types that
							// implement the project interface can receive the call.
							fns = implementors(o, pi)
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
					} else if recv := sig.Recv(); recv != nil && types.IsInterface(recv.Type()) &&
						len(implementors(o, recv.Type().Underlying().(*types.Interface))) > 0 {
						// An outside interface (error, io.Writer) that project types
						// implement: the call may run project code or not.
						s.Status = "unknown"
						s.Why = "outside interface with project implementations"
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
}

// The receiver's static type, when it is an interface declared in the project.
func projectInterface(info *types.Info, sel *ast.SelectorExpr, fset *token.FileSet, rel func(string) (string, bool)) *types.Interface {
	if sel == nil {
		return nil
	}
	selection := info.Selections[sel]
	if selection == nil {
		return nil
	}
	t := selection.Recv()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || !types.IsInterface(named) {
		return nil
	}
	if _, inProj := rel(fset.Position(named.Obj().Pos()).Filename); !inProj {
		return nil
	}
	return named.Underlying().(*types.Interface)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "go-ref:", err)
		os.Exit(1)
	}
}
