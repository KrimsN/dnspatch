package runner

import "time"

// Clock abstracts the time sources the runner depends on, so that tests can
// drive them by hand instead of sleeping.
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) Ticker
	AfterFunc(d time.Duration, f func()) Timer
}

// Ticker delivers ticks on the channel returned by C until stopped.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Timer is a pending call scheduled with Clock.AfterFunc.
type Timer interface {
	Stop() bool
}

// realClock is the Clock backed by the time package.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTicker(d time.Duration) Ticker {
	return realTicker{time.NewTicker(d)}
}

func (realClock) AfterFunc(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}

type realTicker struct{ *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.Ticker.C }
