package device

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestLiveStatusAndProbe reads gate state and runs probes on a real router. Both are
// read-only. Requires TF_ACC=1, ALTA_ROUTER_HOST, ALTA_ROUTER_HOST_KEY and either
// ALTA_ROUTER_KEY_FILE or an SSH agent; ALTA_ROUTER_DNS_SERVER and ALTA_ROUTER_DNS_NAME
// optionally enable the DNS probe.
func TestLiveStatusAndProbe(t *testing.T) {
	host := os.Getenv("ALTA_ROUTER_HOST")
	if os.Getenv("TF_ACC") == "" || host == "" {
		t.Skip("set TF_ACC=1 and ALTA_ROUTER_HOST to run")
	}
	gate := NewGate(liveRunner(t, host), Options{Probes: ProbeSpec{
		WANTarget: "1.1.1.1", DNSServer: os.Getenv("ALTA_ROUTER_DNS_SERVER"), DNSName: os.Getenv("ALTA_ROUTER_DNS_NAME"),
	}})

	status, err := gate.Status(context.Background())
	must(t, err)
	t.Logf("status: state=%s agent=%v timer=%v in_sync=%v", status.State, status.AgentRunning, status.TimerArmed, status.InSync())
	if status.ConfigMD5 == "" || status.AppliedHash == "" {
		t.Errorf("status incomplete: %+v", status)
	}

	report, err := gate.Probe(context.Background())
	must(t, err)
	for _, p := range report {
		t.Logf("probe %-8s ok=%v %s", p.Name, p.OK, p.Detail)
	}
	if len(report.Failures()) > 0 {
		t.Errorf("probe failures: %+v", report.Failures())
	}
}

// TestLiveHookScriptsRunOnTheRouter exercises the hook scripts and the generated boot
// loader against the real router's shell, in a scratch directory. It touches no real
// paths: hooks, hotplug target and post-cfg copy all live under /tmp.
func TestLiveHookScriptsRunOnTheRouter(t *testing.T) {
	host := os.Getenv("ALTA_ROUTER_HOST")
	if os.Getenv("TF_ACC") == "" || host == "" {
		t.Skip("set TF_ACC=1 and ALTA_ROUTER_HOST to run")
	}
	runner := liveRunner(t, host)
	root := "/tmp/alta-hook-check"
	if _, err := runner.Run(context.Background(), "rm -rf "+root+" && mkdir -p "+root+"/iface && printf '#!/bin/ash\\nexit 0\\n' > "+root+"/post-cfg.sh", nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = runner.Run(context.Background(), "rm -rf "+root, nil) })

	layout := Route10Layout()
	layout.HookDir, layout.HotplugDir, layout.PostCfg = root+"/tf.d", root+"/iface", root+"/post-cfg.sh"
	ext := NewExtensions(runner, layout)
	hook := Hook{Name: "check", Interface: "wg-does-not-exist", Script: "true\n", Requires: []string{"iptables", "flock", "setsid"}}

	state, err := ext.Put(context.Background(), hook)
	must(t, err)
	if !state.Present || !state.Installed || state.SHA256 == "" {
		t.Fatalf("state = %+v", state)
	}

	// The loader is what runs at boot and after every push; run it the same way.
	script := strings.Join([]string{"rm -f " + layout.HotplugDir + "/*", "sh " + layout.HookDir + "/loader.sh", "ls " + layout.HotplugDir}, "\n")
	out, err := runner.Run(context.Background(), script, nil)
	must(t, err)
	if !strings.Contains(string(out), hook.FileName()) {
		t.Errorf("the boot loader did not reinstall the hook: %q", out)
	}

	missing := hook
	missing.Requires = []string{"definitely-not-installed"}
	if _, err := ext.Put(context.Background(), missing); err == nil {
		t.Error("a missing binary must fail the install")
	}
	must(t, ext.Delete(context.Background(), hook, ""))
}

func liveRunner(t *testing.T, host string) Runner {
	t.Helper()
	cfg := SSHConfig{Host: host, HostKeyFingerprint: os.Getenv("ALTA_ROUTER_HOST_KEY"), AgentSocket: os.Getenv("SSH_AUTH_SOCK")}
	if file := os.Getenv("ALTA_ROUTER_KEY_FILE"); file != "" {
		key, err := os.ReadFile(file)
		must(t, err)
		cfg.PrivateKey = key
	}
	runner, err := NewSSHRunner(cfg)
	must(t, err)
	return runner
}

// TestLiveFileScriptsRunOnTheRouter exercises the file mechanism against the router's
// own shell, under /tmp. Real paths are untouched.
func TestLiveFileScriptsRunOnTheRouter(t *testing.T) {
	host := os.Getenv("ALTA_ROUTER_HOST")
	if os.Getenv("TF_ACC") == "" || host == "" {
		t.Skip("set TF_ACC=1 and ALTA_ROUTER_HOST to run")
	}
	runner := liveRunner(t, host)
	root := "/tmp/alta-file-check"
	_, err := runner.Run(context.Background(), "rm -rf "+root+" && mkdir -p "+root+" && printf 'original\n' > "+root+"/adopted.sh && chmod 644 "+root+"/adopted.sh", nil)
	must(t, err)
	t.Cleanup(func() { _, _ = runner.Run(context.Background(), "rm -rf "+root, nil) })

	ext := NewExtensions(runner, Route10Layout())
	f := File{Path: root + "/adopted.sh", Content: "#!/bin/sh\ntrue\n", Mode: "0755", Backup: true}

	state, err := ext.PutFile(context.Background(), f)
	must(t, err)
	if state.Mode != "0755" || state.SHA256 == "" {
		t.Fatalf("state = %+v", state)
	}
	got, err := ext.GetFile(context.Background(), f)
	must(t, err)
	if got.Content != f.Content || got.Mode != "0755" {
		t.Errorf("read back %+v", got)
	}
	backup, err := runner.Run(context.Background(), "cat "+f.Path+".bak-alta", nil)
	must(t, err)
	if strings.TrimSpace(string(backup)) != "original" {
		t.Errorf("backup = %q", backup)
	}
	must(t, ext.DeleteFile(context.Background(), f))
}
