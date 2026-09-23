package rfc2136

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/KrimsN/dnspatch/plugin"
)

const (
	testKey    = "dnspatch-key."
	testSecret = "c2VjcmV0LXNlY3JldC1zZWNyZXQ="
)

// fakeServer is a name server that verifies TSIG and records every message
// it is asked to handle.
type fakeServer struct {
	addr string

	mu   sync.Mutex
	msgs []*dns.Msg
	// tsigStatus is the verification result of each recorded message.
	tsigStatus []error
}

func (s *fakeServer) messages() []*dns.Msg {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*dns.Msg(nil), s.msgs...)
}

// serverOptions tunes the reply of a fakeServer.
type serverOptions struct {
	rcode      int
	unsigned   bool
	secretName string
}

// startServer serves on a loopback port with the given transport. The reply
// is signed with the key of the request unless opts.unsigned is set.
func startServer(t *testing.T, network string, opts serverOptions) *fakeServer {
	t.Helper()

	srv := &fakeServer{}
	started := make(chan struct{})
	name := testKey
	if opts.secretName != "" {
		name = opts.secretName
	}

	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		srv.mu.Lock()
		srv.msgs = append(srv.msgs, r)
		srv.tsigStatus = append(srv.tsigStatus, w.TsigStatus())
		srv.mu.Unlock()

		reply := new(dns.Msg)
		reply.SetReply(r)
		reply.Rcode = opts.rcode
		if w.TsigStatus() != nil {
			// A real server refuses a request whose signature does not verify.
			reply.Rcode = dns.RcodeNotAuth
		}
		if tsig := r.IsTsig(); tsig != nil && w.TsigStatus() == nil && !opts.unsigned {
			reply.SetTsig(tsig.Hdr.Name, tsig.Algorithm, tsigFudge, time.Now().Unix())
		}
		if err := w.WriteMsg(reply); err != nil {
			t.Errorf("write reply: %v", err)
		}
	})

	server := &dns.Server{
		Net:               network,
		Handler:           handler,
		TsigSecret:        map[string]string{name: testSecret},
		MsgAcceptFunc:     func(dns.Header) dns.MsgAcceptAction { return dns.MsgAccept },
		NotifyStartedFunc: func() { close(started) },
	}

	switch network {
	case "tcp":
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server.Listener = ln
		srv.addr = ln.Addr().String()
	case "udp":
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server.PacketConn = pc
		srv.addr = pc.LocalAddr().String()
	default:
		t.Fatalf("unknown network %q", network)
	}

	go func() { _ = server.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = server.Shutdown() })

	return srv
}

func testConfig(server string) Config {
	return Config{
		Server:       server,
		Zone:         "Example.COM.",
		RRName:       "Home",
		KeyName:      "dnspatch-key",
		KeySecret:    testSecret,
		KeyAlgorithm: "hmac-sha256",
		TTL:          300,
		Protocol:     "tcp",
		Timeout:      5 * time.Second,
	}
}

func mustProvider(t *testing.T, cfg Config) *provider {
	t.Helper()

	p, err := newProvider(cfg)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	return p
}

func v4(s string) plugin.Addresses { return plugin.Addresses{V4: netip.MustParseAddr(s)} }
func v6(s string) plugin.Addresses { return plugin.Addresses{V6: netip.MustParseAddr(s)} }

// checkUpdate asserts that msg deletes the record set of typ at name and adds
// the record want.
func checkUpdate(t *testing.T, msg *dns.Msg, zone, name string, typ uint16, want dns.RR) {
	t.Helper()

	if msg.Opcode != dns.OpcodeUpdate {
		t.Fatalf("opcode = %s, want UPDATE", dns.OpcodeToString[msg.Opcode])
	}
	if len(msg.Question) != 1 || msg.Question[0].Name != zone || msg.Question[0].Qtype != dns.TypeSOA {
		t.Fatalf("zone section = %v, want one SOA question for %s", msg.Question, zone)
	}
	if len(msg.Ns) != 2 {
		t.Fatalf("update section has %d records, want 2: %v", len(msg.Ns), msg.Ns)
	}

	del := msg.Ns[0].Header()
	if del.Name != name || del.Rrtype != typ || del.Class != dns.ClassANY || del.Ttl != 0 {
		t.Errorf("first update record = %v, want delete of the %s rrset at %s", msg.Ns[0], dns.TypeToString[typ], name)
	}

	if got := msg.Ns[1].String(); got != want.String() {
		t.Errorf("second update record = %q, want %q", got, want.String())
	}
}

