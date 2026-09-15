package device

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// State is the gate's position in a transaction, persisted on the router.
type State string

// Gate states.
const (
	StateClean      State = "clean"
	StateGated      State = "gated"       // cloud agent paused, journal written
	StateCommitted  State = "committed"   // agent resumed, rollback timer armed
	StateConfirmed  State = "confirmed"   // transaction succeeded
	StateRolledBack State = "rolled-back" // snapshot restored; the cloud still holds the change
	StateReleased   State = "released"    // agent resumed after an aborted or repaired transaction
)

// Settled reports whether no transaction is in flight.
func (s State) Settled() bool {
	return s == StateClean || s == StateConfirmed || s == StateReleased
}

// Status is the router's configuration and gate state.
type Status struct {
	ConfigMD5    string
	AppliedHash  string
	State        State
	AgentRunning bool
	TimerArmed   bool
	// Journal is the opaque journal of the last transaction, if any.
	Journal []byte
}

// InSync reports whether the running configuration is exactly what the cloud last
// pushed, i.e. the router carries no local-only edits.
func (s Status) InSync() bool {
	return s.ConfigMD5 != "" && s.ConfigMD5 == s.AppliedHash
}

// Options tunes the gate.
type Options struct {
	Layout Layout
	// Window is how long the router waits for confirmation before rolling back.
	Window time.Duration
	// PushTimeout bounds the wait for the cloud's push after the agent resumes.
	PushTimeout time.Duration
	Probes      ProbeSpec
}

// Gate drives the commit-confirmed transaction on one router.
type Gate struct {
	run  Runner
	opts Options
}

// NewGate returns a Gate using runner, defaulting unset options for a Route10.
func NewGate(runner Runner, opts Options) *Gate {
	if opts.Layout == (Layout{}) {
		opts.Layout = Route10Layout()
	}
	if opts.Window == 0 {
		opts.Window = 5 * time.Minute
	}
	if opts.PushTimeout == 0 {
		opts.PushTimeout = 90 * time.Second
	}
	return &Gate{run: runner, opts: opts}
}

const journalMarker = "--journal--"

// Status reads the router's configuration and gate state.
func (g *Gate) Status(ctx context.Context) (Status, error) {
	out, err := g.exec(ctx, "status.sh", nil)
	if err != nil {
		return Status{}, err
	}
	head, journal, _ := bytes.Cut(out, []byte(journalMarker+"\n"))
	fields := parseKeyValues(head)
	return Status{
		ConfigMD5:    fields["config_md5"],
		AppliedHash:  fields["applied_hash"],
		State:        State(fields["state"]),
		AgentRunning: fields["agent"] == "running",
		TimerArmed:   fields["timer"] == "armed",
		Journal:      bytes.TrimSpace(journal),
	}, nil
}

// Prepare snapshots the configuration, stores journal, installs the rollback script
// and pauses the cloud agent.
func (g *Gate) Prepare(ctx context.Context, journal []byte) error {
	_, err := g.exec(ctx, "prepare.sh", journal)
	return err
}

// Commit arms the rollback timer and resumes the cloud agent, then waits for the push.
// pushed is false when the cloud had nothing new to send.
func (g *Gate) Commit(ctx context.Context) (pushed bool, err error) {
	out, err := g.exec(ctx, "commit.sh", nil)
	if err != nil {
		return false, err
	}
	return parseKeyValues(out)["pushed"] == "1", nil
}

// Confirm disarms the rollback timer, completing the transaction.
func (g *Gate) Confirm(ctx context.Context) error {
	_, err := g.exec(ctx, "confirm.sh", nil)
	return err
}

// Rollback restores the snapshot now instead of waiting for the timer. The cloud agent
// stays paused.
func (g *Gate) Rollback(ctx context.Context) error {
	_, err := g.exec(ctx, "rollback-now.sh", nil)
	return err
}

// Release resumes the cloud agent after an aborted or repaired transaction.
func (g *Gate) Release(ctx context.Context) error {
	_, err := g.exec(ctx, "release.sh", nil)
	return err
}

// Probe runs the health probes on the router.
func (g *Gate) Probe(ctx context.Context) (ProbeReport, error) {
	spec := g.opts.Probes
	spec.DNSName = strings.ReplaceAll(spec.DNSName, NoncePlaceholder, nonce())
	out, err := g.run.Run(ctx, render("probe.sh", g.data(spec)), nil)
	if err != nil {
		return nil, fmt.Errorf("probing router: %w", err)
	}
	return parseProbeReport(out), nil
}

func (g *Gate) exec(ctx context.Context, script string, stdin []byte) ([]byte, error) {
	out, err := g.run.Run(ctx, render(script, g.data(g.opts.Probes)), stdin)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.TrimSuffix(script, ".sh"), err)
	}
	return out, nil
}

func (g *Gate) data(probes ProbeSpec) scriptData {
	return scriptData{
		Layout:             g.opts.Layout,
		WindowSeconds:      int(g.opts.Window / time.Second),
		PushTimeoutSeconds: int(g.opts.PushTimeout / time.Second),
		ProbeSpec:          probes,
	}
}

func parseKeyValues(out []byte) map[string]string {
	fields := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		if k, v, ok := strings.Cut(scanner.Text(), "="); ok {
			fields[k] = v
		}
	}
	return fields
}

func nonce() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "tf" + hex.EncodeToString(b)
}
