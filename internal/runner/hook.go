package runner

import "context"

// Hook is notified after every completed cycle of an instance, whether it
// succeeded or failed. It is the extension point external monitoring (a ping
// to Healthchecks.io or Uptime Kuma, a notification) is built on; the core
// runner has no knowledge of what a hook does with the event.
//
// AfterCycle runs inline in the instance's own goroutine, so it delays the
// next tick for as long as it runs: an implementation must bound its own work
// with a timeout and must not block indefinitely. It must also do its own
// logging, since a hook reports no error back to the runner.
type Hook interface {
	AfterCycle(ctx context.Context, ev CycleEvent)
}

// CycleEvent describes one completed tick of an instance.
type CycleEvent struct {
	// Instance is the name of the instance the cycle belongs to.
	Instance string
	// Success is true when the cycle finished without error: every retriever
	// and provider that was tried succeeded (a provider skipped because the
	// address had not changed still counts as success).
	Success bool
	// Err is the joined error of the cycle, or nil when Success is true.
	Err error
}
