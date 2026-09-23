package rfc2136

import (
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/KrimsN/dnspatch/plugin"
)

// TestRealServer runs an update against a real name server. It is skipped
// unless RFC2136_TEST_SERVER (host:port) is set; the server must be primary
// for the zone example.com and accept updates to home.example.com signed with
// the key dnspatch-key (hmac-sha256) whose secret is RFC2136_TEST_SECRET.
func TestRealServer(t *testing.T) {
	server := os.Getenv("RFC2136_TEST_SERVER")
	secret := os.Getenv("RFC2136_TEST_SECRET")
	if server == "" || secret == "" {
		t.Skip("RFC2136_TEST_SERVER and RFC2136_TEST_SECRET are not set")
	}

	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			cfg := testConfig(server)
			cfg.KeySecret = secret
			cfg.Protocol = network

			addrs := plugin.Addresses{V4: netip.MustParseAddr("198.51.100.42"), V6: netip.MustParseAddr("2001:db8::42")}
			if network == "udp" {
				addrs = plugin.Addresses{V4: netip.MustParseAddr("198.51.100.43"), V6: netip.MustParseAddr("2001:db8::43")}
			}
			if err := mustProvider(t, cfg).Update(t.Context(), addrs, plugin.RecordOptions{}); err != nil {
				t.Fatalf("Update: %v", err)
			}

			for typ, want := range map[uint16]string{dns.TypeA: addrs.V4.String(), dns.TypeAAAA: addrs.V6.String()} {
				q := new(dns.Msg).SetQuestion("home.example.com.", typ)
				reply, _, err := (&dns.Client{Net: network, Timeout: 5 * time.Second}).ExchangeContext(t.Context(), q, server)
				if err != nil {
					t.Fatalf("query %s: %v", dns.TypeToString[typ], err)
				}
				if len(reply.Answer) != 1 {
					t.Fatalf("%s answer = %v, want exactly one record: the old one must be replaced", dns.TypeToString[typ], reply.Answer)
				}
				var got string
				switch rr := reply.Answer[0].(type) {
				case *dns.A:
					got = rr.A.String()
				case *dns.AAAA:
					got = rr.AAAA.String()
				}
				if got != want {
					t.Errorf("%s = %s, want %s", dns.TypeToString[typ], got, want)
				}
			}
		})
	}
}
