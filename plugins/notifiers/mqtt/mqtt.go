// Package mqtt is a notifier backed by an MQTT broker: every event is published
// as a message on the instance's topic, so a subscriber picks the events it
// wants with a topic filter (for example "dnspatch/events/#").
//
// The notifier's topic prefix and instance name use dots like the other
// notifiers' channels and routing keys ("dnspatch.events.home"); MQTT separates
// topic levels with a slash and a wildcard must be a whole level, so the dots
// become slashes ("dnspatch/events/home").
//
// The package pulls in the Eclipse Paho client, which is why notifiers are left
// out of a plain build; see the notify_all and mqtt build tags.
package mqtt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/dnspatch/dnspatch/plugin"
)

// Name is the type name of the notifier in the configuration file.
const Name = "mqtt"

// connectTimeout bounds one connection attempt, so that an unreachable broker
// fails a publish instead of hanging the cycle that triggered it.
const connectTimeout = 10 * time.Second

// disconnectQuiesce is how long, in milliseconds, Close lets in-flight work
// finish before it drops the connection.
const disconnectQuiesce = 250

func init() {
	plugin.RegisterNotifier(Name, newNotifier)
}

// Config holds the parameters of the mqtt notifier.
type Config struct {
	Address  string `toml:"address,required,secret" example:"${MQTT_URL}" doc:"A mqtt://, mqtts://, tcp://, ssl://, ws:// or wss:// URL: it carries the host and an optional user name and password"`
	ClientID string `toml:"client_id" doc:"The client identifier the notifier connects with; by default a random one, so that several daemons never take each other's session over"`
	QoS      int    `toml:"qos" default:"1" doc:"The delivery guarantee: 0 at most once, 1 at least once, 2 exactly once"`
	Retain   bool   `toml:"retain" default:"false" doc:"Ask the broker to keep the last event of each topic and hand it to a subscriber that joins later"`

	plugin.NotifierCommon
}

func newNotifier(cfg Config) (plugin.Notifier, error) {
	if err := checkAddress(cfg.Address); err != nil {
		return nil, fmt.Errorf("address: %w", err)
	}

	if cfg.QoS < 0 || cfg.QoS > 2 {
		return nil, fmt.Errorf("qos: %d is not 0, 1 or 2", cfg.QoS)
	}

	id := cfg.ClientID
	if id == "" {
		var err error
		if id, err = randomClientID(); err != nil {
			return nil, err
		}
	}

	return &publisher{address: cfg.Address, clientID: id, qos: byte(cfg.QoS), retain: cfg.Retain}, nil
}

func checkAddress(address string) error {
	u, err := url.Parse(address)
	if err != nil {
		return err
	}

	switch u.Scheme {
	case "mqtt", "mqtts", "tcp", "ssl", "tls", "ws", "wss":
	default:
		return fmt.Errorf("unsupported scheme %q, want mqtt, mqtts, tcp, ssl, ws or wss", u.Scheme)
	}

	if u.Hostname() == "" {
		return errors.New("no host")
	}

	return nil
}

func randomClientID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("client_id: %w", err)
	}

	return "dnspatch-" + hex.EncodeToString(b), nil
}

// publisher connects on the first Publish, not when it is built, so that a
// broker that is down at startup does not stop the daemon, and reconnects
// after a publish fails.
type publisher struct {
	address  string
	clientID string
	qos      byte
	retain   bool

	mu     sync.Mutex
	client paho.Client
	closed bool
}

func (p *publisher) Publish(ctx context.Context, topic string, payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return errors.New("notifier is closed")
	}

	if p.client == nil || !p.client.IsConnectionOpen() {
		if err := p.connect(ctx); err != nil {
			return err
		}
	}

	token := p.client.Publish(strings.ReplaceAll(topic, ".", "/"), p.qos, p.retain, payload)
	if err := wait(ctx, token); err != nil {
		p.disconnect()

		return fmt.Errorf("publish: %w", err)
	}

	return nil
}

// connect opens a connection that is not retried or re-established by the
// client: Publish decides when to connect again. It leaves nothing half-open
// on failure.
func (p *publisher) connect(ctx context.Context) error {
	p.disconnect()

	opts := paho.NewClientOptions().
		AddBroker(p.address).
		SetClientID(p.clientID).
		SetConnectTimeout(connectTimeout).
		SetAutoReconnect(false).
		SetConnectRetry(false)

	client := paho.NewClient(opts)
	if err := wait(ctx, client.Connect()); err != nil {
		client.Disconnect(0)

		return fmt.Errorf("connect: %w", err)
	}

	p.client = client

	return nil
}

func (p *publisher) disconnect() {
	if p.client != nil {
		p.client.Disconnect(0)
	}

	p.client = nil
}

// wait returns when the token completes or ctx ends, whichever comes first.
func wait(ctx context.Context, token paho.Token) error {
	select {
	case <-token.Done():
		return token.Error()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true

	if p.client != nil {
		p.client.Disconnect(disconnectQuiesce)
		p.client = nil
	}

	return nil
}
