package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree creates files under a fresh directory and returns it.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))

		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

const sampleReadme = "# Sample\n\n" + readmeStart + "\nold\n" + readmeEnd + "\n\nafter\n"

// sampleTree is a module with one plugin of each kind, written the different
// ways a registration can look, and a package that registers nothing.
func sampleTree() map[string]string {
	return map[string]string{
		"go.mod":    "module example.test/dp\n\ngo 1.25\n",
		"README.md": sampleReadme,
		"plugins/alpha/alpha.go": `package alpha

import "example.test/dp/plugin"

const Name = "alpha"

func init() { plugin.RegisterProvider(Name, nil) }
`,
		"plugins/group/beta/beta.go": `package beta

import "example.test/dp/plugin"

func init() {
	plugin.RegisterRetriever[Config]("beta", nil)
}
`,
		"plugins/gamma/gamma.go": `package gamma

import "example.test/dp/plugin"

func init() { plugin.RegisterNotifier("gamma", nil) }
`,
		// Neither a function of the same name in another package nor a test file registers a plugin.
		"plugins/helper/helper.go":      "package helper\n\nfunc RegisterProvider(string) {}\n",
		"plugins/alpha/alpha_test.go":   "package alpha\n\nimport \"example.test/dp/plugin\"\n\nfunc init() { plugin.RegisterProvider(\"in-a-test\", nil) }\n",
		"plugins/all/provider_stale.go": header + "\n\npackage all\n",
		"plugins/all/doc.go":            "package all\n",
	}
}

func TestRunWritesTheGatingFilesTheCatalogAndTheTable(t *testing.T) {
	root := writeTree(t, sampleTree())

	if runErr := run(root); runErr != nil {
		t.Fatal(runErr)
	}

	read := func(name string) string {
		t.Helper()

		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}

		return string(data)
	}

	for file, wants := range map[string][]string{
		"plugins/all/provider_alpha.go": {"//go:build !dnspatch_none || providers_all || alpha\n", `import _ "example.test/dp/plugins/alpha"`},
		"plugins/all/retriever_beta.go": {"//go:build !dnspatch_none || retrievers_all || beta\n", `import _ "example.test/dp/plugins/group/beta"`},
		"plugins/all/notifier_gamma.go": {"//go:build notify_all || gamma\n", `import _ "example.test/dp/plugins/gamma"`},
		"plugins/all/catalog_gen.go":    {`plugin.Default.Declare(plugin.KindProvider, "alpha"`, `plugin.Default.Declare(plugin.KindRetriever, "beta"`, `plugin.Default.Declare(plugin.KindNotifier, "gamma"`},
		"README.md":                     {"| `alpha` | provider | `alpha` | yes |", "| `gamma` | notifier | `gamma` | no (`notify_all` brings it too) |", "after\n"},
	} {
		got := read(file)

		for _, want := range wants {
			if !strings.Contains(got, want) {
				t.Errorf("%s lacks %q:\n%s", file, want, got)
			}
		}
	}

	if strings.Contains(read("README.md"), "old") {
		t.Error("the old table is still in README.md")
	}

	if strings.Contains(read("plugins/all/catalog_gen.go"), "in-a-test") || strings.Contains(read("plugins/all/catalog_gen.go"), "helper") {
		t.Error("the catalog lists something that is not a plugin")
	}

	if _, err := os.Stat(filepath.Join(root, "plugins", "all", "provider_stale.go")); err == nil {
		t.Error("the file of a plugin that is gone was not removed")
	}

	if _, err := os.Stat(filepath.Join(root, "plugins", "all", "doc.go")); err != nil {
		t.Error("a file that is not generated was removed")
	}
}

func TestRunIsRepeatable(t *testing.T) {
	root := writeTree(t, sampleTree())

	if err := run(root); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(filepath.Join(root, "plugins", "all", "catalog_gen.go"))
	if err != nil {
		t.Fatal(err)
	}

	if err = run(root); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(filepath.Join(root, "plugins", "all", "catalog_gen.go"))
	if err != nil {
		t.Fatal(err)
	}

	if string(before) != string(after) {
		t.Errorf("a second run changed the catalog:\n%s\n---\n%s", before, after)
	}
}

func TestRunRejectsWhatCannotBeGenerated(t *testing.T) {
	for name, tc := range map[string]struct {
		extra map[string]string
		want  string
	}{
		"two plugins with one name": {
			map[string]string{"plugins/other/other.go": "package other\n\nimport \"example.test/dp/plugin\"\n\nfunc init() { plugin.RegisterRetriever(\"alpha\", nil) }\n"},
			"share a name",
		},
		"two plugins in one package": {
			map[string]string{"plugins/alpha/more.go": "package alpha\n\nimport \"example.test/dp/plugin\"\n\nfunc init() { plugin.RegisterProvider(\"more\", nil) }\n"},
			"registers 2 plugins",
		},
		"a name that is not a constant": {
			map[string]string{"plugins/dyn/dyn.go": "package dyn\n\nimport \"example.test/dp/plugin\"\n\nfunc init() { plugin.RegisterProvider(name(), nil) }\n"},
			"string literal or a string constant",
		},
	} {
		t.Run(name, func(t *testing.T) {
			tree := sampleTree()
			for k, v := range tc.extra {
				tree[k] = v
			}

			err := run(writeTree(t, tree))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("run error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestRunNeedsTheMarkersInTheReadme(t *testing.T) {
	tree := sampleTree()
	tree["README.md"] = "# Sample\n"

	err := run(writeTree(t, tree))
	if err == nil || !strings.Contains(err.Error(), "block to put the table of build tags in") {
		t.Errorf("run error = %v, want it to explain the missing markers", err)
	}
}

// The check that CONTRIBUTING.md asks a contributor to keep green: a plugin
// added or removed without go generate fails the ordinary test run.
func TestCommittedFilesAreCurrent(t *testing.T) {
	root := filepath.Join("..", "..")

	module, err := modulePath(root)
	if err != nil {
		t.Fatal(err)
	}

	plugins, err := discover(root, module)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string][]byte{catalogFile: renderCatalog(plugins)}
	for _, p := range plugins {
		want[fileName(p)] = renderImport(p)
	}

	dir := filepath.Join(root, "plugins", "all")

	for name, data := range want {
		got, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil || string(got) != string(data) {
			t.Errorf("plugins/all/%s is missing or out of date; run go generate ./... and commit the result", name)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if _, ok := want[e.Name()]; ok || e.Name() == "doc.go" {
			continue
		}

		t.Errorf("plugins/all/%s belongs to no plugin; run go generate ./... and commit the result", e.Name())
	}

	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}

	updated, err := replaceTable(string(readme), renderTable(plugins))
	if err != nil {
		t.Fatal(err)
	}

	if updated != string(readme) {
		t.Error("the table of build tags in README.md is out of date; run go generate ./... and commit the result")
	}
}
