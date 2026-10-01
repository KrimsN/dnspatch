// Package redis is a notifier backed by Redis Pub/Sub: PUBLISH on an
// instance's status change, no queue or persistence, so a client that is not
// subscribed at the moment misses it. That fits a notification channel: the
// next cycle re-announces the current status anyway, and health.Recorder (see
// internal/health) already covers "the daemon itself is stuck" independently
// of whether anyone is listening for notifications.
//
// The package pulls in the go-redis client, which is why notifiers are left
// out of a plain build; see the notify_all and redis build tags.
package redis

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"

	"github.com/dnspatch/dnspatch/plugin"
)

// Name is the type name of the notifier in the configuration file.
const Name = "redis"

func init() {
	plugin.RegisterNotifier(Name, newNotifier)
}

// Config holds the parameters of the redis notifier.
type Config struct {
	Address string `toml:"address,required,secret" example:"${REDIS_URL}" doc:"A redis:// or rediss:// URL, as accepted by go-redis: it carries the host, an optional password and the database index"`

	plugin.NotifierCommon
}

func newNotifier(cfg Config) (plugin.Notifier, error) {
	opts, err := goredis.ParseURL(cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("address: %w", err)
	}

	return &publisher{client: goredis.NewClient(opts)}, nil
}

type publisher struct {
	client *goredis.Client
}

func (p *publisher) Publish(ctx context.Context, topic string, payload []byte) error {
	return p.client.Publish(ctx, topic, payload).Err()
}

func (p *publisher) Close() error {
	return p.client.Close()
}
