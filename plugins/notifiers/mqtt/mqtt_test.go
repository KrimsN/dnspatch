package mqtt

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

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
	for _, address := range []string{"http://localhost:1883/", "amqp://localhost:5672/", "mqtt://", "localhost:1883"} {
		_, err := build(t, map[string]any{"address": address})
		if err == nil || !strings.Contains(err.Error(), "address") {
			t.Errorf("BuildNotifier(%q) error = %v, want an address error", address, err)
		}
	}
}

func TestNotifierRejectsAnUnknownQoS(t *testing.T) {
	for _, qos := range []int{-1, 3} {
		_, err := build(t, map[string]any{"address": "mqtt://localhost:1883", "qos": qos})
		if err == nil || !strings.Contains(err.Error(), "qos") {
			t.Errorf("BuildNotifier(qos %d) error = %v, want a qos error", qos, err)
		}
	}
}

func TestNotifierBuildsWithoutConnecting(t *testing.T) {
	// The connection is opened on the first Publish, so building must succeed
	// with nothing listening on the port.
	n, err := build(t, map[string]any{
		"address":      "mqtt://user:secret@127.0.0.1:1",
		"client_id":    "custom",
		"qos":          2,
		"retain":       true,
		"topic_prefix": "custom.",
	})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}

	if err := n.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestClientIDIsRandomByDefault(t *testing.T) {
	a, err := randomClientID()
	if err != nil {
		t.Fatal(err)
	}

	b, err := randomClientID()
	if err != nil {
		t.Fatal(err)
	}

	if a == b || !strings.HasPrefix(a, "dnspatch-") {
		t.Errorf("randomClientID gave %q and %q, want two different ids with the dnspatch- prefix", a, b)
	}
}

func TestPublishToAnUnreachableBrokerFails(t *testing.T) {
	n, err := build(t, map[string]any{"address": "mqtt://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err = n.Publish(ctx, "home", []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "connect") {
		t.Errorf("Publish error = %v, want a connect error", err)
	}

	_ = n.Close()
}

func TestPublishStopsWhenTheContextEnds(t *testing.T) {
	n, err := build(t, map[string]any{"address": "mqtt://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err = n.Publish(ctx, "home", nil); err == nil {
		t.Error("Publish with a cancelled context succeeded, want an error")
	}

	_ = n.Close()
}

func TestPublishAfterCloseFails(t *testing.T) {
	n, err := build(t, map[string]any{"address": "mqtt://localhost:1883"})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}

	_ = n.Close()

	if err := n.Publish(context.Background(), "home", nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("Publish after Close error = %v, want a closed error", err)
	}
}

// TestPublishReachesASubscriber runs against a real broker, for example
// `docker run --rm -p 1883:1883 eclipse-mosquitto:2 mosquitto -c /mosquitto-no-auth.conf`,
// and is skipped without MQTT_TEST_URL.
func TestPublishReachesASubscriber(t *testing.T) {
	url := os.Getenv("MQTT_TEST_URL")
	if url == "" {
		t.Skip("MQTT_TEST_URL is not set")
	}

	got := make(chan paho.Message, 1)

	sub := paho.NewClient(paho.NewClientOptions().AddBroker(url).SetClientID("dnspatch-test-subscriber"))
	if token := sub.Connect(); !token.WaitTimeout(10*time.Second) || token.Error() != nil {
		t.Fatalf("subscriber connect: %v", token.Error())
	}
	defer sub.Disconnect(100)

	token := sub.Subscribe("dnspatch/events/#", 1, func(_ paho.Client, m paho.Message) { got <- m })
	if !token.WaitTimeout(10*time.Second) || token.Error() != nil {
		t.Fatalf("subscribe: %v", token.Error())
	}

	n, err := build(t, map[string]any{"address": url})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}
	defer func() { _ = n.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err = n.Publish(ctx, "home", []byte(`{"instance":"home"}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case m := <-got:
		if m.Topic() != "dnspatch/events/home" || string(m.Payload()) != `{"instance":"home"}` {
			t.Errorf("got topic %q payload %q, want the published event", m.Topic(), m.Payload())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the subscriber received nothing")
	}
}
