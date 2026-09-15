package txn

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
	"github.com/TwilightCoders/terraform-provider-alta/internal/device"
)

var errBoom = errors.New("boom")

// fakeCloud records applied writes by their Value and fails on a chosen one.
type fakeCloud struct {
	applied []string
	failOn  string
}

func (c *fakeCloud) Apply(_ context.Context, w cloud.Write) error {
	v, _ := w.Value.(string)
	if v == c.failOn {
		return errBoom
	}
	c.applied = append(c.applied, v)
	return nil
}

// fakeGate is a state machine mirroring the router scripts, with injectable faults.
type fakeGate struct {
	status  device.Status
	calls   []string
	probes  []device.ProbeReport // successive probe results
	fail    map[string]error
	journal []byte
}

func newFakeGate() *fakeGate {
	return &fakeGate{
		status: device.Status{ConfigMD5: "h1", AppliedHash: "h1", State: device.StateClean, AgentRunning: true},
		fail:   map[string]error{},
	}
}

func (g *fakeGate) step(name string) error {
	g.calls = append(g.calls, name)
	return g.fail[name]
}

func (g *fakeGate) Status(context.Context) (device.Status, error) {
	if err := g.fail["status"]; err != nil {
		return device.Status{}, err
	}
	s := g.status
	s.Journal = g.journal
	return s, nil
}

func (g *fakeGate) Prepare(_ context.Context, journal []byte) error {
	if err := g.step("prepare"); err != nil {
		return err
	}
	g.journal, g.status.State, g.status.AgentRunning = journal, device.StateGated, false
	return nil
}

func (g *fakeGate) Commit(context.Context) (bool, error) {
	if err := g.step("commit"); err != nil {
		return false, err
	}
	g.status.State, g.status.AgentRunning, g.status.TimerArmed = device.StateCommitted, true, true
	return true, nil
}

func (g *fakeGate) Confirm(context.Context) error {
	if err := g.step("confirm"); err != nil {
		return err
	}
	g.status.State, g.status.TimerArmed = device.StateConfirmed, false
	return nil
}

func (g *fakeGate) Rollback(context.Context) error {
	if err := g.step("rollback"); err != nil {
		return err
	}
	g.status.State, g.status.AgentRunning, g.status.TimerArmed = device.StateRolledBack, false, false
	return nil
}

func (g *fakeGate) Release(context.Context) error {
	if err := g.step("release"); err != nil {
		return err
	}
	g.status.State, g.status.AgentRunning = device.StateReleased, true
	return nil
}

func (g *fakeGate) Probe(context.Context) (device.ProbeReport, error) {
	if err := g.step("probe"); err != nil {
		return nil, err
	}
	if len(g.probes) == 0 {
		return device.ProbeReport{{Name: "wan", OK: true}}, nil
	}
	r := g.probes[0]
	g.probes = g.probes[1:]
	return r, nil
}

func change() Change {
	return Change{
		Writes:    []cloud.Write{{Kind: cloud.KindSite, Key: "firewall", Value: "fw-new"}, {Kind: cloud.KindSite, Key: "vlans", Value: "vlans-new"}},
		PreImages: []cloud.Write{{Kind: cloud.KindSite, Key: "firewall", Value: "fw-old"}, {Kind: cloud.KindSite, Key: "vlans", Value: "vlans-old"}},
	}
}

