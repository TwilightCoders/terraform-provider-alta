package routerconfig

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/TwilightCoders/terraform-provider-alta-labs/internal/cloud"
)

const (
	fixtureSite   = "aBcDeFgHiJkLmNoPqRsTu"
	fixtureRouter = "0a1b2c3d4e5f"
)

func loadJSON(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "fixtures", path))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// fixtureDocument loads the cloud objects captured in a fixture directory.
func fixtureDocument(t *testing.T, dir, siteFile, stateFile string) *Document {
	t.Helper()
	var site cloud.Object
	var state cloud.State
	loadJSON(t, filepath.Join(dir, siteFile), &site)
	loadJSON(t, filepath.Join(dir, stateFile), &state)
	doc, err := NewDocument(fixtureSite, fixtureRouter, site, state)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func beforePhase0(t *testing.T) *Document {
	return fixtureDocument(t, "2026-05-11", "cloud-site-2026-09-14.json", "cloud-state-2026-09-14.json")
}

func afterPhase0(t *testing.T) *Document {
	return fixtureDocument(t, "2026-09-14", "cloud-site.json", "cloud-state.json")
}

func writeKeys(writes []cloud.Write) []string {
	keys := make([]string, 0, len(writes))
	for _, w := range writes {
		keys = append(keys, string(w.Kind)+"."+w.Key+w.ID)
	}
	return keys
}

func TestReadingAndReapplyingIsANoOp(t *testing.T) {
	for name, doc := range map[string]*Document{"before": beforePhase0(t), "after": afterPhase0(t)} {
		writes, _, err := Read(doc).Plan(doc)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(writes) != 0 {
			t.Errorf("%s: expected no writes, got %v", name, writeKeys(writes))
		}
	}
}

func TestPlanReproducesPhase0(t *testing.T) {
	before, after := beforePhase0(t), afterPhase0(t)
	desired := Read(after)

	writes, pre, err := desired.Plan(before)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"site.firewall", "site.vlans", "device.portsCfg" + fixtureRouter}
	if got := writeKeys(writes); !reflect.DeepEqual(got, want) {
		t.Fatalf("writes = %v, want %v", got, want)
	}

	applied := before.Clone()
	for _, w := range writes {
		setWrite(applied, w)
	}
	if got := Read(applied); !reflect.DeepEqual(got, desired) {
		t.Errorf("configuration after writes differs from desired\n got %+v\nwant %+v", got, desired)
	}

	restored := applied.Clone()
	for _, w := range pre {
		setWrite(restored, w)
	}
	if !reflect.DeepEqual(restored.Site, before.Site) || !reflect.DeepEqual(restored.Device, before.Device) {
		t.Error("pre-images do not restore the original documents")
	}
}

// setWrite simulates the cloud accepting a write.
func setWrite(d *Document, w cloud.Write) {
	switch w.Kind {
	case cloud.KindSite:
		d.Site[w.Key] = deepCopy(w.Value)
	case cloud.KindDevice:
		d.Device[w.Key] = deepCopy(w.Value)
	case cloud.KindClient:
		panic("client writes are not simulated here")
	}
}

func TestUnmanagedSectionsAreUntouched(t *testing.T) {
	before, after := beforePhase0(t), afterPhase0(t)
	desired := Config{VLANs: Read(after).VLANs}

	writes, _, err := desired.Plan(before)
	if err != nil {
		t.Fatal(err)
	}
	if got := writeKeys(writes); !reflect.DeepEqual(got, []string{"site.vlans"}) {
		t.Fatalf("writes = %v", got)
	}
}

func TestPreservesFieldsTheModelDoesNotKnow(t *testing.T) {
	doc := &Document{Site: cloud.Object{"firewall": cloud.Object{"nat": cloud.Object{"rules": []any{
		cloud.Object{"id": "a", "description": "old", "futureField": "keep me", "destination": cloud.Object{"port": json.Number("80")}},
	}}}}}
	fwd := Read(doc).PortForwards
	(*fwd)[0].Description = "new"

	next := doc.Clone()
	if err := (Config{PortForwards: fwd}).Apply(next); err != nil {
		t.Fatal(err)
	}
	rule := next.Site["firewall"].(cloud.Object)["nat"].(cloud.Object)["rules"].([]any)[0].(cloud.Object)
	if rule["futureField"] != "keep me" || rule["description"] != "new" {
		t.Errorf("rule = %v", rule)
	}
	if rule["destination"].(cloud.Object)["port"] != json.Number("80") {
		t.Errorf("unchanged port representation was rewritten: %#v", rule["destination"])
	}
}

func TestListsAreAuthoritativeAndOrdered(t *testing.T) {
	doc := &Document{Site: cloud.Object{"routes": []any{
		cloud.Object{"id": "r1", "network": "10.0.0.0/8"},
		cloud.Object{"id": "r2", "network": "172.16.0.0/12"},
	}}}
	routes := []StaticRoute{{ID: "r3", Type: "next-hop", Network: "198.18.20.0/28", NextHop: "192.0.2.10"}, {ID: "r1", Network: "10.0.0.0/8"}}
	if err := (Config{StaticRoutes: &routes}).Apply(doc); err != nil {
		t.Fatal(err)
	}
	got := Read(doc).StaticRoutes
	if len(*got) != 2 || (*got)[0].ID != "r3" || (*got)[1].ID != "r1" {
		t.Fatalf("routes = %+v", *got)
	}
}

func TestListRejectsMissingAndDuplicateIDs(t *testing.T) {
	for name, rules := range map[string][]FirewallRule{
		"missing":   {{Description: "no id"}},
		"duplicate": {{ID: "x"}, {ID: "x"}},
	} {
		doc := &Document{Site: cloud.Object{}}
		if err := (Config{FirewallRules: &rules}).Apply(doc); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestEmptyManagedListLeavesAbsentKeyAlone(t *testing.T) {
	doc := &Document{Site: cloud.Object{"routes": nil}}
	if err := (Config{StaticRoutes: &[]StaticRoute{}}).Apply(doc); err != nil {
		t.Fatal(err)
	}
	if doc.Site["routes"] != nil {
		t.Errorf("routes = %v, want untouched nil", doc.Site["routes"])
	}
}
