package rabbitmq

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/dnspatch/dnspatch/plugin"
)

func build(t *testing.T, params map[string]any) (plugin.Notifier, error) {
	t.Helper()

	return plugin.Default.BuildNotifier(Name, params)
}

func TestNotifierRequiresAnAddress(t *testing.T) {
	_, err := build(t, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "address") {
		t.Errorf("BuildNotifier(no address) error = %v, want it to name the missing address", err)
	}
}

func TestNotifierRejectsAMalformedAddress(t *testing.T) {
	_, err := build(t, map[string]any{"address": "http://localhost:5672/"})
	if err == nil || !strings.Contains(err.Error(), "address") {
		t.Errorf("BuildNotifier(http URL) error = %v, want an address error", err)
	}
}

func TestNotifierRejectsAnEmptyExchange(t *testing.T) {
	_, err := build(t, map[string]any{"address": "amqp://localhost:5672/", "exchange": ""})
	if err == nil || !strings.Contains(err.Error(), "exchange") {
		t.Errorf("BuildNotifier(empty exchange) error = %v, want an exchange error", err)
	}
}

func TestNotifierBuildsWithoutConnecting(t *testing.T) {
	// The connection is opened on the first Publish, so building must succeed
	// with nothing listening on the port.
	n, err := build(t, map[string]any{
		"address":      "amqp://guest:guest@127.0.0.1:1/",
		"exchange":     "custom",
		"topic_prefix": "custom.",
	})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}

	if err := n.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestPublishToAnUnreachableBrokerFails(t *testing.T) {
	n, err := build(t, map[string]any{"address": "amqp://guest:guest@127.0.0.1:1/"})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err = n.Publish(ctx, "home", []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "connect") {
		t.Errorf("Publish error = %v, want a connect error", err)
	}

	_ = n.Close()
}

func TestPublishAfterCloseFails(t *testing.T) {
	n, err := build(t, map[string]any{"address": "amqp://localhost:5672/"})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}

	_ = n.Close()

	if err := n.Publish(context.Background(), "home", nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("Publish after Close error = %v, want a closed error", err)
	}
}

// TestPublishReachesABoundQueue runs against a real broker, for example
// `docker run --rm -p 5672:5672 rabbitmq:4`, and is skipped without
// RABBITMQ_TEST_URL.
func TestPublishReachesABoundQueue(t *testing.T) {
	url := os.Getenv("RABBITMQ_TEST_URL")
	if url == "" {
		t.Skip("RABBITMQ_TEST_URL is not set")
	}

	const exchange = "dnspatch.test"

	n, err := build(t, map[string]any{"address": url, "exchange": exchange})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}
	defer func() { _ = n.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The first publish declares the exchange; its message has no queue to
	// land in, so it is dropped. The queue is bound afterwards.
	if err = n.Publish(ctx, "warmup", nil); err != nil {
		t.Fatalf("Publish (warmup): %v", err)
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("Channel: %v", err)
	}

	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatalf("QueueDeclare: %v", err)
	}

	if err = ch.QueueBind(q.Name, "dnspatch.events.#", exchange, false, nil); err != nil {
		t.Fatalf("QueueBind: %v", err)
	}

	if err = n.Publish(ctx, "home", []byte(`{"instance":"home"}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	msg, ok, err := ch.Get(q.Name, true)
	if err != nil || !ok {
		t.Fatalf("Get = ok %v, err %v, want a message", ok, err)
	}

	if msg.RoutingKey != "dnspatch.events.home" || string(msg.Body) != `{"instance":"home"}` {
		t.Errorf("got key %q body %q, want the published event", msg.RoutingKey, msg.Body)
	}
}
