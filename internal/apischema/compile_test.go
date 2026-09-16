package apischema

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/TwilightCoders/terraform-provider-alta/internal/device"
)

// compiled parses a router configuration document for the checks.
func compiled(t *testing.T, doc string) Compiled {
	t.Helper()
	var out Compiled
	dec := json.NewDecoder(strings.NewReader(doc))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// capture and configuration where both recorded defects are present: the device's own
// DHCP DNS servers are ignored in favour of VLAN 1's, and a port entry carrying an
// interface override has no MTU despite jumbo frames being on.
func defectiveWorld(t *testing.T) (Capture, Compiled) {
	t.Helper()
	c := Capture{
		Site: map[string]any{"vlans": []any{
			map[string]any{"id": json.Number("1"), "dnsServers": "192.0.2.10"},
		}},
		Device: map[string]any{
			"jumboFrames": true,
			"services":    map[string]any{"dhcpServers": []any{map[string]any{"interface": "lan", "dnsServers": []any{"198.51.100.53"}}}},
		},
	}
	return c, compiled(t, `{
      "services": {"dhcpServers": [{"interface": "lan", "dnsServers": ["192.0.2.10"]}]},
      "interfaces": [{"ifName": "eth1", "mtu": 9216}, {"ifName": "eth1", "eee": "off"}]
    }`)
}

func TestCompileChecksConfirmRecordedDefects(t *testing.T) {
	s := load(t)
	c, conf := defectiveWorld(t)
	if problems := s.CheckCompile(c, conf); len(problems) != 0 {
		t.Fatalf("recorded defects should be confirmed silently, got %v", problems)
	}
}

func TestCompileChecksReportADefectThatWasFixed(t *testing.T) {
	s := load(t)
	c, conf := defectiveWorld(t)
	// Alta starts honouring the device record, and stops dropping the MTU.
	conf["services"].(map[string]any)["dhcpServers"].([]any)[0].(map[string]any)["dnsServers"] = []any{"198.51.100.53"}
	conf["interfaces"].([]any)[1].(map[string]any)["mtu"] = json.Number("9216")

	// The VLAN rule legitimately stops holding too: if the device record now wins, VLAN 1's
	// value is no longer what the untagged scope serves.
	fixed := 0
	for _, err := range s.CheckCompile(c, conf) {
		if strings.Contains(err.Error(), "recorded as a defect but now behaves correctly") {
			fixed++
			if !strings.Contains(err.Error(), "update api/schema.json") {
				t.Errorf("unhelpful message: %v", err)
			}
		}
	}
	if fixed != 2 {
		t.Errorf("expected both defects reported as fixed, got %d", fixed)
	}
}

func TestCompileChecksReportRulesThatStoppedHolding(t *testing.T) {
	s := load(t)
	c, conf := defectiveWorld(t)
	// A VLAN's DNS servers no longer reach its scope.
	conf["services"].(map[string]any)["dhcpServers"].([]any)[0].(map[string]any)["dnsServers"] = []any{"203.0.113.1"}

	var found bool
	for _, err := range s.CheckCompile(c, conf) {
		if strings.Contains(err.Error(), "site.vlans[].dnsServers") && strings.Contains(err.Error(), "no longer holds") {
			found = true
		}
	}
	if !found {
		t.Error("a broken ok rule must be reported")
	}
}

// TestLiveCompileMappingHolds reads the configuration the router is running and checks
// the compile mapping against it. Read-only, and the only check that can see whether a
// value the cloud stores ever reaches the device.
func TestLiveCompileMappingHolds(t *testing.T) {
	host, siteID, deviceID := os.Getenv("ALTA_ROUTER_HOST"), os.Getenv("ALTA_LABS_SITE_ID"), os.Getenv("ALTA_LABS_DEVICE_ID")
	if os.Getenv("TF_ACC") == "" || host == "" || siteID == "" || deviceID == "" {
		t.Skip("set TF_ACC=1, ALTA_ROUTER_HOST, ALTA_LABS_SITE_ID and ALTA_LABS_DEVICE_ID (plus credentials) to run")
	}
	cfg := device.SSHConfig{Host: host, HostKeyFingerprint: os.Getenv("ALTA_ROUTER_HOST_KEY"), AgentSocket: os.Getenv("SSH_AUTH_SOCK")}
	if file := os.Getenv("ALTA_ROUTER_KEY_FILE"); file != "" {
		key, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		cfg.PrivateKey = key
	}
	runner, err := device.NewSSHRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runner.Run(context.Background(), "cat /cfg/config.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var running Compiled
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(&running); err != nil {
		t.Fatal(err)
	}

	live := liveCapture(t, siteID, deviceID)
	for _, err := range load(t).CheckCompile(live, running) {
		t.Error(err)
	}
}
