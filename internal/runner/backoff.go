package runner

import "time"

// maxBackoff is the ceiling of the retry delay; it is raised to the polling
// interval when that is longer.
const maxBackoff = 30 * time.Minute

// backoff computes retry delays that grow exponentially with the number of
// consecutive failures.
type backoff struct {
	base time.Duration
	max  time.Duration
}

// newBackoff returns a backoff whose delay starts at the polling interval.
func newBackoff(interval time.Duration) backoff {
	return backoff{base: interval, max: max(maxBackoff, interval)}
}

// delay returns how long to wait after the given number of consecutive
// failures (at least one). The ceiling doubles with every failure up to max;
// the delay is half of it plus a random share of the other half, so that it
// keeps growing while instances that started together drift apart. rnd
// returns a uniformly distributed value in [0, n).
func (b backoff) delay(failures int, rnd func(n int64) int64) time.Duration {
	ceil := b.base
	for i := 1; i < failures && ceil < b.max; i++ {
		if ceil > b.max/2 {
			ceil = b.max
			break
		}
		ceil *= 2
	}
	ceil = min(ceil, b.max)
	half := ceil / 2
	return half + time.Duration(rnd(int64(ceil-half)+1))
}
