package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	"github.com/dnspatch/dnspatch/internal/config"
	"github.com/dnspatch/dnspatch/internal/health"
	"github.com/dnspatch/dnspatch/internal/runner"
	"github.com/dnspatch/dnspatch/plugin"
	_ "github.com/dnspatch/dnspatch/plugins/all"
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

func runWith(registry *plugin.Registry, hooks HookBuilder) Options {
	return Options{Registry: registry, Hooks: hooks}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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
		done <- Run(ctx, []string{"--config", path}, &bytes.Buffer{}, &syncBuffer{buf: &stderr}, runWith(plugin.Default, nil))
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
		if code != ExitOK {
			t.Errorf("exit code = %d, want %d", code, ExitOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after the context was cancelled")
	}
}

// TestDaemonRecordsHealthStatus checks that the daemon wires health.Recorder
// into every instance on its own, with no config needed: unit coverage for
// the recorder and the freshness check themselves lives in internal/health.
func TestDaemonRecordsHealthStatus(t *testing.T) {
	t.Setenv(health.EnvDir, t.TempDir())

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

	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, []string{"--config", path}, &bytes.Buffer{}, &bytes.Buffer{}, runWith(plugin.Default, nil))
	}()

	waitFor(t, "the healthcheck subcommand to report healthy", func() bool {
		var stdout bytes.Buffer
		code := Run(context.Background(), []string{"healthcheck", "--config", path}, &stdout, &bytes.Buffer{}, runWith(plugin.Default, nil))
		return code == ExitOK
	})

	cancel()
	select {
	case code := <-done:
		if code != ExitOK {
			t.Errorf("exit code = %d, want %d", code, ExitOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after the context was cancelled")
	}
}

// minimalConfig defines one retriever and one provider definition, of types
// that need not be registered: the healthcheck subcommand only parses the
// config to learn each instance's name and interval, it never builds
// plugins.
const minimalConfig = `
[retriever.home]
type = "unused"

[provider.main]
type = "unused"
`

func TestHealthCheckSubcommandReportsUnhealthyWithNoStatusFile(t *testing.T) {
	t.Setenv(health.EnvDir, t.TempDir())

	path := writeConfig(t, minimalConfig+`
[[instance]]
name = "a"
[[instance.retriever]]
ref = "home"
[[instance.provider]]
ref = "main"
`)

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"healthcheck", "--config", path}, &stdout, &stderr, runWith(plugin.Default, nil))

	if code != ExitFailure {
		t.Errorf("exit code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr.String(), `instance "a"`) {
		t.Errorf("stderr = %q, want it to name the stuck instance", stderr.String())
	}
}

func TestHealthCheckSubcommandReportsHealthyWithAFreshStatusFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(health.EnvDir, dir)
	if err := os.WriteFile(filepath.Join(dir, "a"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	path := writeConfig(t, minimalConfig+`
[[instance]]
name = "a"
[[instance.retriever]]
ref = "home"
[[instance.provider]]
ref = "main"
`)

	var stdout bytes.Buffer
	code := Run(context.Background(), []string{"healthcheck", "--config", path}, &stdout, &bytes.Buffer{}, runWith(plugin.Default, nil))

	if code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "healthy") {
		t.Errorf("stdout = %q, want it to report healthy", stdout.String())
	}
}

