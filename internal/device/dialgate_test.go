package device

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testGate() *dialGate {
	g := newDialGate(1)
	g.sleep = func(context.Context, time.Duration) error { return nil }
	return g
}

// TestDialGateSerialisesConnections is the whole point of the gate: the router resets
// handshakes that arrive together, and a plan refreshing several resources makes that
// happen without anyone asking for concurrency.
func TestDialGateSerialisesConnections(t *testing.T) {
	g := testGate()
	var live, peak int32

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = g.do(context.Background(), func() ([]byte, error) {
				now := atomic.AddInt32(&live, 1)
				for {
					was := atomic.LoadInt32(&peak)
					if now <= was || atomic.CompareAndSwapInt32(&peak, was, now) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				atomic.AddInt32(&live, -1)
				return nil, nil
			})
		}()
	}
	wg.Wait()

	if peak > 1 {
		t.Errorf("%d connections ran at once; the router only tolerates one", peak)
	}
}

func TestDialGateRetriesARefusedConnection(t *testing.T) {
	g := testGate()
	calls := 0
	out, err := g.do(context.Background(), func() ([]byte, error) {
		calls++
		if calls < 3 {
			return nil, &DialError{Err: errors.New("connection reset by peer")}
		}
		return []byte("ok"), nil
	})
	if err != nil || string(out) != "ok" {
		t.Fatalf("do = %q, %v", out, err)
	}
	if calls != 3 {
		t.Errorf("attempted %d times, want 3", calls)
	}
}

// TestDialGateDoesNotRerunAScript draws the line the retry has to respect: once a script
// has run on the router, running it again is a second change, not a second attempt.
func TestDialGateDoesNotRerunAScript(t *testing.T) {
	g := testGate()
	calls := 0
	_, err := g.do(context.Background(), func() ([]byte, error) {
		calls++
		return nil, &ScriptError{Err: errors.New("exit status 1"), Stderr: "broken pipe"}
	})
	if err == nil {
		t.Fatal("want the script error")
	}
	if calls != 1 {
		t.Errorf("a failed script was re-run %d times", calls)
	}
}

func TestDialGateGivesUp(t *testing.T) {
	g := testGate()
	calls := 0
	_, err := g.do(context.Background(), func() ([]byte, error) {
		calls++
		return nil, &DialError{Err: errors.New("handshake failed")}
	})
	var refused *DialError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want the dial error to reach the caller", err)
	}
	if calls != g.attempts {
		t.Errorf("attempted %d times, want %d", calls, g.attempts)
	}
}

func TestDialGateHonoursCancellation(t *testing.T) {
	g := newDialGate(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.do(ctx, func() ([]byte, error) { return nil, errors.New("must not run") }); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
