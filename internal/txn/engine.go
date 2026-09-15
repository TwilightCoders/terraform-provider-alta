// Package txn runs a router change as one gated, commit-confirmed transaction:
//
//	PREPARE  guard, baseline probes, journal, pause the router's cloud agent
//	STAGE    apply every cloud write while the router cannot receive them
//	COMMIT   arm the router's rollback timer, resume the agent, await one push
//	CONFIRM  probes show no regression → disarm
//	ROLLBACK otherwise the router restores its snapshot
//	REPAIR   revert the cloud from the journal's pre-images, resume the agent
//
// The journal lives on the router, so any later run can finish an interrupted repair.
package txn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
	"github.com/TwilightCoders/terraform-provider-alta/internal/device"
)

// Cloud applies cloud writes.
type Cloud interface {
	Apply(ctx context.Context, w cloud.Write) error
}

// Gate is the router side of a transaction.
type Gate interface {
	Status(ctx context.Context) (device.Status, error)
	Prepare(ctx context.Context, journal []byte) error
	Commit(ctx context.Context) (pushed bool, err error)
	Confirm(ctx context.Context) error
	Rollback(ctx context.Context) error
	Release(ctx context.Context) error
	Probe(ctx context.Context) (device.ProbeReport, error)
}

// Change is a set of cloud writes and the pre-images that undo them.
type Change struct {
	Writes    []cloud.Write `json:"writes"`
	PreImages []cloud.Write `json:"pre_images"`
}

// Journal is what the router stores for a transaction.
type Journal struct {
	StartedAt time.Time `json:"started_at"`
	Change
}

// Errors callers can branch on.
var (
	// ErrLocalEdits means the router runs a configuration the cloud did not push; any
	// push would silently discard those edits.
	ErrLocalEdits = errors.New("the router carries local configuration edits the cloud does not know about")
	// ErrRecoveryNeeded means an earlier transaction did not finish.
	ErrRecoveryNeeded = errors.New("an earlier transaction did not finish")
	// ErrRolledBack means the change was applied, failed verification and was undone.
	ErrRolledBack = errors.New("the change failed verification and was rolled back")
	// ErrStranded means automatic recovery could not complete; the router is on its
	// known-good configuration with the cloud agent paused, and Recover must be retried.
	ErrStranded = errors.New("recovery could not complete; the router is safe on its previous configuration with its cloud agent paused")
)

// Engine runs transactions.
type Engine struct {
	cloud  Cloud
	gate   Gate
	settle time.Duration
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
}

// Options tunes an Engine.
type Options struct {
	// Settle is how long to wait after the push before probing, covering asynchronous
	// work the router does after applying (hotplug scripts, service restarts).
	Settle time.Duration
	Now    func() time.Time
	Sleep  func(context.Context, time.Duration) error
}

// New returns an Engine.
func New(c Cloud, g Gate, opts Options) *Engine {
	if opts.Settle == 0 {
		opts.Settle = 45 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepContext
	}
	return &Engine{cloud: c, gate: g, settle: opts.Settle, now: opts.Now, sleep: opts.Sleep}
}

// Result describes a confirmed transaction.
type Result struct {
	Pushed bool
	Probes device.ProbeReport
}

// Ready returns nil when a transaction may start.
func (e *Engine) Ready(ctx context.Context) error {
	status, err := e.gate.Status(ctx)
	if err != nil {
		return err
	}
	return readiness(status)
}

func readiness(s device.Status) error {
	switch {
	case !s.State.Settled() || s.TimerArmed:
		return fmt.Errorf("%w (router gate state %q)", ErrRecoveryNeeded, s.State)
	case !s.InSync():
		return fmt.Errorf("%w (config md5 %s, last push %s)", ErrLocalEdits, s.ConfigMD5, s.AppliedHash)
	case !s.AgentRunning:
		return fmt.Errorf("%w (the cloud agent is not running)", ErrRecoveryNeeded)
	}
	return nil
}

