package device

import (
	"context"
	"os"
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
	cfg := SSHConfig{Host: host, HostKeyFingerprint: os.Getenv("ALTA_ROUTER_HOST_KEY"), AgentSocket: os.Getenv("SSH_AUTH_SOCK")}
	if file := os.Getenv("ALTA_ROUTER_KEY_FILE"); file != "" {
		key, err := os.ReadFile(file)
		must(t, err)
		cfg.PrivateKey = key
	}
	runner, err := NewSSHRunner(cfg)
	must(t, err)
	gate := NewGate(runner, Options{Probes: ProbeSpec{
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
