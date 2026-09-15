// Package routerconfig maps a router's desired configuration onto the Alta cloud
// objects it is compiled from, and derives the minimal set of cloud writes.
//
// It is pure: no I/O. Desired state is overlaid onto a working copy of the cloud
// documents, preserving every field this package does not model, then diffed against
// the original to produce writes.
package routerconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
)

// Document is a working copy of the cloud objects one router's configuration lives in.
type Document struct {
	SiteID   string
	DeviceID string
	Site     cloud.Object
	Device   cloud.Object
	// Clients are client records keyed by client id (lowercase MAC, no separators).
	Clients map[string]cloud.Object
}

// NewDocument copies the relevant parts of a site read so the caller's data is never mutated.
func NewDocument(siteID, deviceID string, site cloud.Object, state cloud.State) (*Document, error) {
	device, ok := state.Device(deviceID)
	if !ok {
		return nil, fmt.Errorf("device %q is not part of site %q", deviceID, siteID)
	}
	clients := make(map[string]cloud.Object, len(state.Clients))
	for _, c := range state.Clients {
		if id, ok := c["id"].(string); ok {
			clients[id] = c
		}
	}
	doc := &Document{SiteID: siteID, DeviceID: deviceID, Site: site, Device: device, Clients: clients}
	return doc.Clone(), nil
}

// Clone returns a deep copy.
func (d *Document) Clone() *Document {
	return &Document{
		SiteID:   d.SiteID,
		DeviceID: d.DeviceID,
		Site:     deepCopy(d.Site),
		Device:   deepCopy(d.Device),
		Clients:  deepCopy(d.Clients),
	}
}

// Writes returns the cloud writes that turn base into d, in a stable order: site keys,
// device keys, then clients.
func (d *Document) Writes(base *Document) []cloud.Write {
	var writes []cloud.Write
	for _, key := range changedKeys(base.Site, d.Site) {
		writes = append(writes, cloud.Write{Kind: cloud.KindSite, SiteID: d.SiteID, Key: key, Value: d.Site[key]})
	}
	for _, key := range changedKeys(base.Device, d.Device) {
		writes = append(writes, cloud.Write{Kind: cloud.KindDevice, SiteID: d.SiteID, ID: d.DeviceID, Key: key, Value: d.Device[key]})
	}
	for _, id := range sortedKeys(d.Clients) {
		if reflect.DeepEqual(clientEdit(base.Clients[id]), clientEdit(d.Clients[id])) {
			continue
		}
		writes = append(writes, cloud.Write{Kind: cloud.KindClient, SiteID: d.SiteID, ID: id, Value: clientEdit(d.Clients[id])})
	}
	return writes
}

// PreImages returns the writes that would restore base's values for every write in
// writes, which is what a rollback replays.
func PreImages(base *Document, writes []cloud.Write) []cloud.Write {
	pre := make([]cloud.Write, 0, len(writes))
	for _, w := range writes {
		restore := w
		switch w.Kind {
		case cloud.KindSite:
			restore.Value = base.Site[w.Key]
		case cloud.KindDevice:
			restore.Value = base.Device[w.Key]
		case cloud.KindClient:
			restore.Value = clientEdit(base.Clients[w.ID])
		}
		pre = append(pre, restore)
	}
	return pre
}

func changedKeys(base, next cloud.Object) []string {
	var keys []string
	for _, k := range sortedKeys(next) {
		if !reflect.DeepEqual(base[k], next[k]) {
			keys = append(keys, k)
		}
	}
	return keys
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// deepCopy round-trips through JSON, which is exactly the value domain the API uses.
func deepCopy[T any](v T) T {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("routerconfig: copying document: %v", err))
	}
	var out T
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		panic(fmt.Sprintf("routerconfig: copying document: %v", err))
	}
	return out
}
