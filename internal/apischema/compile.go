package apischema

import (
	"fmt"
	"strings"
)

// Compiled is a router's compiled configuration, as the cloud pushed it (/cfg/config.json).
// It is the third observation point: the cloud's own read-back cannot show whether a
// stored field ever reached the device, so a rule marked "defect" can only be confirmed,
// or found fixed, from here.
type Compiled map[string]any

// CheckCompile verifies the compile rules against a capture and the configuration the
// router is actually running. A rule that no longer behaves as recorded is reported,
// including a defect that appears to have been fixed, which is a prompt to update the
// schema rather than to keep working around it.
func (s *Schema) CheckCompile(c Capture, compiled Compiled) []error {
	var problems []error
	for _, rule := range s.Compile {
		check, ok := compileChecks[rule.Cloud]
		if !ok {
			continue
		}
		holds, detail := check(c, compiled)
		switch {
		case rule.Status == "defect" && holds:
			problems = append(problems, fmt.Errorf("compile rule %q is recorded as a defect but now behaves correctly (%s); update api/schema.json and drop the workaround",
				rule.Cloud, detail))
		case rule.Status != "defect" && !holds:
			problems = append(problems, fmt.Errorf("compile rule %q no longer holds: %s", rule.Cloud, detail))
		}
	}
	return problems
}

// compileChecks reports, per rule, whether the cloud's value reaches the device.
var compileChecks = map[string]func(Capture, Compiled) (bool, string){
	"site.vlans[].dnsServers": func(c Capture, compiled Compiled) (bool, string) {
		for _, vlan := range list(c.Site["vlans"]) {
			want := splitList(str(vlan["dnsServers"]))
			if len(want) == 0 {
				continue
			}
			iface := lanInterface(num(vlan["id"]))
			server, ok := dhcpServer(compiled, iface)
			if !ok {
				continue // a VLAN without DHCP has no scope to compare
			}
			if got := strList(server["dnsServers"]); !equal(got, want) {
				return false, fmt.Sprintf("%s serves %v, the site says %v", iface, got, want)
			}
		}
		return true, "every VLAN's DNS servers reached its scope"
	},

	"device.services.dhcpServers[].dnsServers": func(c Capture, compiled Compiled) (bool, string) {
		stored := strList(deviceLANServers(c.Device))
		if len(stored) == 0 {
			return false, "the device record carries no DHCP DNS servers to ignore"
		}
		vlanValue := splitList(vlanDNS(c.Site, 1))
		server, ok := dhcpServer(compiled, "lan")
		if !ok {
			return false, "the router has no untagged lan scope"
		}
		got := strList(server["dnsServers"])
		switch {
		case equal(got, stored) && !equal(stored, vlanValue):
			return true, fmt.Sprintf("the untagged lan now serves the device record's %v", stored)
		case equal(stored, vlanValue):
			return false, "the device record and VLAN 1 agree, so this cannot be told apart right now"
		default:
			return false, fmt.Sprintf("the untagged lan serves %v, not the device record's %v", got, stored)
		}
	},

	"device.jumboFrames": func(c Capture, compiled Compiled) (bool, string) {
		if jumbo, _ := c.Device["jumboFrames"].(bool); !jumbo {
			return false, "jumbo frames are off, so there is nothing to carry"
		}
		var dropped []string
		for _, iface := range list(compiled["interfaces"]) {
			_, hasMTU := iface["mtu"]
			if !hasMTU && (iface["eee"] != nil || iface["speed"] != nil) {
				dropped = append(dropped, str(iface["ifName"]))
			}
		}
		if len(dropped) == 0 {
			return true, "every port entry carries an MTU"
		}
		return false, fmt.Sprintf("port entries %s carry no MTU and win by being last", strings.Join(dropped, ", "))
	},

	"clients[].config.ip": func(c Capture, compiled Compiled) (bool, string) {
		reserved := map[string]bool{}
		for _, client := range c.Clients {
			cfg, _ := client["config"].(map[string]any)
			if ip := str(cfg["ip"]); ip != "" {
				reserved[ip] = false
			}
		}
		for _, server := range list(path(compiled, "services")["dhcpServers"]) {
			for _, mapping := range list(server["staticMappings"]) {
				if ip := str(mapping["ip"]); ip != "" {
					reserved[ip] = true
				}
			}
		}
		var missing []string
		for ip, found := range reserved {
			if !found {
				missing = append(missing, ip)
			}
		}
		if len(missing) > 0 {
			return false, fmt.Sprintf("%d reservation(s) never reached a DHCP scope", len(missing))
		}
		return true, fmt.Sprintf("all %d reservations reached a scope", len(reserved))
	},

	"site.firewall.nat.rules[]": func(c Capture, compiled Compiled) (bool, string) {
		want := map[string]bool{}
		for _, rule := range list(path(c.Site, "firewall", "nat")["rules"]) {
			want[str(rule["id"])] = false
		}
		for _, rule := range list(path(compiled, "nat")["rules"]) {
			want[str(rule["id"])] = true
		}
		for id, found := range want {
			if !found {
				return false, fmt.Sprintf("forward %q never reached the router", id)
			}
		}
		return true, fmt.Sprintf("all %d forwards reached the router", len(want))
	},
}

func lanInterface(id int64) string {
	if id <= 1 {
		return "lan"
	}
	return fmt.Sprintf("lan_%d", id)
}

func vlanDNS(site map[string]any, id int64) string {
	for _, vlan := range list(site["vlans"]) {
		if num(vlan["id"]) == id {
			return str(vlan["dnsServers"])
		}
	}
	return ""
}

func deviceLANServers(device map[string]any) any {
	for _, server := range list(path(device, "services")["dhcpServers"]) {
		if str(server["interface"]) == "lan" {
			return server["dnsServers"]
		}
	}
	return nil
}

func dhcpServer(compiled Compiled, iface string) (map[string]any, bool) {
	for _, server := range list(path(compiled, "services")["dhcpServers"]) {
		if str(server["interface"]) == iface {
			return server, true
		}
	}
	return nil, false
}

// Small readers for the untyped documents these checks walk.

func path(node map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		next, _ := node[key].(map[string]any)
		if next == nil {
			return map[string]any{}
		}
		node = next
	}
	return node
}

func list(v any) []map[string]any {
	items, _ := v.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if obj, ok := item.(map[string]any); ok {
			out = append(out, obj)
		}
	}
	return out
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func num(v any) int64 {
	var n int64
	if _, err := fmt.Sscan(str(v), &n); err != nil {
		return 0
	}
	return n
}

func strList(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, str(item))
	}
	return out
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