func TestUpdateSignedOverBothTransports(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			srv := startServer(t, network, serverOptions{})
			cfg := testConfig(srv.addr)
			cfg.Protocol = network

			if err := mustProvider(t, cfg).Update(t.Context(), v4("203.0.113.7"), plugin.RecordOptions{}); err != nil {
				t.Fatalf("Update: %v", err)
			}

			msgs := srv.messages()
			if len(msgs) != 1 {
				t.Fatalf("server got %d messages, want 1", len(msgs))
			}
			if srv.tsigStatus[0] != nil {
				t.Errorf("TSIG did not verify on the server: %v", srv.tsigStatus[0])
			}

			want, _ := dns.NewRR("home.example.com. 300 IN A 203.0.113.7")
			checkUpdate(t, msgs[0], "example.com.", "home.example.com.", dns.TypeA, want)
		})
	}
}

func TestUpdateWritesEachFamilySeparately(t *testing.T) {
	srv := startServer(t, "tcp", serverOptions{})

	addrs := plugin.Addresses{V4: netip.MustParseAddr("203.0.113.7"), V6: netip.MustParseAddr("2001:db8::7")}
	if err := mustProvider(t, testConfig(srv.addr)).Update(t.Context(), addrs, plugin.RecordOptions{}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	msgs := srv.messages()
	if len(msgs) != 2 {
		t.Fatalf("server got %d messages, want 2", len(msgs))
	}

	wantA, _ := dns.NewRR("home.example.com. 300 IN A 203.0.113.7")
	checkUpdate(t, msgs[0], "example.com.", "home.example.com.", dns.TypeA, wantA)
	wantAAAA, _ := dns.NewRR("home.example.com. 300 IN AAAA 2001:db8::7")
	checkUpdate(t, msgs[1], "example.com.", "home.example.com.", dns.TypeAAAA, wantAAAA)
}

func TestUpdateIPv6Only(t *testing.T) {
	srv := startServer(t, "tcp", serverOptions{})

	if err := mustProvider(t, testConfig(srv.addr)).Update(t.Context(), v6("2001:db8::7"), plugin.RecordOptions{}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	msgs := srv.messages()
	if len(msgs) != 1 {
		t.Fatalf("server got %d messages, want 1: an absent family must not be touched", len(msgs))
	}
	want, _ := dns.NewRR("home.example.com. 300 IN AAAA 2001:db8::7")
	checkUpdate(t, msgs[0], "example.com.", "home.example.com.", dns.TypeAAAA, want)
}

func TestUpdateUnmapsIPv4InIPv6(t *testing.T) {
	srv := startServer(t, "tcp", serverOptions{})

	mapped := plugin.Addresses{V4: netip.MustParseAddr("::ffff:203.0.113.7")}
	if err := mustProvider(t, testConfig(srv.addr)).Update(t.Context(), mapped, plugin.RecordOptions{}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	want, _ := dns.NewRR("home.example.com. 300 IN A 203.0.113.7")
	checkUpdate(t, srv.messages()[0], "example.com.", "home.example.com.", dns.TypeA, want)
}

func TestUpdateApexAndOptionsTTL(t *testing.T) {
	srv := startServer(t, "tcp", serverOptions{})
	cfg := testConfig(srv.addr)
	cfg.RRName = "@"

	if err := mustProvider(t, cfg).Update(t.Context(), v4("203.0.113.7"), plugin.RecordOptions{TTL: 90 * time.Second}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	want, _ := dns.NewRR("example.com. 90 IN A 203.0.113.7")
	checkUpdate(t, srv.messages()[0], "example.com.", "example.com.", dns.TypeA, want)
}

func TestUpdateUnsigned(t *testing.T) {
	srv := startServer(t, "tcp", serverOptions{})
	cfg := testConfig(srv.addr)
	cfg.KeyName, cfg.KeySecret = "", ""

	if err := mustProvider(t, cfg).Update(t.Context(), v4("203.0.113.7"), plugin.RecordOptions{}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	msgs := srv.messages()
	if len(msgs) != 1 || msgs[0].IsTsig() != nil {
		t.Fatalf("want one unsigned message, got %d (tsig=%v)", len(msgs), msgs[0].IsTsig())
	}
}

func TestUpdateFailures(t *testing.T) {
	tests := []struct {
		name    string
		opts    serverOptions
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:    "server refuses",
			opts:    serverOptions{rcode: dns.RcodeRefused},
			wantErr: "REFUSED",
		},
		{
			name:    "server fails",
			opts:    serverOptions{rcode: dns.RcodeServerFailure},
			wantErr: "SERVFAIL",
		},
		{
			name:    "reply is not signed",
			opts:    serverOptions{unsigned: true},
			wantErr: "did not sign",
		},
		{
			name:    "wrong secret",
			mutate:  func(c *Config) { c.KeySecret = "d3Jvbmctd3Jvbmctd3Jvbmc=" },
			wantErr: "NOTAUTH",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := startServer(t, "tcp", tc.opts)
			cfg := testConfig(srv.addr)
			if tc.mutate != nil {
				tc.mutate(&cfg)
			}

			err := mustProvider(t, cfg).Update(t.Context(), v4("203.0.113.7"), plugin.RecordOptions{})
			if err == nil {
				t.Fatal("Update succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestUpdateWithoutAddress(t *testing.T) {
	p := mustProvider(t, testConfig("127.0.0.1:1"))

	if err := p.Update(t.Context(), plugin.Addresses{}, plugin.RecordOptions{}); err == nil {
		t.Fatal("Update succeeded with no address, want an error")
	}
}

func TestUpdateHonoursContext(t *testing.T) {
	// A listener that accepts and never answers.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = mustProvider(t, testConfig(ln.Addr().String())).Update(ctx, v4("203.0.113.7"), plugin.RecordOptions{})
	if err == nil {
		t.Fatal("Update succeeded against a silent server, want an error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Update took %s after the context expired", elapsed)
	}
}

func TestNewProviderValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"empty server", func(c *Config) { c.Server = "" }, "server"},
		{"server with empty port", func(c *Config) { c.Server = "ns1.example.com:" }, "server"},
		{"empty zone", func(c *Config) { c.Zone = " " }, "zone"},
		{"slash in zone", func(c *Config) { c.Zone = "example.com/x" }, "zone"},
		{"empty rr_name", func(c *Config) { c.RRName = "" }, "rr_name"},
		{"rr_name with trailing dot", func(c *Config) { c.RRName = "home." }, "rr_name"},
		{"negative ttl", func(c *Config) { c.TTL = -1 }, "ttl"},
		{"huge ttl", func(c *Config) { c.TTL = 1 << 40 }, "ttl"},
		{"zero timeout", func(c *Config) { c.Timeout = 0 }, "timeout"},
		{"unknown protocol", func(c *Config) { c.Protocol = "tls" }, "protocol"},
		{"key name without secret", func(c *Config) { c.KeySecret = "" }, "key_name and key_secret"},
		{"secret without key name", func(c *Config) { c.KeyName = "" }, "key_name and key_secret"},
		{"unknown algorithm", func(c *Config) { c.KeyAlgorithm = "hmac-md5" }, "key_algorithm"},
		{"secret is not base64", func(c *Config) { c.KeySecret = "not base64!" }, "base64"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig("ns1.example.com")
			tc.mutate(&cfg)

			_, err := newProvider(cfg)
			if err == nil {
				t.Fatal("newProvider succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestServerAddress(t *testing.T) {
	tests := map[string]string{
		"ns1.example.com":      "ns1.example.com:53",
		"ns1.example.com:5353": "ns1.example.com:5353",
		"192.0.2.1":            "192.0.2.1:53",
		"[2001:db8::1]:5353":   "[2001:db8::1]:5353",
		"2001:db8::1":          "[2001:db8::1]:53",
		"[2001:db8::1]":        "[2001:db8::1]:53",
		"  ns1.example.com  ":  "ns1.example.com:53",
	}

	for in, want := range tests {
		got, err := serverAddress(in)
		if err != nil {
			t.Errorf("serverAddress(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("serverAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAlgorithms(t *testing.T) {
	for name, algo := range algorithms {
		t.Run(name, func(t *testing.T) {
			srv := startServer(t, "tcp", serverOptions{})
			cfg := testConfig(srv.addr)
			cfg.KeyAlgorithm = strings.ToUpper(name) + "."

			if err := mustProvider(t, cfg).Update(t.Context(), v4("203.0.113.7"), plugin.RecordOptions{}); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if got := srv.messages()[0].IsTsig().Algorithm; got != algo {
				t.Errorf("signed with %q, want %q", got, algo)
			}
		})
	}
}
