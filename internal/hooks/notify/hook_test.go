package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dnspatch/dnspatch/internal/config"
	"github.com/dnspatch/dnspatch/internal/runner"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// publication is one call of Publish.
type publication struct {
	topic   string
	payload string
	ctx     context.Context
	// ctxErr is the error of ctx when Publish was called: the hook cancels the
	// context once it returns.
	ctxErr error
}

// recordingPublisher records every publication, or fails them all.
type recordingPublisher struct {
	mu   sync.Mutex
	pubs []publication
	fail error
}

func (p *recordingPublisher) Publish(ctx context.Context, topic string, payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.fail != nil {
		return p.fail
	}

	p.pubs = append(p.pubs, publication{topic: topic, payload: string(payload), ctx: ctx, ctxErr: ctx.Err()})

	return nil
}

func (p *recordingPublisher) Close() error { return nil }

func (p *recordingPublisher) all() []publication {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]publication(nil), p.pubs...)
}

var eventTime = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)

func event(kind runner.EventKind) runner.Event {
	return runner.Event{Kind: kind, Instance: "home", Time: eventTime}
}

var allEvents = []config.Event{
	config.EventStatus, config.EventProviderStatus, config.EventRetrieverStatus,
	config.EventIPChange, config.EventCycle, config.EventLifecycle,
}

// The payloads are a contract with whoever reads the broker, so they are
// compared as text.
func TestPayloads(t *testing.T) {
	boom := errors.New("boom")

	ipChange := event(runner.KindIPChange)
	ipChange.Changes = []runner.AddressChange{
		{Provider: "regru", Old: netip.MustParseAddr("1.2.3.4"), New: netip.MustParseAddr("5.6.7.8")},
		{Provider: "regru", IPv6: true, New: netip.MustParseAddr("2001:db8::1")},
	}

	started := event(runner.KindStarted)
	started.Version = "0.5.0"

	stopped := event(runner.KindStopped)
	stopped.Version = "0.5.0"

	failure := func(kind runner.EventKind, name string) runner.Event {
		ev := event(kind)
		ev.Failed, ev.Err, ev.Name = true, boom, name

		return ev
	}

	recovery := func(kind runner.EventKind, name string) runner.Event {
		ev := event(kind)
		ev.Name = name

		return ev
	}

	cycleFailed := event(runner.KindCycle)
	cycleFailed.Failed, cycleFailed.Err = true, boom

	tests := map[string]struct {
		ev   runner.Event
		want string
	}{
		"status failure": {
			failure(runner.KindInstanceStatus, ""),
			`{"event":"status","severity":"error","instance":"home","time":"2026-10-02T10:00:00Z","state":"failure","success":false,"error":"boom"}`,
		},
		"status recovery": {
			recovery(runner.KindInstanceStatus, ""),
			`{"event":"status","severity":"info","instance":"home","time":"2026-10-02T10:00:00Z","state":"recovery","success":true}`,
		},
		"provider failure": {
			failure(runner.KindProviderStatus, "regru"),
			`{"event":"provider_status","severity":"error","instance":"home","time":"2026-10-02T10:00:00Z","provider":"regru","state":"failure","success":false,"error":"boom"}`,
		},
		"provider recovery": {
			recovery(runner.KindProviderStatus, "regru"),
			`{"event":"provider_status","severity":"info","instance":"home","time":"2026-10-02T10:00:00Z","provider":"regru","state":"recovery","success":true}`,
		},
		"retriever failure": {
			failure(runner.KindRetrieverStatus, "ifconfig"),
			`{"event":"retriever_status","severity":"warning","instance":"home","time":"2026-10-02T10:00:00Z","retriever":"ifconfig","state":"failure","success":false,"error":"boom"}`,
		},
		"retriever recovery": {
			recovery(runner.KindRetrieverStatus, "ifconfig"),
			`{"event":"retriever_status","severity":"info","instance":"home","time":"2026-10-02T10:00:00Z","retriever":"ifconfig","state":"recovery","success":true}`,
		},
		"ip change": {
			ipChange,
			`{"event":"ip_change","severity":"info","instance":"home","time":"2026-10-02T10:00:00Z","changes":[` +
				`{"provider":"regru","family":"ipv4","old":"1.2.3.4","new":"5.6.7.8"},` +
				`{"provider":"regru","family":"ipv6","old":"","new":"2001:db8::1"}]}`,
		},
		"cycle success": {
			event(runner.KindCycle),
			`{"event":"cycle","severity":"info","instance":"home","time":"2026-10-02T10:00:00Z","success":true}`,
		},
		"cycle failure": {
			cycleFailed,
			`{"event":"cycle","severity":"error","instance":"home","time":"2026-10-02T10:00:00Z","success":false,"error":"boom"}`,
		},
		"started": {
			started,
			`{"event":"lifecycle","severity":"info","instance":"home","time":"2026-10-02T10:00:00Z","state":"started","version":"0.5.0"}`,
		},
		"stopped": {
			stopped,
			`{"event":"lifecycle","severity":"info","instance":"home","time":"2026-10-02T10:00:00Z","state":"stopped","version":"0.5.0"}`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			pub := &recordingPublisher{}
			newHook(pub, discardLogger(), allEvents).OnEvent(context.Background(), tc.ev)

			pubs := pub.all()
			if len(pubs) != 1 {
				t.Fatalf("published %d messages, want 1", len(pubs))
			}

			if pubs[0].topic != "home" {
				t.Errorf("topic = %q, want the instance name", pubs[0].topic)
			}

			if pubs[0].payload != tc.want {
				t.Errorf("payload =\n%s\nwant\n%s", pubs[0].payload, tc.want)
			}
		})
	}
}

