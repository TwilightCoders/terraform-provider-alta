package routerconfig

import "github.com/TwilightCoders/terraform-provider-alta/internal/cloud"

// Config is a router's configuration, section by section.
//
// A nil section is unmanaged: Apply leaves it untouched. A non-nil section, even an
// empty one, is managed and authoritative (see each type for what that means).
type Config struct {
	PortForwards     *[]PortForward
	FirewallRules    *[]FirewallRule
	VLANs            *[]VLAN
	StaticRoutes     *[]StaticRoute
	SwitchPorts      *[]SwitchPort
	DHCPReservations *[]DHCPReservation
}

// section binds one Config field to where it lives in a Document.
type section struct {
	read  func(*Document, *Config)
	apply func(*Document, Config) error
}

var sections = []section{
	{
		read: func(d *Document, c *Config) { c.PortForwards = ptr(portForwards.read(d.Site)) },
		apply: func(d *Document, c Config) error {
			return applyIf(c.PortForwards, func(v []PortForward) error { return portForwards.write(d.Site, v) })
		},
	},
	{
		read: func(d *Document, c *Config) { c.FirewallRules = ptr(firewallRules.read(d.Site)) },
		apply: func(d *Document, c Config) error {
			return applyIf(c.FirewallRules, func(v []FirewallRule) error { return firewallRules.write(d.Site, v) })
		},
	},
	{
		read: func(d *Document, c *Config) { c.VLANs = ptr(vlans.read(d.Site)) },
		apply: func(d *Document, c Config) error {
			return applyIf(c.VLANs, func(v []VLAN) error { return vlans.write(d.Site, v) })
		},
	},
	{
		read: func(d *Document, c *Config) { c.StaticRoutes = ptr(staticRoutes.read(d.Site)) },
		apply: func(d *Document, c Config) error {
			return applyIf(c.StaticRoutes, func(v []StaticRoute) error { return staticRoutes.write(d.Site, v) })
		},
	},
	{
		read: func(d *Document, c *Config) { c.SwitchPorts = ptr(readSwitchPorts(d.Device)) },
		apply: func(d *Document, c Config) error {
			return applyIf(c.SwitchPorts, func(v []SwitchPort) error { return writeSwitchPorts(d.Device, v) })
		},
	},
	{
		read: func(d *Document, c *Config) { c.DHCPReservations = ptr(readReservations(d.Clients)) },
		apply: func(d *Document, c Config) error {
			return applyIf(c.DHCPReservations, func(v []DHCPReservation) error { return writeReservations(d.Clients, v) })
		},
	},
}

// Read returns every section of the document's configuration.
func Read(doc *Document) Config {
	var c Config
	for _, s := range sections {
		s.read(doc, &c)
	}
	return c
}

// Apply overlays the managed sections of c onto doc.
func (c Config) Apply(doc *Document) error {
	for _, s := range sections {
		if err := s.apply(doc, c); err != nil {
			return err
		}
	}
	return nil
}

// Plan returns the cloud writes that make base match c, and their pre-images.
func (c Config) Plan(base *Document) (writes, preImages []cloud.Write, err error) {
	next := base.Clone()
	if err := c.Apply(next); err != nil {
		return nil, nil, err
	}
	writes = next.Writes(base)
	return writes, PreImages(base, writes), nil
}

func applyIf[T any](section *[]T, write func([]T) error) error {
	if section == nil {
		return nil
	}
	return write(*section)
}

func ptr[T any](v T) *T { return &v }
