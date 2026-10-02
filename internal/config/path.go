package config

import (
	"fmt"
	"os"
	"strings"
)

// EnvPath names the environment variable that holds the config file path.
const EnvPath = "DNSPATCH_CONFIG"

// defaultPaths are tried in order when no path is given explicitly.
var defaultPaths = []string{"./dnspatch.toml", "/etc/dnspatch/config.toml"}

// ResolvePath picks the config file path. Priority: the flag value, the
// DNSPATCH_CONFIG variable, then the first existing default location. A path
// given explicitly must exist; the defaults are not consulted in that case.
func ResolvePath(flagValue string) (string, error) {
	return resolvePath(flagValue, os.Getenv(EnvPath), defaultPaths)
}

func resolvePath(flagValue, envValue string, defaults []string) (string, error) {
	explicit, source := flagValue, "--config"
	if explicit == "" {
		explicit, source = envValue, EnvPath
	}

	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("%s: %w", source, err)
		}

		return explicit, nil
	}

	for _, path := range defaults {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}

	return "", fmt.Errorf("no config file found: pass --config, set %s or create one of: %s",
		EnvPath, strings.Join(defaults, ", "))
}
