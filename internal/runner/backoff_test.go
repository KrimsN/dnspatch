package runner

import (
	"testing"
	"time"
)

func TestBackoffDelay(t *testing.T) {
	b := newBackoff(time.Minute)
	lowest := func(int64) int64 { return 0 }
	highest := func(n int64) int64 { return n - 1 }

	tests := []struct {
		failures int
		ceil     time.Duration
	}{
		{1, time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{5, 16 * time.Minute},
		{6, 30 * time.Minute},
		{7, 30 * time.Minute},
		{1000, 30 * time.Minute},
	}
	for _, tt := range tests {
		if got := b.delay(tt.failures, lowest); got != tt.ceil/2 {
			t.Errorf("failures=%d lowest jitter: got %s, want %s", tt.failures, got, tt.ceil/2)
		}
		if got := b.delay(tt.failures, highest); got != tt.ceil {
			t.Errorf("failures=%d highest jitter: got %s, want %s", tt.failures, got, tt.ceil)
		}
	}
}

func TestBackoffCeilingFollowsLongInterval(t *testing.T) {
	b := newBackoff(time.Hour)
	if got := b.delay(10, func(n int64) int64 { return n - 1 }); got != time.Hour {
		t.Errorf("got %s, want an hour: the ceiling must not drop below the interval", got)
	}
}

func TestBackoffDoesNotOverflow(t *testing.T) {
	b := newBackoff(time.Duration(1 << 62))
	if got := b.delay(100, func(n int64) int64 { return n - 1 }); got <= 0 {
		t.Errorf("got %s, want a positive delay", got)
	}
}

func TestBackoffJitterSpread(t *testing.T) {
	b := newBackoff(time.Minute)
	rnd := func(n int64) int64 { return n / 3 }
	for failures := 1; failures <= 8; failures++ {
		d := b.delay(failures, rnd)
		ceil := min(time.Minute<<(failures-1), maxBackoff)
		if d < ceil/2 || d > ceil {
			t.Errorf("failures=%d: delay %s outside [%s, %s]", failures, d, ceil/2, ceil)
		}
	}
}
