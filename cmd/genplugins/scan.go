package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/dnspatch/dnspatch/plugin"
)

// registrars maps the registration functions of package plugin to the kind of
// plugin each one registers.
var registrars = map[string]plugin.Kind{
	"RegisterProvider":  plugin.KindProvider,
	"RegisterRetriever": plugin.KindRetriever,
	"RegisterNotifier":  plugin.KindNotifier,
}

// found is one plugin discovered in the source tree.
type found struct {
	Kind plugin.Kind
	// Name is the type name the plugin registers under, which is also its
	// build tag.
	Name string
	// Import is the import path of the package that registers it.
	Import string
}

// modulePath reads the module path from the go.mod in root.
func modulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}

	return "", fmt.Errorf("%s: no module directive", filepath.Join(root, "go.mod"))
}

// discover finds every plugin under root/plugins by looking for calls to the
// registration functions of package plugin, without compiling anything: it has
// to see the plugins a build tag leaves out. Each package may register one
// plugin, and no two plugins may share a name, because the name is the build
// tag. The package all, which imports the plugins, is skipped.
func discover(root, module string) ([]found, error) {
	var (
		plugins []found
		errs    []string
	)

	err := filepath.WalkDir(filepath.Join(root, "plugins"), func(dir string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			return nil
		}

		if d.Name() == "all" || d.Name() == "testdata" {
			return filepath.SkipDir
		}

		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return err
		}

		registered, err := scanDir(dir)
		if err != nil {
			return err
		}

		switch len(registered) {
		case 0:
		case 1:
			registered[0].Import = path.Join(module, filepath.ToSlash(rel))
			plugins = append(plugins, registered[0])
		default:
			errs = append(errs, fmt.Sprintf("%s registers %d plugins; a package may register one, since its name is its build tag", filepath.ToSlash(rel), len(registered)))
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	seen := make(map[string]found)

	for _, p := range plugins {
		if other, ok := seen[p.Name]; ok {
			errs = append(errs, fmt.Sprintf("%s %q of %s and %s %q of %s share a name, so they would share a build tag", other.Kind, other.Name, other.Import, p.Kind, p.Name, p.Import))
		}

		seen[p.Name] = p
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "\n"))
	}

	slices.SortFunc(plugins, func(a, b found) int {
		if c := strings.Compare(string(a.Kind), string(b.Kind)); c != 0 {
			return c
		}

		return strings.Compare(a.Name, b.Name)
	})

	return plugins, nil
}

// scanDir lists the registrations made by the non-test Go files of one
// directory. Build constraints are ignored on purpose.
func scanDir(dir string) ([]found, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()

	var files []*ast.File

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			return nil, err
		}

		files = append(files, file)
	}

	consts := stringConsts(files)

	var registered []found

	for _, file := range files {
		var walkErr error

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			kind, ok := registrar(call)
			if !ok {
				return true
			}

			name, err := pluginName(call, consts)
			if err != nil {
				walkErr = fmt.Errorf("%s: %w", fset.Position(call.Pos()), err)
				return false
			}

			registered = append(registered, found{Kind: kind, Name: name})

			return true
		})

		if walkErr != nil {
			return nil, walkErr
		}
	}

	return registered, nil
}

// registrar reports whether call registers a plugin with package plugin, as in
// plugin.RegisterProvider(...) or plugin.RegisterProvider[Config](...), and of
// which kind.
func registrar(call *ast.CallExpr) (plugin.Kind, bool) {
	fun := call.Fun

	switch f := fun.(type) {
	case *ast.IndexExpr:
		fun = f.X
	case *ast.IndexListExpr:
		fun = f.X
	}

	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}

	if x, isIdent := sel.X.(*ast.Ident); !isIdent || x.Name != "plugin" {
		return "", false
	}

	kind, ok := registrars[sel.Sel.Name]

	return kind, ok
}

// pluginName reads the name argument of a registration: a string literal, or a
// constant of the package holding one.
func pluginName(call *ast.CallExpr, consts map[string]string) (string, error) {
	if len(call.Args) == 0 {
		return "", fmt.Errorf("registration without a name")
	}

	switch arg := call.Args[0].(type) {
	case *ast.BasicLit:
		if arg.Kind == token.STRING {
			return strconv.Unquote(arg.Value)
		}
	case *ast.Ident:
		if name, ok := consts[arg.Name]; ok {
			return name, nil
		}
	}

	return "", fmt.Errorf("the name of the plugin must be a string literal or a string constant of its package, so that it can be read without compiling")
}

// stringConsts collects the package-level constants defined as a string
// literal in the files of a directory.
func stringConsts(files []*ast.File) map[string]string {
	consts := make(map[string]string)

	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}

			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)

				for i, ident := range vs.Names {
					if i >= len(vs.Values) {
						break
					}

					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}

					if value, err := strconv.Unquote(lit.Value); err == nil {
						consts[ident.Name] = value
					}
				}
			}
		}
	}

	return consts
}
