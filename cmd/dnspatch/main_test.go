package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/internal/config"
	"github.com/KrimsN/dnspatch/plugin"
)

// fakeWorld plays both external services: the IP echo and the REG.RU DNS API.
// It keeps the contents of the A and AAAA records at home.example.com and
// counts how many records the daemon has added and how often it asked for
// its address.
type fakeWorld struct {
	mu        sync.Mutex
	ip        string
	contents  []string // A records
	contents6 []string // AAAA records
	adds      int
	lookups   int
}

func (w *fakeWorld) setIP(ip string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ip = ip
}

// snapshot returns the record contents, comma-joined, and the number of adds.
func (w *fakeWorld) snapshot() (content string, adds int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.contents, ","), w.adds
}

// snapshot6 is snapshot for the AAAA records.
func (w *fakeWorld) snapshot6() (content string, adds int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.contents6, ","), w.adds
}

// lookupCount returns how many times the daemon has asked for its address.
func (w *fakeWorld) lookupCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lookups
}

func (w *fakeWorld) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if r.URL.Path == "/ip" {
		w.lookups++
		_, _ = fmt.Fprint(rw, w.ip)
		return
	}

	var input struct {
		Ipaddr     string `json:"ipaddr"`
		Content    string `json:"content"`
		RecordType string `json:"record_type"`
	}
	_ = json.Unmarshal([]byte(r.FormValue("input_data")), &input)

	rrs := []map[string]string{}

	switch r.URL.Path {
	case "/zone/get_resource_records":
		for _, content := range w.contents {
			rrs = append(rrs, map[string]string{"subname": "home", "rectype": "A", "content": content})
		}
		for _, content := range w.contents6 {
			rrs = append(rrs, map[string]string{"subname": "home", "rectype": "AAAA", "content": content})
		}
	case "/zone/add_alias":
		w.contents = append(w.contents, input.Ipaddr)
		w.adds++
	case "/zone/add_aaaa":
		w.contents6 = append(w.contents6, input.Ipaddr)
		w.adds++
	case "/zone/remove_record":
		if input.RecordType == "AAAA" {
			w.contents6 = slices.DeleteFunc(w.contents6, func(content string) bool { return content == input.Content })
		} else {
			w.contents = slices.DeleteFunc(w.contents, func(content string) bool { return content == input.Content })
		}
	default:
		http.NotFound(rw, r)
		return
	}

	_ = json.NewEncoder(rw).Encode(map[string]any{
		"result": "success",
		"answer": map[string]any{"domains": []any{map[string]any{"dname": "example.com", "result": "success", "rrs": rrs}}},
	})
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dnspatch.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestDaemonEndToEnd(t *testing.T) {
	world := &fakeWorld{ip: "203.0.113.7"}
	srv := httptest.NewServer(world)
	defer srv.Close()

	path := writeConfig(t, fmt.Sprintf(`
interval = "1s"

[retriever.echo]
type     = "ifconfigco"
base_url = %[1]q

[provider.dns]
type     = "regru"
username = "user"
password = "secret"
zone     = "example.com"
rr_name  = "home"
base_url = %[1]q

[[instance]]
name = "home"
[[instance.retriever]]
ref = "echo"
[[instance.provider]]
ref = "dns"
`, srv.URL))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"--config", path}, &bytes.Buffer{}, &syncBuffer{buf: &stderr}, plugin.Default)
	}()

	waitFor(t, "first address written", func() bool {
		content, _ := world.snapshot()
		return content == "203.0.113.7"
	})

	// An unchanged address must not cause further writes. A tick starts only
	// after the previous one is finished, so the third lookup proves that the
	// second tick, which saw the same address again, has been fully handled.
	waitFor(t, "a tick with an unchanged address", func() bool { return world.lookupCount() >= 3 })
	if _, adds := world.snapshot(); adds != 1 {
		t.Errorf("records added after unchanged ticks = %d, want 1", adds)
	}

	// A new address is picked up on a later tick.
	world.setIP("203.0.113.99")
	waitFor(t, "changed address written", func() bool {
		content, _ := world.snapshot()
		return content == "203.0.113.99"
	})

	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("exit code = %d, want %d", code, exitOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after the context was cancelled")
	}
}

