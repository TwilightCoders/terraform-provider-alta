package device

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // matches the router's md5sum-based config hash
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// localRunner executes scripts with the host's sh, with stub commands first on PATH.
type localRunner struct{ path string }

func (r localRunner) Run(ctx context.Context, script string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+r.path)
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, &ScriptError{Err: err, Stderr: stderr.String()}
	}
	return out, nil
}

// router is a fake router rooted in a temp dir.
type router struct {
	t    *testing.T
	root string
	gate *Gate
}

func newRouter(t *testing.T, window, pushTimeout time.Duration, agentStart string) *router {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	stubs := map[string]string{
		"md5sum":   `md5 -q "$1" 2>/dev/null || command md5sum "$1"; true`,
		"ping":     `case "$*" in *198.51.100.99*) echo "100% packet loss"; exit 1;; esac`,
		"nslookup": `case "$1" in *good*) printf 'Server: x\nAddress 1: 1.2.3.4\n';; *) echo "No answer";; esac`,
	}
	must(t, os.MkdirAll(bin, 0o755))
	for name, body := range stubs {
		must(t, os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	}

	r := &router{t: t, root: root}
	r.writeConfig("original")
	must(t, os.WriteFile(r.path("agent"), nil, 0o644))

	layout := Layout{
		Dir:          r.path("tf"),
		ConfigFile:   r.path("config.json"),
		HashFile:     r.path("hash.txt"),
		AgentStart:   strings.ReplaceAll(agentStart, "$ROOT", root),
		AgentStop:    "rm -f " + shellQuote(r.path("agent")),
		AgentRunning: "test -f " + shellQuote(r.path("agent")),
		Apply:        "echo applied >> " + shellQuote(r.path("apply.log")),
		Log:          ":",
	}
	r.gate = NewGate(localRunner{path: bin + ":" + os.Getenv("PATH")}, Options{
		Layout:      layout,
		Window:      window,
		PushTimeout: pushTimeout,
		Probes: ProbeSpec{
			WANTarget: "1.1.1.1", DNSServer: "192.0.2.10", DNSName: "{nonce}.good.example",
			LANTargets: []string{"192.0.2.10", "198.51.100.99"},
		},
	})
	return r
}

func (r *router) path(name string) string { return filepath.Join(r.root, name) }

func (r *router) read(name string) string {
	data, _ := os.ReadFile(r.path(name))
	return strings.TrimSpace(string(data))
}

func (r *router) writeConfig(content string) {
	sum := md5.Sum([]byte(content)) //nolint:gosec // see import
	must(r.t, os.WriteFile(r.path("config.json"), []byte(content), 0o644))
	must(r.t, os.WriteFile(r.path("hash.txt"), []byte(hex.EncodeToString(sum[:])+"\n"), 0o644))
}

func (r *router) status() Status {
	s, err := r.gate.Status(context.Background())
	must(r.t, err)
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// pushAgent resumes the agent and, like the cloud, pushes a new config a moment later.
const pushAgent = `touch "$ROOT/agent"; (sleep 1; printf pushed > "$ROOT/config.json"; md5 -q "$ROOT/config.json" > "$ROOT/hash.txt" 2>/dev/null || md5sum "$ROOT/config.json" | cut -d' ' -f1 > "$ROOT/hash.txt") &`

const quietAgent = `touch "$ROOT/agent"`

func TestTransactionConfirms(t *testing.T) {
	r := newRouter(t, 30*time.Second, 10*time.Second, pushAgent)
	ctx := context.Background()

	if s := r.status(); s.State != StateClean || !s.InSync() || !s.AgentRunning || s.TimerArmed {
		t.Fatalf("initial status = %+v", s)
	}

	must(t, r.gate.Prepare(ctx, []byte(`{"id":"txn-1"}`)))
	s := r.status()
	if s.State != StateGated || s.AgentRunning || string(s.Journal) != `{"id":"txn-1"}` {
		t.Fatalf("after prepare = %+v", s)
	}
	if r.read("tf/snap/config.json") != "original" {
		t.Error("snapshot missing")
	}

	pushed, err := r.gate.Commit(ctx)
	must(t, err)
	if s := r.status(); !pushed || s.State != StateCommitted || !s.TimerArmed || !s.AgentRunning {
		t.Fatalf("after commit pushed=%v status=%+v", pushed, s)
	}

	must(t, r.gate.Confirm(ctx))
	if s := r.status(); s.State != StateConfirmed || s.TimerArmed || !s.InSync() || r.read("config.json") != "pushed" {
		t.Fatalf("after confirm = %+v config=%q", s, r.read("config.json"))
	}
}

func TestUnconfirmedTransactionRollsBackOnItsOwn(t *testing.T) {
	r := newRouter(t, 2*time.Second, 5*time.Second, pushAgent)
	ctx := context.Background()

	must(t, r.gate.Prepare(ctx, []byte(`{}`)))
	_, err := r.gate.Commit(ctx)
	must(t, err)

	deadline := time.Now().Add(10 * time.Second)
	for r.status().State != StateRolledBack {
		if time.Now().After(deadline) {
			t.Fatalf("timer never fired: %+v", r.status())
		}
		time.Sleep(200 * time.Millisecond)
	}
	waitTimerIdle(t, r)
	s := r.status()
	if r.read("config.json") != "original" || s.AgentRunning || !s.InSync() || r.read("apply.log") != "applied" {
		t.Fatalf("after rollback status=%+v config=%q apply=%q", s, r.read("config.json"), r.read("apply.log"))
	}
}

func TestImmediateRollbackStandsTheTimerDown(t *testing.T) {
	r := newRouter(t, 30*time.Second, 5*time.Second, pushAgent)
	ctx := context.Background()

	must(t, r.gate.Prepare(ctx, []byte(`{}`)))
	_, err := r.gate.Commit(ctx)
	must(t, err)
	must(t, r.gate.Rollback(ctx))

	if r.read("config.json") != "original" || r.status().State != StateRolledBack {
		t.Fatalf("status = %+v", r.status())
	}
	waitTimerIdle(t, r)

	must(t, r.gate.Release(ctx))
	if s := r.status(); s.State != StateReleased || !s.AgentRunning {
		t.Fatalf("after release = %+v", s)
	}
}

func TestNoPushIsReported(t *testing.T) {
	r := newRouter(t, 30*time.Second, 2*time.Second, quietAgent)
	ctx := context.Background()
	must(t, r.gate.Prepare(ctx, []byte(`{}`)))
	pushed, err := r.gate.Commit(ctx)
	must(t, err)
	if pushed {
		t.Fatal("pushed = true without a hash change")
	}
	must(t, r.gate.Confirm(ctx))
}

func TestGateRefusesOutOfOrderSteps(t *testing.T) {
	r := newRouter(t, 30*time.Second, 5*time.Second, pushAgent)
	ctx := context.Background()

	must(t, r.gate.Prepare(ctx, []byte(`{}`)))
	if err := r.gate.Confirm(ctx); err == nil {
		t.Error("confirm from gated: expected error")
	}
	_, err := r.gate.Commit(ctx)
	must(t, err)
	if _, err := r.gate.Commit(ctx); err == nil {
		t.Error("second commit: expected error")
	}
	if err := r.gate.Release(ctx); err == nil {
		t.Error("release while committed: expected error")
	}
	if err := r.gate.Prepare(ctx, []byte(`{}`)); err == nil {
		t.Error("prepare with armed timer: expected error")
	}
	must(t, r.gate.Confirm(ctx))
}

func TestProbe(t *testing.T) {
	r := newRouter(t, time.Minute, time.Second, quietAgent)
	report, err := r.gate.Probe(context.Background())
	must(t, err)

	got := map[string]bool{}
	for _, p := range report {
		got[p.Name] = p.OK
	}
	want := map[string]bool{"wan": true, "dns": true, "lan:192.0.2.10": true, "lan:198.51.100.99": false}
	for name, ok := range want {
		if v, present := got[name]; !present || v != ok {
			t.Errorf("probe %s = %v (present %v), want %v; report %+v", name, v, present, ok, report)
		}
	}
	if len(report.Failures()) != 1 || report.Failures()[0].Detail == "" {
		t.Errorf("failures = %+v", report.Failures())
	}
}

func TestProbeRegressionsIgnorePreexistingFailures(t *testing.T) {
	baseline := ProbeReport{{Name: "wan", OK: true}, {Name: "lan:x", OK: false}}
	now := ProbeReport{{Name: "wan", OK: false}, {Name: "lan:x", OK: false}, {Name: "dns", OK: false}}
	reg := now.Regressions(baseline)
	if len(reg) != 1 || reg[0].Name != "wan" {
		t.Fatalf("regressions = %+v", reg)
	}
}

func TestShellQuote(t *testing.T) {
	out, err := exec.Command("sh", "-c", "printf %s "+shellQuote(`it's "$HOME" \n`)).Output()
	must(t, err)
	if string(out) != `it's "$HOME" \n` {
		t.Fatalf("round trip = %q", out)
	}
}

func TestRoute10LayoutRendersEveryScript(t *testing.T) {
	g := NewGate(localRunner{}, Options{})
	for _, name := range []string{"status.sh", "prepare.sh", "commit.sh", "confirm.sh", "rollback-now.sh", "release.sh", "probe.sh"} {
		script := render(name, g.data(ProbeSpec{WANTarget: "1.1.1.1"}))
		if strings.Contains(script, "<no value>") {
			t.Errorf("%s renders missing values", name)
		}
		if err := exec.Command("sh", "-n", "-c", script).Run(); err != nil {
			t.Errorf("%s: syntax error: %v", name, err)
		}
	}
}

func waitTimerIdle(t *testing.T, r *router) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for r.status().TimerArmed {
		if time.Now().After(deadline) {
			t.Fatal("timer still armed")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestStatusReadsOnlyTheStateWord(t *testing.T) {
	r := newRouter(t, time.Minute, time.Second, quietAgent)
	must(t, os.MkdirAll(r.path("tf"), 0o755))
	must(t, os.WriteFile(r.path("tf/state"), []byte("confirmed 1789423421 t2\n"), 0o644))
	if s := r.status(); s.State != StateConfirmed || !s.State.Settled() {
		t.Fatalf("state = %q", s.State)
	}
}
