// Package ping implements a runner.Hook that pings an external monitoring
// endpoint after every completed cycle of an instance: a dead man's switch
// compatible with Healthchecks.io and Uptime Kuma push, which alerts when the
// ping stops arriving, not only when dnspatch itself reports an error.
package ping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KrimsN/dnspatch/internal/config"
	"github.com/KrimsN/dnspatch/internal/runner"
)

// Timeout bounds a single ping request, so an unreachable or slow monitoring
// endpoint cannot stall an instance's next tick for long.
const Timeout = 10 * time.Second

// BuildHooks turns inst.PingURL, when set, into a hook that pings it on every
// completed cycle. It matches the app.HookBuilder signature.
func BuildHooks(inst config.Instance, log *slog.Logger) ([]runner.Hook, error) {
	if inst.PingURL == "" {
		return nil, nil
	}

	parsed, err := url.Parse(inst.PingURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("ping_url: %q is not a valid http:// or https:// URL", inst.PingURL)
	}

	return []runner.Hook{&hook{
		successURL: inst.PingURL,
		// The Healthchecks.io convention for reporting failure is the ping
		// URL with "/fail" appended. Uptime Kuma push has no such path: it
		// simply answers this request with a 404, and the regular success
		// pings still serve as its dead man's switch.
		failURL: strings.TrimSuffix(inst.PingURL, "/") + "/fail",
		host:    parsed.Host,
		client:  &http.Client{Timeout: Timeout},
		log:     log.With("instance", inst.Name, "hook", "ping"),
	}}, nil
}

// hook pings successURL after a successful cycle and failURL after a failed
// one.
type hook struct {
	successURL, failURL string
	// host is logged instead of the full URL, which commonly embeds the
	// service's secret token in its path.
	host   string
	client *http.Client
	log    *slog.Logger
}

func (h *hook) AfterCycle(ctx context.Context, ev runner.CycleEvent) {
	target, label := h.successURL, "success"
	if !ev.Success {
		target, label = h.failURL, "fail"
	}

	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	if err := h.ping(ctx, target); err != nil {
		h.log.Warn("monitoring ping failed", "host", h.host, "kind", label, "err", err)
	}
}

// ping sends the GET request and reports a failure without the request URL,
// which would otherwise leak the service's secret token into the log through
// a wrapped *url.Error or an HTTP status line.
func (h *hook) ping(ctx context.Context, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}

	resp, err := h.client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	return nil
}
