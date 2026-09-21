// Package runner orchestrates instances: each one runs in its own goroutine
// on its own interval and stops when the context is cancelled.
package runner

// TODO: the instance loop. Providers are visited independently, errors are
// collected with errors.Join, the last written address is tracked per
// provider and a failing provider is retried with exponential backoff.
// State is kept in memory only and does not survive a restart.
