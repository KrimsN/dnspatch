// Command genplugins keeps the list of built-in plugins in one place: the
// source tree. It finds every plugin under plugins/ by its registration call
// and writes, into plugins/all, a file per plugin that imports it under its
// build tag, a catalog that declares all of them to the registry, and the table
// of build tags in docs/deployment/building.md.
//
// A new plugin is a new package that registers itself; running go generate is
// all it takes to make it selectable by a tag. See generate.go in the
// repository root.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	if err := run(*root); err != nil {
		fmt.Fprintln(os.Stderr, "genplugins:", err)
		os.Exit(1)
	}
}

// run renders everything first and writes only when all of it rendered, so the
// files never come from different states of the tree.
func run(root string) error {
	module, err := modulePath(root)
	if err != nil {
		return err
	}

	plugins, err := discover(root, module)
	if err != nil {
		return err
	}

	if len(plugins) == 0 {
		return fmt.Errorf("no plugins found under %s", filepath.Join(root, "plugins"))
	}

	docPath := filepath.Join(root, "docs", "deployment", "building.md")

	doc, err := os.ReadFile(docPath)
	if err != nil {
		return err
	}

	newDoc, err := replaceTable(string(doc), renderTable(plugins))
	if err != nil {
		return err
	}

	dir := filepath.Join(root, "plugins", "all")
	files := map[string][]byte{catalogFile: renderCatalog(plugins)}

	for _, p := range plugins {
		files[fileName(p)] = renderImport(p)
	}

	if err := removeStale(dir, files); err != nil {
		return err
	}

	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}

	return os.WriteFile(docPath, []byte(newDoc), 0o644)
}

// removeStale deletes the files of an earlier run that no longer have a plugin.
func removeStale(dir string, keep map[string][]byte) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		if _, ok := keep[e.Name()]; ok || e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}

		full := filepath.Join(dir, e.Name())

		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}

		if strings.HasPrefix(string(data), header) {
			if err := os.Remove(full); err != nil {
				return err
			}
		}
	}

	return nil
}
