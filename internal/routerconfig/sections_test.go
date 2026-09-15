package routerconfig

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/TwilightCoders/terraform-provider-alta-labs/internal/cloud"
)

func TestPutPortKeepsEquivalentRepresentation(t *testing.T) {
	o := cloud.Object{"a": json.Number("8100"), "b": "8100"}
	putPort(o, "a", "8100")
	putPort(o, "b", "8100")
	putPort(o, "c", "443")
	putPort(o, "d", "50000-50100")
	putPort(o, "e", "")
	want := cloud.Object{"a": json.Number("8100"), "b": "8100", "c": json.Number("443"), "d": "50000-50100"}
	if !reflect.DeepEqual(o, want) {
		t.Errorf("got %v, want %v", o, want)
	}
}

func TestVLANDHCPAndFlags(t *testing.T) {
	doc := &Document{Site: cloud.Object{"vlans": []any{
		cloud.Object{"id": json.Number("20"), "notes": "Isolated", "disableDHCP": true, "mdns": false},
	}}}
	got := (*Read(doc).VLANs)[0]
	if got.DHCP || got.MDNS == nil || *got.MDNS {
		t.Fatalf("decoded %+v", got)
	}

	got.DHCP = true
	got.DNSServers = []string{"192.0.2.10", "192.0.2.1"}
	if err := (Config{VLANs: &[]VLAN{got}}).Apply(doc); err != nil {
		t.Fatal(err)
	}
	el := doc.Site["vlans"].([]any)[0].(cloud.Object)
	if _, ok := el["disableDHCP"]; ok || el["dnsServers"] != "192.0.2.10,192.0.2.1" || el["mdns"] != false {
		t.Errorf("encoded %v", el)
	}
}

func TestSwitchPortsPreservePortSettings(t *testing.T) {
	doc := &Document{Device: cloud.Object{"portsCfg": cloud.Object{"ports": cloud.Object{
		"3": cloud.Object{"eee": "off", "allowedVlans": cloud.Object{"list": []any{json.Number("2")}}},
		"5": cloud.Object{"wan": "default1", "vlan": json.Number("2")},
	}}}}
	ports := []SwitchPort{{Port: 3, TaggedVLANs: []int64{2, 3}}}
	if err := (Config{SwitchPorts: &ports}).Apply(doc); err != nil {
		t.Fatal(err)
	}
	all := doc.Device["portsCfg"].(cloud.Object)["ports"].(cloud.Object)
	p3 := all["3"].(cloud.Object)
	if p3["eee"] != "off" || !reflect.DeepEqual(p3["allowedVlans"], cloud.Object{"list": []any{json.Number("2"), json.Number("3")}}) {
		t.Errorf("port 3 = %v", p3)
	}
	if all["5"].(cloud.Object)["wan"] != "default1" {
		t.Error("unlisted port 5 was modified")
	}

	read := *Read(doc).SwitchPorts
	if len(read) != 2 || read[1].Port != 5 || *read[1].NativeVLAN != 2 {
		t.Errorf("read = %+v", read)
	}
}

func TestSwitchPortValidation(t *testing.T) {
	for name, ports := range map[string][]SwitchPort{
		"duplicate": {{Port: 1}, {Port: 1}},
		"both":      {{Port: 1, AllVLANs: true, TaggedVLANs: []int64{2}}},
	} {
		if err := (Config{SwitchPorts: &ports}).Apply(&Document{Device: cloud.Object{}}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestReservationsAreAuthoritative(t *testing.T) {
	doc := &Document{SiteID: "s", Clients: map[string]cloud.Object{
		"020000000018": {"id": "020000000018", "wired": true, "config": cloud.Object{"ip": "192.0.2.20", "icon": "airplay"}},
		"02000000001b": {"id": "02000000001b", "config": cloud.Object{"ip": "192.0.2.10", "vlan": json.Number("2")}},
	}}
	base := doc.Clone()
	desired := []DHCPReservation{
		{MAC: "02:00:00:00:00:1B", IP: "192.0.2.10"},
		{MAC: "aa-bb-cc-dd-ee-ff", IP: "203.0.113.50"},
	}
	writes, pre, err := (Config{DHCPReservations: &desired}).Plan(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := writeKeys(writes); !reflect.DeepEqual(got, []string{"client.020000000018", "client.aabbccddeeff"}) {
		t.Fatalf("writes = %v", got)
	}
	cleared := writes[0].Value.(cloud.Object)
	if cleared["ip"] != nil || cleared["icon"] != "airplay" || cleared["wired"] != true {
		t.Errorf("clearing write = %v", cleared)
	}
	if created := writes[1].Value.(cloud.Object); created["ip"] != "203.0.113.50" || created["wired"] != false {
		t.Errorf("creating write = %v", created)
	}
	if pre[0].Value.(cloud.Object)["ip"] != "192.0.2.20" {
		t.Errorf("pre-image = %v", pre[0].Value)
	}
}

func TestReservationValidation(t *testing.T) {
	for name, rs := range map[string][]DHCPReservation{
		"bad mac":   {{MAC: "nope", IP: "1.2.3.4"}},
		"duplicate": {{MAC: "aabbccddeeff", IP: "1.2.3.4"}, {MAC: "AA:BB:CC:DD:EE:FF", IP: "1.2.3.5"}},
	} {
		if err := (Config{DHCPReservations: &rs}).Apply(&Document{Clients: map[string]cloud.Object{}}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestClientID(t *testing.T) {
	for _, in := range []string{"02:00:00:00:00:1b", "02-00-00-00-00-1B", "02000000001b", "0200.0000.001b"} {
		if id, err := ClientID(in); err != nil || id != "02000000001b" {
			t.Errorf("ClientID(%q) = %q, %v", in, id, err)
		}
	}
}

func TestNewDocumentRequiresDevice(t *testing.T) {
	if _, err := NewDocument("s", "missing", cloud.Object{}, cloud.State{}); err == nil {
		t.Fatal("expected error for unknown device")
	}
}
