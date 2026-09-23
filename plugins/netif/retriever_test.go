package netif

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/KrimsN/dnspatch/plugin"
)

func addrs(t *testing.T, cidrs ...string) lookupFunc {
	t.Helper()

	var out []net.Addr
	for _, c := range cidrs {
		ip, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatal(err)
		}
		n.IP = ip
		out = append(out, n)
	}

	return func(string) ([]net.Addr, error) { return out, nil }
}

func TestGetAddresses(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		have    []string
		want    plugin.Addresses
		wantErr string
	}{
		{
			name: "ipv6 skips link-local and ULA",
			cfg:  Config{Name: "eth0", Family: "ipv6"},
			have: []string{"fe80::1/64", "fd00::5/64", "2001:db8::10/64"},
			want: plugin.Addresses{V6: netip.MustParseAddr("2001:db8::10")},
		},
		{
			name: "lowest of several is stable",
			cfg:  Config{Name: "eth0", Family: "ipv6"},
			have: []string{"2001:db8::9/64", "2001:db8::2/64", "2001:db8::5/64"},
			want: plugin.Addresses{V6: netip.MustParseAddr("2001:db8::2")},
		},
		{
			name: "network narrows the choice",
			cfg:  Config{Name: "eth0", Family: "ipv6", Network: "2001:db8:2::/64"},
			have: []string{"2001:db8:1::1/64", "2001:db8:2::7/64"},
			want: plugin.Addresses{V6: netip.MustParseAddr("2001:db8:2::7")},
		},
		{
			name:    "network excludes everything",
			cfg:     Config{Name: "eth0", Family: "ipv6", Network: "2001:db8:3::/64"},
			have:    []string{"2001:db8:1::1/64"},
			wantErr: "no public ipv6 address in 2001:db8:3::/64",
		},
		{
			name: "ipv4 skips private, CGNAT and loopback",
			cfg:  Config{Name: "wan", Family: "ipv4"},
			have: []string{"10.0.0.2/8", "192.168.1.2/24", "100.64.0.9/10", "127.0.0.1/8", "203.0.113.7/24"},
			want: plugin.Addresses{V4: netip.MustParseAddr("203.0.113.7")},
		},
		{
			name:    "ipv4 behind NAT has nothing to report",
			cfg:     Config{Name: "wan", Family: "ipv4"},
			have:    []string{"192.168.1.2/24"},
			wantErr: "no public ipv4 address",
		},
		{
			name: "dual reads both",
			cfg:  Config{Name: "wan", Family: "dual"},
			have: []string{"203.0.113.7/24", "2001:db8::1/64"},
			want: plugin.Addresses{V4: netip.MustParseAddr("203.0.113.7"), V6: netip.MustParseAddr("2001:db8::1")},
		},
		{
			name:    "dual fails when one family is missing",
			cfg:     Config{Name: "wan", Family: "dual"},
			have:    []string{"2001:db8::1/64"},
			wantErr: "no public ipv4 address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := newRetriever(tt.cfg, addrs(t, tt.have...))
			if err != nil {
				t.Fatal(err)
			}

			got, err := r.GetAddresses(context.Background())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLookupError(t *testing.T) {
	r, err := newRetriever(Config{Name: "nope0", Family: "ipv6"}, func(string) ([]net.Addr, error) {
		return nil, errors.New("no such network interface")
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = r.GetAddresses(context.Background())
	if err == nil || !strings.Contains(err.Error(), `interface "nope0"`) {
		t.Fatalf("error = %v, want it to name the interface", err)
	}
}

func TestNewRetrieverValidation(t *testing.T) {
	for name, cfg := range map[string]Config{
		"bad family":  {Name: "eth0", Family: "v5"},
		"empty name":  {Name: " ", Family: "ipv6"},
		"bad network": {Name: "eth0", Family: "ipv6", Network: "not-a-prefix"},
	} {
		if _, err := newRetriever(cfg, nil); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestDefaultFamilyIsIPv6(t *testing.T) {
	cfg, err := plugin.Decode[Config](map[string]any{"name": "eth0"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Family != "ipv6" {
		t.Errorf("default family = %q, want ipv6", cfg.Family)
	}
}

// The real lookup must work on the loopback interface of whatever platform
// the tests run on; it has no public address, so it must say so.
func TestOSLookupLoopback(t *testing.T) {
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skip(err)
	}

	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback == 0 {
			continue
		}

		r, err := newRetriever(Config{Name: i.Name, Family: "ipv4"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.GetAddresses(context.Background()); err == nil || !strings.Contains(err.Error(), "no public") {
			t.Errorf("loopback %s: error = %v, want a no-public-address error", i.Name, err)
		}
		return
	}
	t.Skip("no loopback interface")
}