func TestHealthCheckSubcommandExitsWithConfigCodeOnAnInvalidConfig(t *testing.T) {
	var stderr bytes.Buffer

	code := Run(context.Background(), []string{"healthcheck", "--config", filepath.Join(t.TempDir(), "absent.toml")}, &bytes.Buffer{}, &stderr, runWith(plugin.Default, nil))
	if code != ExitConfig {
		t.Errorf("exit code = %d, want %d", code, ExitConfig)
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
		done <- Run(ctx, []string{"--config", path}, &bytes.Buffer{}, &bytes.Buffer{}, runWith(plugin.Default, nil))
	}()

	waitFor(t, "both records written", func() bool {
		v4, _ := world.snapshot()
		v6, _ := world.snapshot6()
		return v4 == "203.0.113.7" && v6 == "2001:db8::1"
	})

	cancel()
	select {
	case code := <-done:
		if code != ExitOK {
			t.Errorf("exit code = %d, want %d", code, ExitOK)
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
		{
			name: "ping_url set but the build has no hooks",
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
name     = "x"
ping_url = "https://hc-ping.com/abc"
[[instance.retriever]]
ref = "echo"
[[instance.provider]]
ref = "dns"
`,
			wantErr: "does not support monitoring hooks",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer

			code := Run(context.Background(), []string{"--config", writeConfig(t, tt.config)}, &bytes.Buffer{}, &stderr, runWith(plugin.Default, nil))
			if code != ExitConfig {
				t.Errorf("exit code = %d, want %d", code, ExitConfig)
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

	instances, err := buildInstances(cfg, registry, nil, discardLogger())
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

	families := []string{instances[0].Retrievers[0].Family, instances[0].Retrievers[1].Family}
	if !slices.Equal(families, []string{"ipv4", "ipv6"}) {
		t.Errorf("retriever families = %v, want [ipv4 ipv6]: the runner needs this hint to skip unneeded retrievers", families)
	}
}

func TestBuildInstancesLowercasesTheFamilyHintAndLeavesItEmptyWhenAbsent(t *testing.T) {
	registry := plugin.NewRegistry()
	plugin.RegisterRetrieverIn(registry, "fake", func(cfg fakeRetrieverConfig) (plugin.Retriever, error) {
		return &fakeRetriever{family: cfg.Family}, nil
	})
	plugin.RegisterProviderIn(registry, "fake", func(fakeRetrieverConfig) (plugin.Provider, error) {
		return fakeProviderStub{}, nil
	})

	cfg, err := config.Parse([]byte(`
[retriever.pinned]
type = "fake"
[retriever.plain]
type = "fake"
[provider.main]
type = "fake"

[[instance]]
name = "mixed"
[[instance.retriever]]
ref    = "pinned"
family = "IPv4"
[[instance.retriever]]
ref = "plain"
[[instance.provider]]
ref = "main"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	instances, err := buildInstances(cfg, registry, nil, discardLogger())
	if err != nil {
		t.Fatalf("buildInstances: %v", err)
	}

	families := []string{instances[0].Retrievers[0].Family, instances[0].Retrievers[1].Family}
	if !slices.Equal(families, []string{"ipv4", ""}) {
		t.Errorf("retriever families = %v, want [ipv4 \"\"]: mixed case is lowercased, an absent parameter leaves it empty", families)
	}
}

// TestBuildInstancesPerFamilyFallbackChainFromDocs builds the documentation's
// worked example of a per-family fallback chain (dual + ipv6-only +
// ipv4-only retrievers, all real plugins) to make sure it is valid
// configuration and wires the Family hints as documented.
func TestBuildInstancesPerFamilyFallbackChainFromDocs(t *testing.T) {
	cfg, err := config.Parse([]byte(`
[retriever.icanhazip]
type   = "icanhazip"
family = "dual"

[retriever.ipify]
type   = "ipify"
family = "ipv6"

[retriever.ifconfigco]
type   = "ifconfigco"
family = "ipv4"

[provider.regru]
type     = "regru"
username = "u"
password = "p"
zone     = "example.com"
rr_name  = "home"

[[instance]]
name = "home"

[[instance.retriever]]
ref = "icanhazip"

[[instance.retriever]]
ref = "ipify"

[[instance.retriever]]
ref = "ifconfigco"

[[instance.provider]]
ref = "regru"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	instances, err := buildInstances(cfg, plugin.Default, nil, discardLogger())
	if err != nil {
		t.Fatalf("buildInstances: %v", err)
	}

	families := make([]string, len(instances[0].Retrievers))
	for i, r := range instances[0].Retrievers {
		families[i] = r.Family
	}
	if want := []string{"dual", "ipv6", "ipv4"}; !slices.Equal(families, want) {
		t.Errorf("retriever families = %v, want %v", families, want)
	}
}

// TestBuildInstancesWiresHooksFromPingURL checks that a build with a
// HookBuilder attaches the hooks it returns to the right instance, and that
// an instance without ping_url gets none.
func TestBuildInstancesWiresHooksFromPingURL(t *testing.T) {
	registry := plugin.NewRegistry()
	plugin.RegisterRetrieverIn(registry, "fake", func(fakeRetrieverConfig) (plugin.Retriever, error) {
		return &fakeRetriever{}, nil
	})
	plugin.RegisterProviderIn(registry, "fake", func(fakeRetrieverConfig) (plugin.Provider, error) {
		return fakeProviderStub{}, nil
	})

	cfg, err := config.Parse([]byte(`
[retriever.r]
type = "fake"
[provider.p]
type = "fake"

[[instance]]
name     = "pinged"
ping_url = "https://hc-ping.com/abc"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "p"

[[instance]]
name = "plain"
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "p"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	var built []config.Instance
	fake := struct{ runner.Hook }{}
	buildHooks := func(inst config.Instance, _ *slog.Logger) ([]runner.Hook, error) {
		built = append(built, inst)
		return []runner.Hook{fake}, nil
	}

	instances, err := buildInstances(cfg, registry, buildHooks, discardLogger())
	if err != nil {
		t.Fatalf("buildInstances: %v", err)
	}

	if len(built) != 1 || built[0].Name != "pinged" {
		t.Fatalf("HookBuilder called for %+v, want only the instance with ping_url set", built)
	}

	if len(instances[0].Hooks) != 1 {
		t.Errorf("pinged instance hooks = %d, want 1", len(instances[0].Hooks))
	}
	if len(instances[1].Hooks) != 0 {
		t.Errorf("plain instance hooks = %d, want 0", len(instances[1].Hooks))
	}
}

// notifyConfig builds a config with two instances, a and b, and one
// [notify.<type>] definition per given type, using a registry with a "fake"
// retriever/provider type so the test does not depend on any real plugin. Both
// instances get every definition.
func notifyConfig(t *testing.T, notifyTypes ...string) (string, *plugin.Registry) {
	t.Helper()

	return notifyConfigWith(t, notifyTypes, "", "")
}

// notifyConfigWith is notifyConfig with the given extra lines in instance a and
// in instance b, for example `notify = ["redis"]`.
func notifyConfigWith(t *testing.T, notifyTypes []string, aExtra, bExtra string) (string, *plugin.Registry) {
	t.Helper()

	registry := plugin.NewRegistry()
	plugin.RegisterRetrieverIn(registry, "fake", func(fakeRetrieverConfig) (plugin.Retriever, error) {
		return &fakeRetriever{}, nil
	})
	plugin.RegisterProviderIn(registry, "fake", func(fakeRetrieverConfig) (plugin.Provider, error) {
		return fakeProviderStub{}, nil
	})

	var notify strings.Builder
	for _, typ := range notifyTypes {
		fmt.Fprintf(&notify, "[notify.%[1]s]\ntype = %[1]q\n\n", typ)
	}

	path := writeConfig(t, fmt.Sprintf(`
[retriever.r]
type = "fake"
[provider.p]
type = "fake"

%[1]s[[instance]]
name = "a"
%[2]s[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "p"

[[instance]]
name = "b"
%[3]s[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "p"
`, notify.String(), aExtra, bExtra))

	return path, registry
}

func TestNotifyRejectedWhenBuildHasNoNotifyBackend(t *testing.T) {
	path, registry := notifyConfig(t, "redis")

	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--config", path, "--check-config"}, &bytes.Buffer{}, &stderr, runWith(registry, nil))

	if code != ExitConfig {
		t.Errorf("exit code = %d, want %d", code, ExitConfig)
	}
	if !strings.Contains(stderr.String(), `"redis"`) || !strings.Contains(stderr.String(), "does not support a notify backend") {
		t.Errorf("stderr = %q, want it to name the notifier and explain why", stderr.String())
	}
}

// A definition no instance uses is not something the build has to support.
func TestUnusedNotifierIsAcceptedByABuildWithoutBackends(t *testing.T) {
	path, registry := notifyConfigWith(t, []string{"redis"}, "notify = []\n", "notify = []\n")

	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--config", path, "--check-config"}, &bytes.Buffer{}, &stderr, runWith(registry, nil))

	if code != ExitOK {
		t.Errorf("exit code = %d, want %d; stderr: %s", code, ExitOK, stderr.String())
	}
}

func TestNotifyBuilderErrorExitsWithConfigCode(t *testing.T) {
	path, registry := notifyConfig(t, "redis")

	opts := runWith(registry, nil)
	opts.Notify = func(config.Plugin, *slog.Logger) (NotifyConnection, error) {
		return nil, errors.New("no such host")
	}

	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--config", path, "--check-config"}, &bytes.Buffer{}, &stderr, opts)

	if code != ExitConfig {
		t.Errorf("exit code = %d, want %d", code, ExitConfig)
	}
	if !strings.Contains(stderr.String(), "no such host") {
		t.Errorf("stderr = %q, want it to contain the NotifyBuilder's error", stderr.String())
	}
}

// recordingConn is a NotifyConnection that hands out one shared hook and
// remembers the events each instance asked for and how often it was closed.
type recordingConn struct {
	hook *recordingHook

	mu     sync.Mutex
	asked  [][]config.Event
	closed int
}

func (c *recordingConn) Hook(events []config.Event) runner.Hook {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, events)
	return c.hook
}

func (c *recordingConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
	return nil
}

// recordingHook is a runner.Hook that records which instances it was
// notified for.
type recordingHook struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (h *recordingHook) AfterCycle(_ context.Context, ev runner.CycleEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.seen == nil {
		h.seen = map[string]bool{}
	}
	h.seen[ev.Instance] = true
}

func (h *recordingHook) sawAll(instances ...string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, name := range instances {
		if !h.seen[name] {
			return false
		}
	}
	return true
}

func (h *recordingHook) saw(instance string) bool {
	return h.sawAll(instance)
}

// runNotifyDaemon runs the daemon on a config until every wanted hook has
// seen the instances it is expected to see, then stops it.
func runNotifyDaemon(t *testing.T, path string, opts Options, ready func() bool) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, []string{"--config", path}, &bytes.Buffer{}, &bytes.Buffer{}, opts)
	}()

	waitFor(t, "the notify hooks to see their instances", ready)

	cancel()
	select {
	case code := <-done:
		if code != ExitOK {
			t.Errorf("exit code = %d, want %d", code, ExitOK)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after the context was cancelled")
	}
}

// TestNotifyBuilderRunsOnceAndReachesEveryInstance checks that NotifyBuilder,
// unlike HookBuilder, is called once per definition rather than once per
// instance, and that the hook it returns is attached to every instance that
// lists it, not just one.
func TestNotifyBuilderRunsOnceAndReachesEveryInstance(t *testing.T) {
	path, registry := notifyConfig(t, "redis")

	var calls int
	var gotType, gotName string
	hook := &recordingHook{}
	opts := runWith(registry, nil)
	opts.Notify = func(cfg config.Plugin, _ *slog.Logger) (NotifyConnection, error) {
		calls++
		gotType, gotName = cfg.Type, cfg.Name()
		return &recordingConn{hook: hook}, nil
	}

	runNotifyDaemon(t, path, opts, func() bool { return hook.sawAll("a", "b") })

	if calls != 1 {
		t.Errorf("NotifyBuilder called %d times, want 1 (one definition)", calls)
	}
	if gotType != "redis" || gotName != "redis" {
		t.Errorf("NotifyBuilder got type %q and name %q, want redis for both", gotType, gotName)
	}
}

// Every definition gets its own hook, and an instance that does not list
// notifiers reaches all of them.
func TestSeveralNotifiersEachReachEveryInstance(t *testing.T) {
	path, registry := notifyConfig(t, "redis", "mqtt")

	hooks := map[string]*recordingHook{"redis": {}, "mqtt": {}}
	opts := runWith(registry, nil)
	opts.Notify = func(cfg config.Plugin, _ *slog.Logger) (NotifyConnection, error) {
		return &recordingConn{hook: hooks[cfg.Type]}, nil
	}

	runNotifyDaemon(t, path, opts, func() bool {
		return hooks["redis"].sawAll("a", "b") && hooks["mqtt"].sawAll("a", "b")
	})
}

// An instance publishes to the notifiers it lists and to no others, and a
// notifier that two instances list is one connection, built once.
func TestInstancesPublishOnlyToTheNotifiersTheyList(t *testing.T) {
	path, registry := notifyConfigWith(t, []string{"redis", "mqtt"}, `notify = ["redis"]`+"\n", `notify = ["redis", "mqtt"]`+"\n")

	hooks := map[string]*recordingHook{"redis": {}, "mqtt": {}}
	built := map[string]int{}
	opts := runWith(registry, nil)
	opts.Notify = func(cfg config.Plugin, _ *slog.Logger) (NotifyConnection, error) {
		built[cfg.Type]++
		return &recordingConn{hook: hooks[cfg.Type]}, nil
	}

	runNotifyDaemon(t, path, opts, func() bool {
		return hooks["redis"].sawAll("a", "b") && hooks["mqtt"].saw("b")
	})

	if hooks["mqtt"].saw("a") {
		t.Error("instance a published to mqtt, which it does not list")
	}
	if built["redis"] != 1 || built["mqtt"] != 1 {
		t.Errorf("notifiers built %v, want each once", built)
	}
}

// A definition that no instance lists is never built.
func TestNotifierNoInstanceListsIsNotBuilt(t *testing.T) {
	path, registry := notifyConfigWith(t, []string{"redis", "mqtt"}, `notify = ["redis"]`+"\n", `notify = ["redis"]`+"\n")

	hook := &recordingHook{}
	opts := runWith(registry, nil)
	opts.Notify = func(cfg config.Plugin, _ *slog.Logger) (NotifyConnection, error) {
		if cfg.Type == "mqtt" {
			t.Error("the mqtt notifier was built, but no instance lists it")
		}
		return &recordingConn{hook: hook}, nil
	}

	runNotifyDaemon(t, path, opts, func() bool { return hook.sawAll("a", "b") })
}

// A definition the build cannot serve is reported by name, and one bad
// definition does not hide the problem of another.
func TestNotifyErrorsNameTheDefinitionAndAreReportedTogether(t *testing.T) {
	path, registry := notifyConfig(t, "redis", "mqtt", "amqp")

	opts := runWith(registry, nil)
	opts.Notify = func(cfg config.Plugin, _ *slog.Logger) (NotifyConnection, error) {
		if cfg.Type == "redis" {
			return &recordingConn{hook: &recordingHook{}}, nil
		}
		return nil, errors.New("unknown backend")
	}

	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--config", path, "--check-config"}, &bytes.Buffer{}, &stderr, opts)

	if code != ExitConfig {
		t.Errorf("exit code = %d, want %d", code, ExitConfig)
	}
	for _, want := range []string{`notify "mqtt" (mqtt): unknown backend`, `notify "amqp" (amqp): unknown backend`} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
	if strings.Contains(stderr.String(), `notify "redis"`) {
		t.Errorf("stderr = %q, want no complaint about the working notifier", stderr.String())
	}
}

func TestCheckConfigValidatesWithoutStartingTheDaemon(t *testing.T) {
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

	var stdout bytes.Buffer

	code := Run(context.Background(), []string{"--config", path, "--check-config"}, &stdout, &bytes.Buffer{}, runWith(plugin.Default, nil))
	if code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}

	if got := stdout.String(); !strings.Contains(got, path) ||
		!strings.Contains(got, `home: interval=1s retrievers=[echo] providers=[dns]`) {
		t.Errorf("stdout = %q, want it to name the config path and describe instance %q", got, "home")
	}

	// runner.Run never started: nothing was looked up or written.
	if world.lookupCount() != 0 {
		t.Errorf("lookups = %d, want 0: --check-config must not start the daemon", world.lookupCount())
	}
}

func TestCheckConfigReportsTheRetrieverFamilyHint(t *testing.T) {
	registry := plugin.NewRegistry()
	plugin.RegisterRetrieverIn(registry, "fake", func(fakeRetrieverConfig) (plugin.Retriever, error) {
		return &fakeRetriever{}, nil
	})
	plugin.RegisterProviderIn(registry, "fake", func(fakeRetrieverConfig) (plugin.Provider, error) {
		return fakeProviderStub{}, nil
	})

	path := writeConfig(t, `
[retriever.v4]
type   = "fake"
family = "ipv4"
[provider.main]
type = "fake"

[[instance]]
name = "dual"
[[instance.retriever]]
ref = "v4"
[[instance.provider]]
ref = "main"
`)

	var stdout bytes.Buffer

	code := Run(context.Background(), []string{"--config", path, "--check-config"}, &stdout, &bytes.Buffer{}, runWith(registry, nil))
	if code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}

	if got := stdout.String(); !strings.Contains(got, "retrievers=[v4(ipv4)]") {
		t.Errorf("stdout = %q, want it to include the family hint", got)
	}
}

