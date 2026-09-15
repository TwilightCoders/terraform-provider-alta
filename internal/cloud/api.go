package cloud

import (
	"context"
	"fmt"
	"net/url"
)

// State is GET /api/site/state: the live devices and clients of a site.
type State struct {
	Devices []Object `json:"devices"`
	Clients []Object `json:"clients"`
}

// Device returns the device with the given id (its MAC without separators).
func (s State) Device(id string) (Object, bool) {
	return findByID(s.Devices, id)
}

// Client returns the client record with the given id (its MAC without separators).
func (s State) Client(id string) (Object, bool) {
	return findByID(s.Clients, id)
}

func findByID(objects []Object, id string) (Object, bool) {
	for _, o := range objects {
		if o["id"] == id {
			return o, true
		}
	}
	return nil, false
}

// AuditEntry is one row of a site's audit trail.
type AuditEntry struct {
	ID        string `json:"id"`
	Timestamp string `json:"ts"`
	Action    string `json:"action"`
	Type      string `json:"type"`
	Data      Object `json:"data"`
}

// Author returns the portal user who made the change, empty for system changes.
func (e AuditEntry) Author() string {
	name, _ := e.Data["username"].(string)
	return name
}

// Site reads a site document (firewall, vlans, routes, …).
func (c *Client) Site(ctx context.Context, siteID string) (Object, error) {
	var site Object
	if err := c.get(ctx, "/api/site", url.Values{"id": {siteID}}, &site); err != nil {
		return nil, err
	}
	return site, nil
}

// State reads a site's devices and clients.
func (c *Client) State(ctx context.Context, siteID string) (State, error) {
	var state State
	err := c.get(ctx, "/api/site/state", url.Values{"id": {siteID}}, &state)
	return state, err
}

// Audit reads a site's audit trail, newest first as the API returns it.
func (c *Client) Audit(ctx context.Context, siteID string) ([]AuditEntry, error) {
	var resp struct {
		Trail []AuditEntry `json:"trail"`
	}
	err := c.get(ctx, "/api/site/audit", url.Values{"id": {siteID}}, &resp)
	return resp.Trail, err
}

// Kind is the kind of object a Write changes.
type Kind string

// Write kinds, one per portal write endpoint.
const (
	KindSite   Kind = "site"
	KindDevice Kind = "device"
	KindClient Kind = "client"
)

// Write is one cloud mutation. Site and device writes replace a single top-level key
// wholesale, exactly as the portal does; client writes set fields on a client record.
// The same type is used to stage changes and to record pre-images for rollback.
type Write struct {
	Kind   Kind   `json:"kind"`
	SiteID string `json:"site_id"`
	// ID is the device id for device writes and the client id for client writes.
	ID    string `json:"id,omitempty"`
	Key   string `json:"key,omitempty"`
	Value any    `json:"value"`
}

// Apply performs a Write.
func (c *Client) Apply(ctx context.Context, w Write) error {
	switch w.Kind {
	case KindSite:
		return c.post(ctx, "/api/site", Object{"id": w.SiteID, w.Key: w.Value})
	case KindDevice:
		return c.post(ctx, "/api/device/edit", Object{"id": w.ID, w.Key: w.Value})
	case KindClient:
		fields, ok := w.Value.(Object)
		if !ok {
			return fmt.Errorf("client write for %s: value must be an object, got %T", w.ID, w.Value)
		}
		body := Object{"siteid": w.SiteID, "id": w.ID}
		for k, v := range fields {
			body[k] = v
		}
		return c.post(ctx, "/api/client/edit", body)
	default:
		return fmt.Errorf("unknown write kind %q", w.Kind)
	}
}
