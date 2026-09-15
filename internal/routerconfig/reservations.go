package routerconfig

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
)

// DHCPReservation pins a client to an address. The cloud stores it on the client record
// (config.ip) and compiles it into the matching VLAN's DHCP static mappings. A client's
// VLAN assignment (config.vlan) is a separate portal feature and is left untouched.
type DHCPReservation struct {
	MAC string
	IP  string
}

// clientEditFields are the fields the portal sends to /api/client/edit. Fields under
// config on the stored record are sent flat; the rest are top-level on the record.
var (
	clientConfigFields = []string{"type", "vlan", "ip", "dlRate", "ulRate", "ignoreHotspot", "icon", "upnp", "upnpPorts"}
	clientRecordFields = []string{"profile", "schedule", "filter"}
)

// ClientID converts a MAC address in any common notation to the cloud's client id.
func ClientID(mac string) (string, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		var compact net.HardwareAddr
		if compact, err = parseCompactMAC(mac); err != nil {
			return "", fmt.Errorf("invalid MAC address %q", mac)
		}
		hw = compact
	}
	return strings.ReplaceAll(hw.String(), ":", ""), nil
}

func parseCompactMAC(s string) (net.HardwareAddr, error) {
	if len(s) != 12 {
		return nil, fmt.Errorf("not a compact MAC")
	}
	var b strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(s[i : i+2])
	}
	return net.ParseMAC(b.String())
}

// formatMAC renders a client id as colon-separated lowercase MAC.
func formatMAC(id string) string {
	hw, err := parseCompactMAC(id)
	if err != nil {
		return id
	}
	return hw.String()
}

func readReservations(clients map[string]cloud.Object) []DHCPReservation {
	var out []DHCPReservation
	for id, c := range clients {
		cfg, _ := c["config"].(cloud.Object)
		if ip := asString(cfg["ip"]); ip != "" {
			out = append(out, DHCPReservation{MAC: formatMAC(id), IP: ip})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MAC < out[j].MAC })
	return out
}

// writeReservations is authoritative: clients not listed lose any reservation.
func writeReservations(clients map[string]cloud.Object, desired []DHCPReservation) error {
	want := map[string]DHCPReservation{}
	for _, r := range desired {
		id, err := ClientID(r.MAC)
		if err != nil {
			return fmt.Errorf("dhcp reservations: %w", err)
		}
		if _, dup := want[id]; dup {
			return fmt.Errorf("dhcp reservations: MAC %s listed twice", r.MAC)
		}
		want[id] = r
	}

	for id, c := range clients {
		if _, keep := want[id]; !keep {
			if cfg, ok := c["config"].(cloud.Object); ok {
				delete(cfg, "ip")
			}
		}
	}
	for id, r := range want {
		record, ok := clients[id]
		if !ok {
			record = cloud.Object{"id": id}
			clients[id] = record
		}
		cfg := child(record, "config")
		putString(cfg, "ip", r.IP)
	}
	return nil
}

// clientEdit is the flat body the portal posts to /api/client/edit for a client record.
// Unset fields are sent as null, as the portal does.
func clientEdit(record cloud.Object) cloud.Object {
	edit := cloud.Object{}
	cfg, _ := record["config"].(cloud.Object)
	for _, k := range clientConfigFields {
		edit[k] = cfg[k]
	}
	for _, k := range clientRecordFields {
		edit[k] = record[k]
	}
	wired, _ := record["wired"].(bool)
	edit["wired"] = wired
	return edit
}
