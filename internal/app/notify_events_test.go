package app

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dnspatch/dnspatch/internal/config"
	"github.com/dnspatch/dnspatch/internal/runner"
	"github.com/dnspatch/dnspatch/plugin"
)

// eventsConfig is a config with one notifier, "redis", that two instances
// publish to: a with the definition's events, b with its own override.
func eventsConfig(t *testing.T) (string, *plugin.Registry) {
	t.Helper()

	registry := plugin.NewRegistry()
	plugin.RegisterRetrieverIn(registry, "fake", func(fakeRetrieverConfig) (plugin.Retriever, error) {
		return &fakeRetriever{}, nil
	})
	plugin.RegisterProviderIn(registry, "fake", func(fakeRetrieverConfig) (plugin.Provider, error) {
		return fakeProviderStub{}, nil
	})

	path := writeConfig(t, `
[retriever.r]
type = "fake"
[provider.p]
type = "fake"

[notify.redis]
type   = "redis"
events = ["cycle", "ip_change"]

[[instance]]
name   = "a"
notify = ["redis"]
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "p"

[[instance]]
name = "b"
[[instance.notify]]
ref    = "redis"
events = ["status", "lifecycle"]
[[instance.retriever]]
ref = "r"
[[instance.provider]]
ref = "p"
`)

	return path, registry
}

// Two instances using one definition share its connection, built once and
// closed once, and each gets the events it asked for.
func TestInstancesShareOneConnectionWithTheirOwnEvents(t *testing.T) {
	path, registry := eventsConfig(t)

	hook := &recordingHook{}
	conn := &recordingConn{hook: hook}
	built := 0
	opts := runWith(registry, nil)
	opts.Notify = func(config.Plugin, *slog.Logger) (NotifyConnection, error) {
		built++
		return conn, nil
	}

	runNotifyDaemon(t, path, opts, func() bool { return hook.sawAll("a", "b") })

	if built != 1 {
		t.Errorf("connection built %d times, want 1", built)
	}

	want := [][]config.Event{
		{config.EventCycle, config.EventIPChange},
		{config.EventStatus, config.EventLifecycle},
	}
	if !slices.EqualFunc(conn.asked, want, slices.Equal) {
		t.Errorf("events asked = %v, want %v", conn.asked, want)
	}

	if conn.closed != 1 {
		t.Errorf("connection closed %d times, want 1", conn.closed)
	}
}

// orderedConn checks that the connection is not closed before the instances
// have reported that they stopped.
type orderedConn struct {
	mu            sync.Mutex
	stopped       map[string]bool
	cycles        map[string]bool
	closedStopped []string
	closed        bool
}

func (c *orderedConn) Hook([]config.Event) runner.Hook { return c }

func (c *orderedConn) AfterCycle(context.Context, runner.CycleEvent) {}

func (c *orderedConn) OnEvent(_ context.Context, ev runner.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	switch ev.Kind {
	case runner.KindStopped:
		c.stopped[ev.Instance] = true
	case runner.KindCycle:
		c.cycles[ev.Instance] = true
	}
}

func (c *orderedConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed = true
	for name := range c.stopped {
		c.closedStopped = append(c.closedStopped, name)
	}

	return nil
}

func (c *orderedConn) sawCycles(names ...string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, name := range names {
		if !c.cycles[name] {
			return false
		}
	}

	return true
}

func TestStoppedIsDeliveredBeforeTheConnectionCloses(t *testing.T) {
	path, registry := eventsConfig(t)

	conn := &orderedConn{stopped: map[string]bool{}, cycles: map[string]bool{}}
	opts := runWith(registry, nil)
	opts.Notify = func(config.Plugin, *slog.Logger) (NotifyConnection, error) { return conn, nil }

	runNotifyDaemon(t, path, opts, func() bool { return conn.sawCycles("a", "b") })

	slices.Sort(conn.closedStopped)

	if !slices.Equal(conn.closedStopped, []string{"a", "b"}) {
		t.Errorf("instances that had reported stopped when the connection closed = %v, want a and b", conn.closedStopped)
	}
}

func TestCheckConfigShowsTheEventsOfEachNotifier(t *testing.T) {
	path, registry := eventsConfig(t)

	opts := runWith(registry, nil)
	opts.Notify = func(config.Plugin, *slog.Logger) (NotifyConnection, error) {
		return &recordingConn{hook: &recordingHook{}}, nil
	}

	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"--config", path, "--check-config"}, &stdout, &stderr, opts); code != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, ExitOK, stderr.String())
	}

	lines := strings.Split(stdout.String(), "\n")
	if !strings.HasSuffix(lines[1], "notify=[redis(cycle, ip_change)]") {
		t.Errorf("summary of a = %q, want the events of the definition", lines[1])
	}
	if !strings.HasSuffix(lines[2], "notify=[redis(status, lifecycle)]") {
		t.Errorf("summary of b = %q, want its override", lines[2])
	}
}
