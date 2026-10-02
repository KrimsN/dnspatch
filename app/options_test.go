package app

import (
	"testing"

	"github.com/dnspatch/dnspatch/internal/config"
	"github.com/dnspatch/dnspatch/plugin"
)

func TestWithVersionSetsTheVersionOption(t *testing.T) {
	var s settings

	WithVersion("1.2.3")(&s)

	if got := options(s).Version; got != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", got)
	}
}

func TestOptionsUseTheDefaultRegistry(t *testing.T) {
	opts := options(settings{})

	if opts.Registry != plugin.Default {
		t.Error("Registry is not plugin.Default, so plugins registered by import would not be found")
	}

	if opts.Notify == nil {
		t.Error("Notify is nil, want it always set so that an unavailable backend is named in the error")
	}
}

func TestNotifyReportsAnUnknownBackend(t *testing.T) {
	conn, err := options(settings{}).Notify(config.Plugin{Type: "no-such-backend"}, nil)
	if err == nil || conn != nil {
		t.Errorf("Notify(unknown type) = %v, %v, want no connection and an error", conn, err)
	}
}
