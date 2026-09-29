package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/KrimsN/dnspatch/internal/runner"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// recordingPublisher records every (topic, Event) it was asked to publish.
type recordingPublisher struct {
	mu     sync.Mutex
	topics []string
	events []Event
	fail   error
}

func (p *recordingPublisher) Publish(_ context.Context, topic string, payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return p.fail
	}

	var ev Event
	if err := json.Unmarshal(payload, &ev); err != nil {
		return err
	}
	p.topics = append(p.topics, topic)
	p.events = append(p.events, ev)
	return nil
}

func (p *recordingPublisher) Close() error { return nil }

func (p *recordingPublisher) all() (topics []string, events []Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.topics...), append([]Event(nil), p.events...)
}

func TestHookDoesNotPublishOnAFirstSuccessfulCycle(t *testing.T) {
	pub := &recordingPublisher{}
	hook := NewHook(pub, discardLogger())

	hook.AfterCycle(context.Background(), runner.CycleEvent{Instance: "home", Success: true})

	if _, events := pub.all(); len(events) != 0 {
		t.Errorf("events = %+v, want none: a first cycle that succeeds is not news", events)
	}
}

func TestHookPublishesOnAFirstFailingCycle(t *testing.T) {
	pub := &recordingPublisher{}
	hook := NewHook(pub, discardLogger())
	boom := errors.New("boom")

	hook.AfterCycle(context.Background(), runner.CycleEvent{Instance: "home", Success: false, Err: boom})

	topics, events := pub.all()
	if len(events) != 1 {
		t.Fatalf("events = %+v, want exactly 1", events)
	}
	if topics[0] != "home" {
		t.Errorf("topic = %q, want %q", topics[0], "home")
	}
	if events[0].Instance != "home" || events[0].Success || events[0].Error != "boom" {
		t.Errorf("event = %+v, want instance=home success=false error=boom", events[0])
	}
}

func TestHookPublishesOnlyOnATransition(t *testing.T) {
	pub := &recordingPublisher{}
	hook := NewHook(pub, discardLogger())
	ctx := context.Background()

	hook.AfterCycle(ctx, runner.CycleEvent{Instance: "home", Success: true})  // first success: no event
	hook.AfterCycle(ctx, runner.CycleEvent{Instance: "home", Success: true})  // still fine: no event
	hook.AfterCycle(ctx, runner.CycleEvent{Instance: "home", Success: false}) // started failing: event
	hook.AfterCycle(ctx, runner.CycleEvent{Instance: "home", Success: false}) // still failing: no event
	hook.AfterCycle(ctx, runner.CycleEvent{Instance: "home", Success: true})  // recovered: event

	_, events := pub.all()
	if len(events) != 2 {
		t.Fatalf("events = %+v, want exactly 2 (fail, then recover)", events)
	}
	if events[0].Success || !events[1].Success {
		t.Errorf("events = %+v, want [fail, recover]", events)
	}
}

func TestHookTracksEachInstanceIndependently(t *testing.T) {
	pub := &recordingPublisher{}
	hook := NewHook(pub, discardLogger())
	ctx := context.Background()

	hook.AfterCycle(ctx, runner.CycleEvent{Instance: "a", Success: true})
	hook.AfterCycle(ctx, runner.CycleEvent{Instance: "b", Success: false}) // b's first failure: event

	_, events := pub.all()
	if len(events) != 1 || events[0].Instance != "b" {
		t.Errorf("events = %+v, want exactly one, for instance b", events)
	}
}

func TestHookLogsAPublishFailureAndDoesNotPanic(t *testing.T) {
	pub := &recordingPublisher{fail: errors.New("unreachable")}
	var buf syncBuffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	hook := NewHook(pub, log)

	hook.AfterCycle(context.Background(), runner.CycleEvent{Instance: "home", Success: false})

	if out := buf.String(); !strings.Contains(out, "could not publish notify event") {
		t.Errorf("log = %q, want it to report the publish failure", out)
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