func engine(c *fakeCloud, g *fakeGate) *Engine {
	return New(c, g, Options{
		Now:   func() time.Time { return time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC) },
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
}

func expectCalls(t *testing.T, g *fakeGate, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(g.calls, want) {
		t.Errorf("gate calls = %v, want %v", g.calls, want)
	}
}

func expectApplied(t *testing.T, c *fakeCloud, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(c.applied, want) {
		t.Errorf("cloud writes = %v, want %v", c.applied, want)
	}
}

func TestConfirmedTransaction(t *testing.T) {
	c, g := &fakeCloud{}, newFakeGate()
	res, err := engine(c, g).Run(context.Background(), change())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pushed || g.status.State != device.StateConfirmed {
		t.Fatalf("result %+v, state %s", res, g.status.State)
	}
	expectCalls(t, g, "probe", "prepare", "commit", "probe", "confirm")
	expectApplied(t, c, "fw-new", "vlans-new")

	var j Journal
	if err := json.Unmarshal(g.journal, &j); err != nil || len(j.PreImages) != 2 {
		t.Errorf("journal = %s (%v)", g.journal, err)
	}
}

func TestEmptyChangeDoesNothing(t *testing.T) {
	c, g := &fakeCloud{}, newFakeGate()
	if _, err := engine(c, g).Run(context.Background(), Change{}); err != nil {
		t.Fatal(err)
	}
	expectCalls(t, g)
}

func TestRefusesToStart(t *testing.T) {
	cases := map[string]struct {
		mutate func(*device.Status)
		want   error
	}{
		"local edits":   {func(s *device.Status) { s.ConfigMD5 = "edited" }, ErrLocalEdits},
		"unfinished":    {func(s *device.Status) { s.State = device.StateRolledBack }, ErrRecoveryNeeded},
		"timer armed":   {func(s *device.Status) { s.TimerArmed = true }, ErrRecoveryNeeded},
		"agent stopped": {func(s *device.Status) { s.AgentRunning = false }, ErrRecoveryNeeded},
	}
	for name, tc := range cases {
		c, g := &fakeCloud{}, newFakeGate()
		tc.mutate(&g.status)
		if _, err := engine(c, g).Run(context.Background(), change()); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
		if len(g.calls) != 0 || len(c.applied) != 0 {
			t.Errorf("%s: acted anyway: %v %v", name, g.calls, c.applied)
		}
	}
}

func TestStagingFailureRevertsWithoutTouchingTheRouter(t *testing.T) {
	c, g := &fakeCloud{failOn: "vlans-new"}, newFakeGate()
	_, err := engine(c, g).Run(context.Background(), change())
	if !errors.Is(err, errBoom) || errors.Is(err, ErrRolledBack) {
		t.Fatalf("err = %v", err)
	}
	expectApplied(t, c, "fw-new", "fw-old")
	expectCalls(t, g, "probe", "prepare", "release")
}

func TestRegressionRollsBackAndRepairs(t *testing.T) {
	c, g := &fakeCloud{}, newFakeGate()
	g.probes = []device.ProbeReport{
		{{Name: "wan", OK: true}, {Name: "dns", OK: true}},
		{{Name: "wan", OK: true}, {Name: "dns", OK: false, Detail: "SERVFAIL"}},
	}
	_, err := engine(c, g).Run(context.Background(), change())
	if !errors.Is(err, ErrRolledBack) || !strings.Contains(err.Error(), "dns (SERVFAIL)") {
		t.Fatalf("err = %v", err)
	}
	expectCalls(t, g, "probe", "prepare", "commit", "probe", "rollback", "release")
	expectApplied(t, c, "fw-new", "vlans-new", "vlans-old", "fw-old")
	if g.status.State != device.StateReleased || !g.status.AgentRunning {
		t.Errorf("final status = %+v", g.status)
	}
}

func TestPreexistingProbeFailureDoesNotBlock(t *testing.T) {
	c, g := &fakeCloud{}, newFakeGate()
	down := device.ProbeReport{{Name: "wan", OK: true}, {Name: "lan:printer", OK: false}}
	g.probes = []device.ProbeReport{down, down}
	if _, err := engine(c, g).Run(context.Background(), change()); err != nil {
		t.Fatal(err)
	}
}

func TestUnreachableRouterAfterCommitStrands(t *testing.T) {
	c, g := &fakeCloud{}, newFakeGate()
	g.fail["probe"] = nil
	e := engine(c, g)
	e.sleep = func(context.Context, time.Duration) error {
		g.fail["probe"], g.fail["status"] = errBoom, errBoom // the push took the path to the router down
		return nil
	}
	_, err := e.Run(context.Background(), change())
	if !errors.Is(err, ErrStranded) {
		t.Fatalf("err = %v", err)
	}
	expectApplied(t, c, "fw-new", "vlans-new") // cloud not reverted: the router must roll back first
}

func TestCommitFailureBeforeArmingOnlyRepairs(t *testing.T) {
	c, g := &fakeCloud{}, newFakeGate()
	g.fail["commit"] = errBoom
	_, err := engine(c, g).Run(context.Background(), change())
	if !errors.Is(err, ErrRolledBack) {
		t.Fatalf("err = %v", err)
	}
	expectCalls(t, g, "probe", "prepare", "commit", "release")
	expectApplied(t, c, "fw-new", "vlans-new", "vlans-old", "fw-old")
}

func TestRecoverFinishesAnInterruptedTransaction(t *testing.T) {
	for _, state := range []device.State{device.StateGated, device.StateCommitted, device.StateRolledBack} {
		c, g := &fakeCloud{}, newFakeGate()
		journal, _ := json.Marshal(Journal{Change: change()})
		g.journal, g.status.State, g.status.AgentRunning = journal, state, false

		recovered, err := engine(c, g).Recover(context.Background())
		if err != nil || !recovered {
			t.Fatalf("%s: recovered=%v err=%v", state, recovered, err)
		}
		expectApplied(t, c, "vlans-old", "fw-old")
		if g.status.State != device.StateReleased || !g.status.AgentRunning {
			t.Errorf("%s: final status %+v", state, g.status)
		}
		if state == device.StateCommitted && g.calls[0] != "rollback" {
			t.Errorf("%s: calls %v, want rollback first", state, g.calls)
		}
	}
}

func TestRecoverIsANoOpWhenSettled(t *testing.T) {
	c, g := &fakeCloud{}, newFakeGate()
	recovered, err := engine(c, g).Recover(context.Background())
	if err != nil || recovered {
		t.Fatalf("recovered=%v err=%v", recovered, err)
	}
}

func TestMismatchedPreImagesRejected(t *testing.T) {
	ch := change()
	ch.PreImages = ch.PreImages[:1]
	if _, err := engine(&fakeCloud{}, newFakeGate()).Run(context.Background(), ch); err == nil {
		t.Fatal("expected error")
	}
}

func TestPrepareFailureStopsBeforeStaging(t *testing.T) {
	c, g := &fakeCloud{}, newFakeGate()
	g.fail["prepare"] = errBoom
	if _, err := engine(c, g).Run(context.Background(), change()); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}
	expectApplied(t, c)
}

func TestConfirmFailureRollsBack(t *testing.T) {
	c, g := &fakeCloud{}, newFakeGate()
	g.fail["confirm"] = errBoom
	if _, err := engine(c, g).Run(context.Background(), change()); !errors.Is(err, ErrRolledBack) {
		t.Fatalf("err = %v", err)
	}
	expectCalls(t, g, "probe", "prepare", "commit", "probe", "confirm", "rollback", "release")
}

func TestFailedCloudRevertStrandsSafely(t *testing.T) {
	c, g := &fakeCloud{failOn: "fw-old"}, newFakeGate()
	g.fail["confirm"] = errBoom
	_, err := engine(c, g).Run(context.Background(), change())
	if !errors.Is(err, ErrStranded) {
		t.Fatalf("err = %v", err)
	}
	if g.status.State != device.StateRolledBack || g.status.AgentRunning {
		t.Errorf("router must stay rolled back with its agent paused: %+v", g.status)
	}
}

func TestRecoverRejectsCorruptJournal(t *testing.T) {
	g := newFakeGate()
	g.status.State, g.journal = device.StateGated, []byte("{not json")
	if _, err := engine(&fakeCloud{}, g).Recover(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestSleepContextHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if err := sleepContext(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
}
