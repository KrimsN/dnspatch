package config

import (
	"path/filepath"
	"testing"
)

// TestExamplesParse parses every examples/*.toml shipped for users through
// the real loader, so a change to the config schema that breaks a documented
// example is caught here instead of by a bug report.
func TestExamplesParse(t *testing.T) {
	t.Setenv("REGRU_PASSWORD", "example-password")
	t.Setenv("SELECTEL_PASSWORD", "example-password")
	t.Setenv("PROXY_URL", "socks5://user:pass@203.0.113.5:1080")

	examplesDir, err := filepath.Abs(filepath.Join("..", "..", "examples"))
	if err != nil {
		t.Fatal(err)
	}

	files, err := filepath.Glob(filepath.Join(examplesDir, "*.toml"))
	if err != nil {
		t.Fatal(err)
	}

	if len(files) == 0 {
		t.Fatal("no examples found")
	}

	// The secrets example references its secret file with a path relative to
	// the config file, so parsing it needs the working directory to match.
	t.Chdir(examplesDir)

	for _, file := range files {
		name := filepath.Base(file)

		t.Run(name, func(t *testing.T) {
			if _, err := Load(name); err != nil {
				t.Errorf("Load(%s): %v", name, err)
			}
		})
	}
}
