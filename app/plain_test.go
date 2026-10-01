//go:build !ping

package app

import "testing"

// A build without the ping tag must not carry the monitoring hooks: that is
// what makes a config with ping_url fail loudly instead of being ignored.
func TestPlainBuildHasNoPingHook(t *testing.T) {
	if options(settings{}).Hooks != nil {
		t.Error("Hooks is set in a build without the ping tag")
	}
}