// Before event types existed the payload was instance, success, error and
// time; a consumer written for it must still find them in a status event.
func TestStatusPayloadKeepsTheFieldsOfTheOldOne(t *testing.T) {
	pub := &recordingPublisher{}
	ev := event(runner.KindInstanceStatus)
	ev.Failed, ev.Err = true, errors.New("boom")

	newHook(pub, discardLogger(), []config.Event{config.EventStatus}).OnEvent(context.Background(), ev)

	got := pub.all()[0].payload
	for _, want := range []string{`"instance":"home"`, `"success":false`, `"error":"boom"`, `"time":"2026-10-02T10:00:00Z"`} {
		if !strings.Contains(got, want) {
			t.Errorf("payload %s lacks %s", got, want)
		}
	}
}

func TestHookPublishesOnlyTheEventsItWasGiven(t *testing.T) {
	kinds := map[config.Event]runner.EventKind{
		config.EventStatus:          runner.KindInstanceStatus,
		config.EventProviderStatus:  runner.KindProviderStatus,
		config.EventRetrieverStatus: runner.KindRetrieverStatus,
		config.EventIPChange:        runner.KindIPChange,
		config.EventCycle:           runner.KindCycle,
		config.EventLifecycle:       runner.KindStarted,
	}

	for _, want := range allEvents {
		t.Run(string(want), func(t *testing.T) {
			pub := &recordingPublisher{}
			hook := newHook(pub, discardLogger(), []config.Event{want})

			for _, kind := range kinds {
				hook.OnEvent(context.Background(), event(kind))
			}

			pubs := pub.all()
			if len(pubs) != 1 || !strings.Contains(pubs[0].payload, `"event":"`+string(want)+`"`) {
				t.Errorf("published %+v, want only %s", pubs, want)
			}
		})
	}
}

// Both lifecycle events are one type.
func TestLifecycleCoversStartAndStop(t *testing.T) {
	pub := &recordingPublisher{}
	hook := newHook(pub, discardLogger(), []config.Event{config.EventLifecycle})

	hook.OnEvent(context.Background(), event(runner.KindStarted))
	hook.OnEvent(context.Background(), event(runner.KindStopped))

	if got := len(pub.all()); got != 2 {
		t.Errorf("published %d messages, want 2", got)
	}
}