// Run applies change as one transaction. A change with no writes does nothing.
func (e *Engine) Run(ctx context.Context, change Change) (Result, error) {
	if len(change.Writes) == 0 {
		return Result{}, nil
	}
	if len(change.PreImages) != len(change.Writes) {
		return Result{}, errors.New("txn: every write needs a pre-image")
	}
	if err := e.Ready(ctx); err != nil {
		return Result{}, err
	}

	baseline, err := e.gate.Probe(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("baseline probes: %w", err)
	}
	journal, err := json.Marshal(Journal{StartedAt: e.now().UTC(), Change: change})
	if err != nil {
		return Result{}, err
	}
	if err := e.gate.Prepare(ctx, journal); err != nil {
		return Result{}, fmt.Errorf("pausing the router: %w", err)
	}

	for i, w := range change.Writes {
		if err := e.cloud.Apply(ctx, w); err != nil {
			stageErr := fmt.Errorf("staging %s: %w", describe(w), err)
			return Result{}, e.abortStaging(ctx, change.PreImages[:i], stageErr)
		}
	}

	pushed, err := e.gate.Commit(ctx)
	if err != nil {
		return Result{}, e.rollback(ctx, change, fmt.Errorf("resuming the router: %w", err))
	}
	if err := e.sleep(ctx, e.settle); err != nil {
		return Result{}, e.rollback(ctx, change, err)
	}

	report, err := e.gate.Probe(ctx)
	if err != nil {
		return Result{}, e.rollback(ctx, change, fmt.Errorf("verification probes: %w", err))
	}
	if regressions := report.Regressions(baseline); len(regressions) > 0 {
		return Result{}, e.rollback(ctx, change, fmt.Errorf("health regressed: %s", describeProbes(regressions)))
	}
	if err := e.gate.Confirm(ctx); err != nil {
		return Result{}, e.rollback(ctx, change, fmt.Errorf("confirming: %w", err))
	}
	return Result{Pushed: pushed, Probes: report}, nil
}

// abortStaging undoes the writes staged so far; the router never saw them.
func (e *Engine) abortStaging(ctx context.Context, staged []cloud.Write, cause error) error {
	ctx = context.WithoutCancel(ctx) // cleanup must finish even if the apply was interrupted
	if err := e.revert(ctx, staged); err != nil {
		return errors.Join(cause, fmt.Errorf("%w: %w", ErrStranded, err))
	}
	if err := e.gate.Release(ctx); err != nil {
		return errors.Join(cause, fmt.Errorf("%w: resuming the router: %w", ErrStranded, err))
	}
	return cause
}

// rollback restores the router, reverts the cloud and resumes the agent.
func (e *Engine) rollback(ctx context.Context, change Change, cause error) error {
	ctx = context.WithoutCancel(ctx) // cleanup must finish even if the apply was interrupted
	if err := e.restoreRouter(ctx); err != nil {
		// The router may be unreachable; its own timer will roll back. Nothing may be
		// released until that has happened, so stop here and let Recover finish.
		return errors.Join(cause, err)
	}
	if err := e.repair(ctx, change); err != nil {
		return errors.Join(cause, err)
	}
	return errors.Join(ErrRolledBack, cause)
}

// restoreRouter puts the router back on its snapshot if the change reached it.
func (e *Engine) restoreRouter(ctx context.Context) error {
	status, err := e.gate.Status(ctx)
	if err != nil {
		return fmt.Errorf("%w: reading router state: %w", ErrStranded, err)
	}
	switch status.State {
	case device.StateCommitted:
		if err := e.gate.Rollback(ctx); err != nil {
			return fmt.Errorf("%w: rolling back: %w", ErrStranded, err)
		}
		return nil
	case device.StateGated, device.StateRolledBack:
		return nil // the change never reached the router, or is already undone
	default:
		return fmt.Errorf("%w: unexpected router state %q", ErrStranded, status.State)
	}
}

func (e *Engine) repair(ctx context.Context, change Change) error {
	if err := e.revert(ctx, change.PreImages); err != nil {
		return fmt.Errorf("%w: %w", ErrStranded, err)
	}
	if err := e.gate.Release(ctx); err != nil {
		return fmt.Errorf("%w: resuming the router: %w", ErrStranded, err)
	}
	return nil
}

// Recover finishes a transaction interrupted at any point. It reports whether there was
// anything to recover.
func (e *Engine) Recover(ctx context.Context) (bool, error) {
	status, err := e.gate.Status(ctx)
	if err != nil {
		return false, err
	}
	if status.State.Settled() && !status.TimerArmed {
		return false, nil
	}

	var journal Journal
	if err := json.Unmarshal(status.Journal, &journal); err != nil {
		return false, fmt.Errorf("reading the router's transaction journal: %w", err)
	}
	// An unconfirmed change is presumed bad.
	if err := e.restoreRouter(ctx); err != nil {
		return false, err
	}
	// A gated transaction may have staged any prefix of its writes; restoring every
	// pre-image is idempotent.
	return true, e.repair(ctx, journal.Change)
}

// revert applies pre-images in reverse order.
func (e *Engine) revert(ctx context.Context, preImages []cloud.Write) error {
	for i := len(preImages) - 1; i >= 0; i-- {
		if err := e.cloud.Apply(ctx, preImages[i]); err != nil {
			return fmt.Errorf("reverting %s: %w", describe(preImages[i]), err)
		}
	}
	return nil
}

func describe(w cloud.Write) string {
	switch w.Kind {
	case cloud.KindClient:
		return "client " + w.ID
	default:
		return string(w.Kind) + " " + w.Key
	}
}

func describeProbes(results []device.ProbeResult) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		parts = append(parts, fmt.Sprintf("%s (%s)", r.Name, r.Detail))
	}
	return strings.Join(parts, ", ")
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
