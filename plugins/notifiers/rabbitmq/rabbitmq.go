// Package rabbitmq is a notifier backed by RabbitMQ: every event is published
// to a topic exchange with the instance's topic as the routing key, so a
// consumer binds its own queue with the pattern it wants (for example
// "dnspatch.events.#"). Unlike Redis Pub/Sub, a queue that is bound keeps the
// events while its consumer is away.
//
// The package pulls in the amqp091-go client, which is why notifiers are left
// out of a plain build; see the notify_all and rabbitmq build tags.
package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/dnspatch/dnspatch/plugin"
)

// Name is the type name of the notifier in the configuration file.
const Name = "rabbitmq"

// dialTimeout bounds one connection attempt, so that an unreachable broker
// fails a publish instead of hanging the cycle that triggered it.
const dialTimeout = 10 * time.Second

func init() {
	plugin.RegisterNotifier(Name, newNotifier)
}

// Config holds the parameters of the rabbitmq notifier.
type Config struct {
	Address  string `toml:"address,required,secret" example:"${RABBITMQ_URL}" doc:"An amqp:// or amqps:// URL: it carries the host, the credentials and the virtual host"`
	Exchange string `toml:"exchange" default:"dnspatch" doc:"The durable topic exchange events are published to; it is declared on connect if it does not exist"`

	plugin.NotifierCommon
}

func newNotifier(cfg Config) (plugin.Notifier, error) {
	if _, err := amqp.ParseURI(cfg.Address); err != nil {
		return nil, fmt.Errorf("address: %w", err)
	}

	if cfg.Exchange == "" {
		return nil, errors.New("exchange: must not be empty")
	}

	return &publisher{address: cfg.Address, exchange: cfg.Exchange}, nil
}

// publisher connects on the first Publish, not when it is built, so that a
// broker that is down at startup does not stop the daemon, and reconnects
// after a publish fails.
type publisher struct {
	address  string
	exchange string

	mu     sync.Mutex
	conn   *amqp.Connection
	ch     *amqp.Channel
	closed bool
}

func (p *publisher) Publish(ctx context.Context, routingKey string, payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return errors.New("notifier is closed")
	}

	if p.ch == nil || p.ch.IsClosed() {
		if err := p.connect(); err != nil {
			return err
		}
	}

	confirmation, err := p.ch.PublishWithDeferredConfirmWithContext(ctx, p.exchange, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Body:         payload,
	})
	if err != nil {
		p.disconnect()

		return fmt.Errorf("publish: %w", err)
	}

	acked, err := confirmation.WaitContext(ctx)
	if err != nil {
		p.disconnect()

		return fmt.Errorf("await confirmation: %w", err)
	}

	if !acked {
		return errors.New("the broker rejected the message")
	}

	return nil
}

// connect opens the connection and a channel in confirm mode, and declares the
// exchange. It leaves nothing half-open on failure.
func (p *publisher) connect() error {
	p.disconnect()

	conn, err := amqp.DialConfig(p.address, amqp.Config{Dial: amqp.DefaultDial(dialTimeout)})
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()

		return fmt.Errorf("open channel: %w", err)
	}

	if err := ch.ExchangeDeclare(p.exchange, amqp.ExchangeTopic, true, false, false, false, nil); err != nil {
		_ = conn.Close()

		return fmt.Errorf("declare exchange %q: %w", p.exchange, err)
	}

	if err := ch.Confirm(false); err != nil {
		_ = conn.Close()

		return fmt.Errorf("enable publisher confirms: %w", err)
	}

	p.conn, p.ch = conn, ch

	return nil
}

func (p *publisher) disconnect() {
	if p.conn != nil {
		_ = p.conn.Close()
	}

	p.conn, p.ch = nil, nil
}

func (p *publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true

	if p.conn == nil || p.conn.IsClosed() {
		p.conn, p.ch = nil, nil

		return nil
	}

	err := p.conn.Close()
	p.conn, p.ch = nil, nil

	return err
}
