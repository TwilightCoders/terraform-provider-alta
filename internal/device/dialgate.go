package device

import (
	"context"
	"errors"
	"time"
)

// dialGate serialises connections to the router and retries the ones it refuses.
//
// The router's SSH server resets handshakes that arrive together, and Terraform's default
// parallelism produces exactly that: several resources refreshing at once. Only a
// connection that never carried a script is retried, so nothing half-run is repeated.
type dialGate struct {
	slots    chan struct{}
	attempts int
	backoff  func(attempt int) time.Duration
	sleep    func(ctx context.Context, d time.Duration) error
}

func newDialGate(concurrent int) *dialGate {
	if concurrent < 1 {
		concurrent = 1
	}
	return &dialGate{
		slots:    make(chan struct{}, concurrent),
		attempts: 3,
		backoff:  func(attempt int) time.Duration { return time.Duration(attempt) * 250 * time.Millisecond },
		sleep:    sleepCtx,
	}
}

func (g *dialGate) do(ctx context.Context, fn func() ([]byte, error)) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case g.slots <- struct{}{}:
		defer func() { <-g.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	var err error
	for attempt := 1; attempt <= g.attempts; attempt++ {
		var out []byte
		out, err = fn()
		var refused *DialError
		if err == nil || !errors.As(err, &refused) || attempt == g.attempts {
			return out, err
		}
		if waitErr := g.sleep(ctx, g.backoff(attempt)); waitErr != nil {
			return nil, waitErr
		}
	}
	return nil, err
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