func TestHookIgnoresAnUnknownKind(t *testing.T) {
	pub := &recordingPublisher{}

	newHook(pub, discardLogger(), allEvents).OnEvent(context.Background(), runner.Event{Instance: "home"})

	if got := pub.all(); len(got) != 0 {
		t.Errorf("published %+v, want nothing", got)
	}
}

func TestPublicationIsBoundedByATimeout(t *testing.T) {
	pub := &recordingPublisher{}

	newHook(pub, discardLogger(), allEvents).OnEvent(context.Background(), event(runner.KindCycle))

	deadline, ok := pub.all()[0].ctx.Deadline()
	if !ok {
		t.Fatal("the publication has no deadline: a broker that does not answer would hold the instance")
	}

	if left := time.Until(deadline); left <= 0 || left > publishTimeout {
		t.Errorf("deadline in %s, want within %s", left, publishTimeout)
	}
}

// A publication that never returns is cut off by its deadline.
func TestPublicationTimesOutOnASilentBroker(t *testing.T) {
	hook := newHook(blockingPublisher{}, discardLogger(), allEvents)
	hook.timeout = 20 * time.Millisecond

	done := make(chan struct{})

	go func() {
		hook.OnEvent(context.Background(), event(runner.KindCycle))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("OnEvent did not return: the timeout is not applied")
	}
}

type blockingPublisher struct{}

func (blockingPublisher) Publish(ctx context.Context, _ string, _ []byte) error {
	<-ctx.Done()

	return ctx.Err()
}

func (blockingPublisher) Close() error { return nil }

func TestHookLogsAPublishFailureAndDoesNotPanic(t *testing.T) {
	pub := &recordingPublisher{fail: errors.New("unreachable")}

	var buf syncBuffer

	hook := newHook(pub, slog.New(slog.NewTextHandler(&buf, nil)), allEvents)
	hook.OnEvent(context.Background(), event(runner.KindCycle))

	if out := buf.String(); !strings.Contains(out, "could not publish notify event") || !strings.Contains(out, "unreachable") {
		t.Errorf("log = %q, want it to report the publish failure", out)
	}
}

// The event of a stop is published after the context is cancelled; the runner
// hands it one that is not, and the hook must not make it cancelled again.
func TestStoppedIsPublishedWithAContextThatIsAlive(t *testing.T) {
	pub := &recordingPublisher{}

	newHook(pub, discardLogger(), allEvents).OnEvent(context.WithoutCancel(cancelled()), event(runner.KindStopped))

	pubs := pub.all()
	if len(pubs) != 1 || pubs[0].ctxErr != nil {
		t.Errorf("publications = %+v, want one with a live context", pubs)
	}
}

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	return ctx
}

// The text of an error goes to the broker exactly as it goes to the log, and
// nothing besides it: no parameters of the plugin, no addresses of the broker.
func TestPayloadCarriesOnlyTheErrorText(t *testing.T) {
	pub := &recordingPublisher{}
	ev := event(runner.KindProviderStatus)
	ev.Failed, ev.Name, ev.Err = true, "regru", errors.New(`PUT https://api.example/zone?token=%5Bredacted%5D: 401`)

	newHook(pub, discardLogger(), allEvents).OnEvent(context.Background(), ev)

	got := pub.all()[0].payload
	if !strings.Contains(got, `token=%5Bredacted%5D`) || strings.Contains(got, "api_token") {
		t.Errorf("payload = %s, want the redacted error text and no parameters", got)
	}
}

func TestAfterCycleDoesNothing(t *testing.T) {
	pub := &recordingPublisher{}

	newHook(pub, discardLogger(), allEvents).AfterCycle(context.Background(), runner.CycleEvent{Instance: "home"})

	if got := pub.all(); len(got) != 0 {
		t.Errorf("published %+v, want nothing: events come through OnEvent", got)
	}
}

// syncBuffer is a minimal concurrency-safe io.Writer for slog in tests.
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)

	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return string(b.buf)
}