// ipv6Echo serves a fixed address at /ip on an IPv6-only loopback listener, so
// a retriever configured with family = "ipv6" has something real to dial.
type ipv6Echo struct {
	addr string
}

func (e *ipv6Echo) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ip" {
		http.NotFound(rw, r)
		return
	}
	_, _ = fmt.Fprint(rw, e.addr)
}

func newIPv6EchoServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()

	listener, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback available: %v", err)
	}

	srv := &httptest.Server{Listener: listener, Config: &http.Server{Handler: h}}
	srv.Start()
	t.Cleanup(srv.Close)

	return srv
}

func TestDaemonWritesBothFamiliesFromTwoRetrievers(t *testing.T) {
	world := &fakeWorld{ip: "203.0.113.7"}
	srv := httptest.NewServer(world)
	defer srv.Close()

	echo6 := &ipv6Echo{addr: "2001:db8::1"}
	srv6 := newIPv6EchoServer(t, echo6)

	path := writeConfig(t, fmt.Sprintf(`
interval = "1s"

[retriever.v4]
type     = "ifconfigco"
family   = "ipv4"
base_url = %[1]q

[retriever.v6]
type     = "ifconfigco"
family   = "ipv6"
base_url = %[2]q

[provider.dns]
type     = "regru"
username = "user"
password = "secret"
zone     = "example.com"
rr_name  = "home"
base_url = %[1]q

[[instance]]
name = "home"
[[instance.retriever]]
ref = "v4"
[[instance.retriever]]
ref = "v6"
[[instance.provider]]
ref = "dns"
`, srv.URL, srv6.URL))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"--config", path}, &bytes.Buffer{}, &bytes.Buffer{}, plugin.Default)
	}()

	waitFor(t, "both records written", func() bool {
		v4, _ := world.snapshot()
		v6, _ := world.snapshot6()
		return v4 == "203.0.113.7" && v6 == "2001:db8::1"
	})

	cancel()
	select {
	case code := <-done:
		if code != exitOK {
			t.Errorf("exit code = %d, want %d", code, exitOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after the context was cancelled")
	}
}

func TestBadConfigExitsWithConfigCode(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name: "unknown provider type",
			config: `
[retriever.echo]
type = "ifconfigco"
[provider.dns]
type = "nosuch"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "echo"
[[instance.provider]]
ref = "dns"
`,
			wantErr: `unknown provider type "nosuch"`,
		},
		{
			name: "missing required parameter",
			config: `
[retriever.echo]
type = "ifconfigco"
[provider.dns]
type = "regru"
username = "user"
zone = "example.com"
rr_name = "home"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "echo"
[[instance.provider]]
ref = "dns"
`,
			wantErr: "password",
		},
		{
			name: "provider listed twice",
			config: `
[retriever.echo]
type = "ifconfigco"
[provider.dns]
type     = "regru"
username = "user"
password = "secret"
zone     = "example.com"
rr_name  = "home"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "echo"
[[instance.provider]]
ref = "dns"
[[instance.provider]]
ref = "dns"
`,
			wantErr: "repeats provider #1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer

			code := run(context.Background(), []string{"--config", writeConfig(t, tt.config)}, &bytes.Buffer{}, &stderr, plugin.Default)
			if code != exitConfig {
				t.Errorf("exit code = %d, want %d", code, exitConfig)
			}
			if !strings.Contains(stderr.String(), tt.wantErr) || !strings.Contains(stderr.String(), `instance "x"`) {
				t.Errorf("stderr = %q, want it to name the instance and contain %q", stderr.String(), tt.wantErr)
			}
		})
	}
}

type fakeRetrieverConfig struct {
	Family string `toml:"family"`
}

type fakeRetriever struct{ family string }

func (r *fakeRetriever) GetAddresses(context.Context) (plugin.Addresses, error) {
	if r.family == "ipv6" {
		return plugin.Addresses{V6: netip.MustParseAddr("2001:db8::1")}, nil
	}
	return plugin.Addresses{V4: netip.MustParseAddr("203.0.113.1")}, nil
}

