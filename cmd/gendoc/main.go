// Command gendoc writes the files generated from the configuration types of
// the built-in plugins: docs/PARAMETERS.md, the reference of their parameters,
// and dnspatch.toml.example, a configuration to copy and edit.
//
// It is run by go generate; see generate.go in the repository root.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dnspatch/dnspatch/plugin"
	_ "github.com/dnspatch/dnspatch/plugins/all"
)

func main() {
	docs := flag.String("docs", filepath.Join("docs", "PARAMETERS.md"), "parameter reference to write, relative to the working directory")
	example := flag.String("example", "dnspatch.toml.example", "example configuration to write, relative to the working directory")
	flag.Parse()

	if err := run(*docs, *example); err != nil {
		fmt.Fprintln(os.Stderr, "gendoc:", err)
		os.Exit(1)
	}
}

// run renders both files for the built-in plugins and writes them, creating
// directories that are missing. Nothing is written when either fails to
// render, so the two files never come from different states of the code, and
// nothing is written by a build that lacks a plugin, since the files would then
// document fewer plugins than there are.
func run(docsPath, examplePath string) error {
	if err := checkComplete(plugin.Default); err != nil {
		return err
	}

	retrievers, providers, notifiers := plugin.Default.RetrieverConfigTypes(), plugin.Default.ProviderConfigTypes(), plugin.Default.NotifierConfigTypes()

	docs, err := render(retrievers, providers, notifiers)
	if err != nil {
		return err
	}

	example, err := renderExample(retrievers, providers, notifiers)
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

// checkComplete fails when a plugin that the source tree declares is not
// registered, which is what a build without some of the build tags looks like.
// go generate passes the tags that put every plugin in.
func checkComplete(r *plugin.Registry) error {
	known := r.Known()
	if len(known) == 0 {
		return errors.New("no plugin is declared, so the catalog of plugins/all is missing; run cmd/genplugins first")
	}

	var missing []string

	for _, k := range known {
		if !r.Registered(k.Kind, k.Name) {
			missing = append(missing, fmt.Sprintf("%s %s", k.Kind, k.Name))
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("built without %s; build it with the tags that compile every plugin in, as go generate does (-tags notify_all)", strings.Join(missing, ", "))
	}

	return nil
}