func TestCheckConfigExitsWithConfigCodeOnAnInvalidConfig(t *testing.T) {
	var stderr bytes.Buffer

	path := writeConfig(t, `
[provider.dns]
type = "nosuch"
[[instance]]
name = "x"
[[instance.retriever]]
ref = "echo"
[[instance.provider]]
ref = "dns"
`)

	code := Run(context.Background(), []string{"--config", path, "--check-config"}, &bytes.Buffer{}, &stderr, runWith(plugin.Default, nil))
	if code != ExitConfig {
		t.Errorf("exit code = %d, want %d", code, ExitConfig)
	}
	if !strings.Contains(stderr.String(), `instance "x"`) {
		t.Errorf("stderr = %q, want it to name the invalid instance", stderr.String())
	}
}

func TestMissingConfigFile(t *testing.T) {
	var stderr bytes.Buffer

	code := Run(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "absent.toml")}, &bytes.Buffer{}, &stderr, runWith(plugin.Default, nil))
	if code != ExitConfig {
		t.Errorf("exit code = %d, want %d", code, ExitConfig)
	}
}

func TestVersionFlag(t *testing.T) {
	var stdout bytes.Buffer

	if code := Run(context.Background(), []string{"--version"}, &stdout, &bytes.Buffer{}, runWith(plugin.Default, nil)); code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	if !strings.HasPrefix(stdout.String(), "dnspatch ") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestUnknownFlag(t *testing.T) {
	if code := Run(context.Background(), []string{"--nope"}, &bytes.Buffer{}, &bytes.Buffer{}, runWith(plugin.Default, nil)); code != ExitConfig {
		t.Errorf("exit code = %d, want %d", code, ExitConfig)
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

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.String()
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
		{name: "bad env", env: "loud", wantErr: EnvLogLevel},
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

	code := Run(context.Background(), []string{"--log-level", "loud"}, &bytes.Buffer{}, &stderr, runWith(plugin.Default, nil))
	if code != ExitConfig {
		t.Errorf("exit code = %d, want %d", code, ExitConfig)
	}
	if !strings.Contains(stderr.String(), "--log-level") {
		t.Errorf("stderr = %q, want it to name the flag", stderr.String())
	}
}

// --check-config shows which notifiers an instance publishes to, so that a
// name that reached the wrong instance is visible before the daemon runs.
func TestCheckConfigShowsTheNotifiersOfEachInstance(t *testing.T) {
	path, registry := notifyConfigWith(t, []string{"redis", "mqtt"}, `notify = ["mqtt"]`+"\n", "notify = []\n")

	opts := runWith(registry, nil)
	opts.Notify = func(config.Plugin, *slog.Logger) (NotifyConnection, error) {
		return &recordingConn{hook: &recordingHook{}}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"--config", path, "--check-config"}, &stdout, &stderr, opts); code != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, ExitOK, stderr.String())
	}

	lines := strings.Split(stdout.String(), "\n")
	if !strings.HasSuffix(lines[1], "providers=[p] notify=[mqtt(status)]") {
		t.Errorf("summary of a = %q, want it to end with the notifier it lists", lines[1])
	}
	if strings.Contains(lines[2], "notify") {
		t.Errorf("summary of b = %q, want no notifier: it lists none", lines[2])
	}
}
