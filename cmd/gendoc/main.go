// Command gendoc writes the files generated from the configuration types of
// the built-in plugins: docs/PARAMETERS.md, the reference of their parameters,
// and config.toml.example, a configuration to copy and edit.
//
// It is run by go generate; see generate.go in the repository root.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/KrimsN/dnspatch/plugin"
	_ "github.com/KrimsN/dnspatch/plugins/all"
)

func main() {
	docs := flag.String("docs", filepath.Join("docs", "PARAMETERS.md"), "parameter reference to write, relative to the working directory")
	example := flag.String("example", "config.toml.example", "example configuration to write, relative to the working directory")
	flag.Parse()

	if err := run(*docs, *example); err != nil {
		fmt.Fprintln(os.Stderr, "gendoc:", err)
		os.Exit(1)
	}
}

// run renders both files for the built-in plugins and writes them, creating
// directories that are missing. Nothing is written when either fails to
// render, so the two files never come from different states of the code.
func run(docsPath, examplePath string) error {
	retrievers, providers := plugin.Default.RetrieverConfigTypes(), plugin.Default.ProviderConfigTypes()

	docs, err := render(retrievers, providers)
	if err != nil {
		return err
	}

	example, err := renderExample(retrievers, providers)
	if err != nil {
		return err
	}

	if err := writeFile(docsPath, docs); err != nil {
		return err
	}

	return writeFile(examplePath, example)
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}
