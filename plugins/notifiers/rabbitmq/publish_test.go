package rabbitmq

import (
	"context"
	"strings"
	"testing"
	"time"
)

func nextPublished(t *testing.T, b *fakeBroker) published {
	t.Helper()

	select {
	case p := <-b.received:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("the broker received nothing")

		return published{}
	}
}

func newPublisher(t *testing.T, b *fakeBroker, params map[string]any) interface {
	Publish(context.Context, string, []byte) error
	Close() error
} {
	t.Helper()

	params["address"] = b.url()

	n, err := build(t, params)
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}

	t.Cleanup(func() { _ = n.Close() })

	return n
}

func TestPublishDeliversTheEventToTheExchange(t *testing.T) {
	b := newFakeBroker(t)
	n := newPublisher(t, b, map[string]any{"exchange": "custom"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := n.Publish(ctx, "home", []byte(`{"instance":"home"}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if d := <-b.declares; d != (declared{name: "custom", kind: "topic", durable: true}) {
		t.Errorf("declared %+v, want a durable topic exchange named custom", d)
	}

	got := nextPublished(t, b)
	if got.exchange != "custom" || got.routingKey != "dnspatch.events.home" || string(got.body) != `{"instance":"home"}` {
		t.Errorf("got %+v, want the event routed by the instance topic", got)
	}
}

func TestPublishReusesTheConnection(t *testing.T) {
	b := newFakeBroker(t)
	n := newPublisher(t, b, map[string]any{})

	for range 2 {
		if err := n.Publish(context.Background(), "home", nil); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		nextPublished(t, b)
	}

	if got := b.connectCount(); got != 1 {
		t.Errorf("the broker saw %d connections, want the second publish to reuse the first", got)
	}
}

func TestPublishReconnectsAfterTheBrokerDropsTheConnection(t *testing.T) {
	b := newFakeBroker(t)
	n := newPublisher(t, b, map[string]any{})

	if err := n.Publish(context.Background(), "home", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	nextPublished(t, b)
	b.dropConnections()

	// The client notices the drop asynchronously: a publish racing it may
	// fail once, after which the notifier must connect again by itself.
	deadline := time.Now().Add(5 * time.Second)

	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := n.Publish(ctx, "home", nil)

		cancel()

		if err == nil {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("Publish never recovered after the connection was dropped: %v", err)
		}
	}

	if got := b.connectCount(); got < 2 {
		t.Errorf("the broker saw %d connections, want a new one after the drop", got)
	}
}

func TestPublishFailsWhenTheBrokerRejectsTheMessage(t *testing.T) {
	b := newFakeBroker(t)
	b.nack.Store(true)

	n := newPublisher(t, b, map[string]any{})

	if err := n.Publish(context.Background(), "home", nil); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Errorf("Publish error = %v, want a rejection", err)
	}
}

func TestPublishFailsWhenTheBrokerNeverConfirms(t *testing.T) {
	b := newFakeBroker(t)
	b.silent.Store(true)

	n := newPublisher(t, b, map[string]any{})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	if err := n.Publish(ctx, "home", nil); err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Errorf("Publish error = %v, want a confirmation error when nothing is acked", err)
	}

	// The failed publish dropped the connection, so the next one starts over.
	b.silent.Store(false)

	if err := n.Publish(context.Background(), "home", nil); err != nil {
		t.Errorf("Publish after the failure: %v", err)
	}
}

func TestPublishFailsWhenTheExchangeCannotBeDeclared(t *testing.T) {
	b := newFakeBroker(t)
	b.declareFails.Store(true)

	n := newPublisher(t, b, map[string]any{})

	if err := n.Publish(context.Background(), "home", nil); err == nil || !strings.Contains(err.Error(), "declare exchange") {
		t.Errorf("Publish error = %v, want a declare error", err)
	}
}

func TestPublishFailsWhenTheBrokerDropsTheHandshake(t *testing.T) {
	steps := map[string]method{
		"connect":                   connectionOpen,
		"open channel":              channelOpen,
		"enable publisher confirms": confirmSelect,
	}

	for want, at := range steps {
		b := newFakeBroker(t, at)
		n := newPublisher(t, b, map[string]any{})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := n.Publish(ctx, "home", nil)

		cancel()

		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("breaking at %q: Publish error = %v, want it to mention %q", want, err, want)
		}
	}
}

func TestCloseAfterTheConnectionWasDroppedSucceeds(t *testing.T) {
	b := newFakeBroker(t)
	n := newPublisher(t, b, map[string]any{})

	if err := n.Publish(context.Background(), "home", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	b.dropConnections()
	time.Sleep(200 * time.Millisecond)

	if err := n.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestCloseClosesAnOpenConnection(t *testing.T) {
	b := newFakeBroker(t)
	n := newPublisher(t, b, map[string]any{})

	if err := n.Publish(context.Background(), "home", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if err := n.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
