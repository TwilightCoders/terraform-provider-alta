package routerconfig

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
)

// SwitchPort is the VLAN membership of one physical port (device.portsCfg.ports).
//
// Ports are physical, so managing ports is not authoritative over the set: listed ports
// get exactly these fields, unlisted ports are left alone. Port-level settings this type
// does not model (speed, EEE, PoE, WAN role) are always preserved.
type SwitchPort struct {
	Port int64
	// NativeVLAN is the untagged VLAN; nil means the router's default (VLAN 1).
	NativeVLAN *int64
	// AllVLANs tags every VLAN on the port; otherwise TaggedVLANs lists them.
	AllVLANs    bool
	TaggedVLANs []int64
}

func readSwitchPorts(device cloud.Object) []SwitchPort {
	ports := portsObject(device, false)
	keys := make([]int64, 0, len(ports))
	for k := range ports {
		if n, err := strconv.ParseInt(k, 10, 64); err == nil {
			keys = append(keys, n)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	out := make([]SwitchPort, 0, len(keys))
	for _, n := range keys {
		p, _ := ports[strconv.FormatInt(n, 10)].(cloud.Object)
		allowed, _ := p["allowedVlans"].(cloud.Object)
		all, _ := allowed["all"].(bool)
		tagged, _ := asInts(allowed["list"])
		out = append(out, SwitchPort{Port: n, NativeVLAN: asOptionalInt(p["vlan"]), AllVLANs: all, TaggedVLANs: tagged})
	}
	return out
}

func writeSwitchPorts(device cloud.Object, desired []SwitchPort) error {
	if device == nil {
		return fmt.Errorf("switch ports belong to a device, so a device id is required")
	}
	seen := map[int64]bool{}
	for _, sp := range desired {
		if seen[sp.Port] {
			return fmt.Errorf("switch ports: port %d listed twice", sp.Port)
		}
		seen[sp.Port] = true
		if sp.AllVLANs && len(sp.TaggedVLANs) > 0 {
			return fmt.Errorf("switch ports: port %d sets both all VLANs and a tagged list", sp.Port)
		}

		port := child(portsObject(device, true), strconv.FormatInt(sp.Port, 10))
		putInt(port, "vlan", sp.NativeVLAN)

		allowed := child(port, "allowedVlans")
		putFlag(allowed, "all", sp.AllVLANs)
		putIntSet(allowed, "list", sp.TaggedVLANs)
		pruneEmpty(port, "allowedVlans")
	}
	return nil
}

func portsObject(device cloud.Object, create bool) cloud.Object {
	if !create {
		cfg, _ := device["portsCfg"].(cloud.Object)
		ports, _ := cfg["ports"].(cloud.Object)
		return ports
	}
	return child(child(device, "portsCfg"), "ports")
}