type fakeProviderStub struct{}

func (fakeProviderStub) Update(context.Context, plugin.Addresses, plugin.RecordOptions) error {
	return nil
}

func TestBuildInstancesWiresUpToTwoRetrieversPerInstance(t *testing.T) {
	registry := plugin.NewRegistry()
	plugin.RegisterRetrieverIn(registry, "fake", func(cfg fakeRetrieverConfig) (plugin.Retriever, error) {
		return &fakeRetriever{family: cfg.Family}, nil
	})
	plugin.RegisterProviderIn(registry, "fake", func(fakeRetrieverConfig) (plugin.Provider, error) {
		return fakeProviderStub{}, nil
	})

	cfg, err := config.Parse([]byte(`
[retriever.v4]
type   = "fake"
family = "ipv4"
[retriever.v6]
type   = "fake"
family = "ipv6"
[provider.main]
type = "fake"

[[instance]]
name = "dual"
[[instance.retriever]]
ref = "v4"
[[instance.retriever]]
ref = "v6"
[[instance.provider]]
ref = "main"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	instances, err := buildInstances(cfg, registry)
	if err != nil {
		t.Fatalf("buildInstances: %v", err)
	}

	if len(instances) != 1 || len(instances[0].Retrievers) != 2 {
		t.Fatalf("instances = %+v, want 1 instance with 2 retrievers", instances)
	}

	names := []string{instances[0].Retrievers[0].Name, instances[0].Retrievers[1].Name}
	if !slices.Equal(names, []string{"v4", "v6"}) {
		t.Errorf("retriever names = %v, want [v4 v6]", names)
	}

	addrs0, _ := instances[0].Retrievers[0].Retriever.GetAddresses(context.Background())
	addrs1, _ := instances[0].Retrievers[1].Retriever.GetAddresses(context.Background())
	if !addrs0.V4.IsValid() || !addrs1.V6.IsValid() {
		t.Errorf("addresses = %+v (v4), %+v (v6); families were not wired to the right retriever", addrs0, addrs1)
	}
}

func TestMissingConfigFile(t *testing.T) {
	var stderr bytes.Buffer

	code := run(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "absent.toml")}, &bytes.Buffer{}, &stderr, plugin.Default)
	if code != exitConfig {
		t.Errorf("exit code = %d, want %d", code, exitConfig)
	}
}

func TestVersionFlag(t *testing.T) {
	var stdout bytes.Buffer

	if code := run(context.Background(), []string{"--version"}, &stdout, &bytes.Buffer{}, plugin.Default); code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(stdout.String(), "dnspatch ") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestUnknownFlag(t *testing.T) {
	if code := run(context.Background(), []string{"--nope"}, &bytes.Buffer{}, &bytes.Buffer{}, plugin.Default); code != exitConfig {
		t.Errorf("exit code = %d, want %d", code, exitConfig)
	}
}

// syncBuffer lets the daemon's logger and the test share a buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf *bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		env     string
		want    slog.Level
		wantErr string
	}{
		{name: "default is info", want: slog.LevelInfo},
		{name: "flag", flag: "debug", want: slog.LevelDebug},
		{name: "env", env: "warn", want: slog.LevelWarn},
		{name: "flag beats env", flag: "error", env: "debug", want: slog.LevelError},
		{name: "case does not matter", flag: "DEBUG", want: slog.LevelDebug},
		{name: "bad flag", flag: "loud", wantErr: "--log-level"},
		{name: "bad env", env: "loud", wantErr: envLogLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLogLevel(tt.flag, tt.env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLogLevel: %v", err)
			}
			if got != tt.want {
				t.Errorf("level = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBadLogLevelExitsWithConfigCode(t *testing.T) {
	var stderr bytes.Buffer

	code := run(context.Background(), []string{"--log-level", "loud"}, &bytes.Buffer{}, &stderr, plugin.Default)
	if code != exitConfig {
		t.Errorf("exit code = %d, want %d", code, exitConfig)
	}
	if !strings.Contains(stderr.String(), "--log-level") {
		t.Errorf("stderr = %q, want it to name the flag", stderr.String())
	}
}
