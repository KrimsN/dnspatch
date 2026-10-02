package redis

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/dnspatch/dnspatch/plugin"
)

// TestPublishHonoursTheContextWhileTheServerIsSilent uses a server that
// accepts connections and never answers, like one that is reachable but stuck.
func TestPublishHonoursTheContextWhileTheServerIsSilent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			if _, acceptErr := ln.Accept(); acceptErr != nil {
				return
			}
		}
	}()

	n, err := plugin.Default.BuildNotifier(Name, map[string]any{"address": "redis://" + ln.Addr().String() + "/0"})
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}

	defer func() { _ = n.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = n.Publish(ctx, "home", nil)

	if err == nil {
		t.Error("Publish to a silent server succeeded, want an error")
	}

	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Publish took %v with a 200ms context, want it to give up with the context", took)
	}
}
