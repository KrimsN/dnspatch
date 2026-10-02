package mqtt

import (
	"context"
	"strings"
	"testing"
	"time"
)

func publishOnce(ctx context.Context, t *testing.T, params map[string]any) error {
	t.Helper()

	n, err := build(t, params)
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}

	defer func() { _ = n.Close() }()

	return n.Publish(ctx, "home", []byte(`{"instance":"home"}`))
}

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

func TestPublishDeliversTheEventAtEveryQoS(t *testing.T) {
	for _, qos := range []int{0, 1, 2} {
		b := newFakeBroker(t)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		err := publishOnce(ctx, t, map[string]any{"address": b.url(), "qos": qos, "retain": true})

		cancel()

		if err != nil {
			t.Fatalf("Publish at QoS %d: %v", qos, err)
		}

		got := nextPublished(t, b)
		if got.topic != "dnspatch/events/home" || string(got.payload) != `{"instance":"home"}` {
			t.Errorf("QoS %d: got topic %q payload %q, want the event on a slash-separated topic", qos, got.topic, got.payload)
		}

		if int(got.qos) != qos || !got.retain {
			t.Errorf("QoS %d: got qos %d retain %v, want the configured ones", qos, got.qos, got.retain)
		}
	}
}

func TestPublishReusesTheConnection(t *testing.T) {
	b := newFakeBroker(t)

	n, err := build(t, map[string]any{"address": b.url()})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}

	defer func() { _ = n.Close() }()

	for range 2 {
		if err = n.Publish(context.Background(), "home", nil); err != nil {
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

	n, err := build(t, map[string]any{"address": b.url()})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}

	defer func() { _ = n.Close() }()

	if err = n.Publish(context.Background(), "home", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	nextPublished(t, b)
	b.dropConnections()

	// The client notices the drop asynchronously: a publish racing it may
	// fail once, after which the notifier must connect again by itself.
	deadline := time.Now().Add(5 * time.Second)

	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = n.Publish(ctx, "home", nil)

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

func TestPublishFailsWhenTheBrokerRefusesTheConnection(t *testing.T) {
	b := newFakeBroker(t)
	b.connackCode = 5 // not authorised

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := publishOnce(ctx, t, map[string]any{"address": b.url()})
	if err == nil || !strings.Contains(err.Error(), "connect") {
		t.Errorf("Publish error = %v, want a connect error", err)
	}
}

func TestPublishFailsWhenTheBrokerNeverAcknowledges(t *testing.T) {
	b := newFakeBroker(t)
	b.silent.Store(true)

	n, err := build(t, map[string]any{"address": b.url(), "qos": 1})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}

	defer func() { _ = n.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	if err = n.Publish(ctx, "home", nil); err == nil || !strings.Contains(err.Error(), "publish") {
		t.Errorf("Publish error = %v, want a publish error when no PUBACK arrives", err)
	}

	// The failed publish dropped the connection, so the next one starts over.
	b.silent.Store(false)

	if err = n.Publish(context.Background(), "home", nil); err != nil {
		t.Errorf("Publish after the failure: %v", err)
	}
}
