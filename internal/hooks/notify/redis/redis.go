// Package redis is a notify.Publisher backed by Redis Pub/Sub: PUBLISH on an
// instance's status change, no queue or persistence, so a client that is not
// subscribed at the moment misses it. That fits a notification channel: the
// next cycle re-announces the current status anyway, and health.Recorder (see
// internal/health) already covers "the daemon itself is stuck" independently
// of whether anyone is listening for notifications.
package redis

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"

	"github.com/KrimsN/dnspatch/internal/hooks/notify"
)

func init() {
	notify.Default.Register("redis", Factory)
}

// Factory builds a Publisher from the [[notify]] table's parameters: "address"
// is a redis:// or rediss:// URL as accepted by redis.ParseURL, carrying the
// host, an optional password and the database index.
func Factory(params map[string]any) (notify.Publisher, error) {
	address, _ := params["address"].(string)
	if address == "" {
		return nil, fmt.Errorf(`"address" is required and must be a redis:// URL`)
	}

	opts, err := goredis.ParseURL(address)
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
