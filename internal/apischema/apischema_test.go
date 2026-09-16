package apischema

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/TwilightCoders/terraform-provider-alta/api"
	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud/cloudtest"
)

var update = flag.Bool("update", false, "refit object shapes from the fixtures and rewrite api/schema.json")

func load(t *testing.T) *Schema {
	t.Helper()
	s, err := Load(api.Schema)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fixtureCaptures returns every recorded capture, oldest first.
func fixtureCaptures(t *testing.T) []Capture {
	t.Helper()
	var out []Capture
	for _, f := range []struct{ dir, site, state string }{
		{"2026-05-11", "cloud-site-2026-09-14.json", "cloud-state-2026-09-14.json"},
		{"2026-09-14", "cloud-site.json", "cloud-state.json"},
	} {
		site, state := cloudtest.Fixture(t, f.dir, f.site, f.state)
		out = append(out, capture(t, site, state))
	}
	return out
}

func capture(t *testing.T, site cloud.Object, state cloud.State) Capture {
	t.Helper()
	c, err := CaptureFrom(site, state, cloudtest.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestFitMatchesFixtures keeps api/schema.json honest: the checked-in object shapes must
// be exactly what the captures show. Run with -update after recapturing.
func TestFitMatchesFixtures(t *testing.T) {
	fitted := load(t)
	if err := fitted.Fit(fixtureCaptures(t)...); err != nil {
		t.Fatal(err)
	}
	data, err := fitted.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	if *update {
		_, file, _, _ := runtime.Caller(0)
		path := filepath.Join(filepath.Dir(file), "..", "..", "api", "schema.json")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("wrote", path)
		return
	}
	if string(data) != string(api.Schema) {
		t.Error("api/schema.json does not match the fixtures; run `make schema` and review the diff")
	}
}

func TestFixturesConform(t *testing.T) {
	for _, err := range load(t).Conform(fixtureCaptures(t)...) {
		t.Error(err)
	}
}

func TestSchemaIsInternallyConsistent(t *testing.T) {
	s := load(t)
	for _, c := range s.Collections {
		if _, ok := s.Objects[c.Object]; !ok {
			t.Errorf("collection %q references unknown object %q", c.Name, c.Object)
		}
	}
	for _, e := range s.Endpoints {
		if e.Token != "header" && e.Token != "body" {
			t.Errorf("endpoint %q: token = %q, want header or body", e.Name, e.Token)
		}
	}
	for _, r := range s.Compile {
		switch r.Status {
		case "ok", "defect":
			if r.Observed == "" {
				t.Errorf("compile rule %q is %s but records no observation date", r.Cloud, r.Status)
			}
		case "unverified":
		default:
			t.Errorf("compile rule %q: unknown status %q", r.Cloud, r.Status)
		}
	}
	if len(s.Defects()) == 0 {
		t.Error("no compile defects recorded; the two known ones should be")
	}
	if s.Provenance.PortalBundle.SHA256 == "" || s.Provenance.RouterFirmware == "" {
		t.Error("provenance must say what this was observed from")
	}
}

// TestConformDetectsDrift proves the check fails when the API returns something new.
func TestConformDetectsDrift(t *testing.T) {
	s := load(t)
	captures := fixtureCaptures(t)
	rules, _ := captures[1].Site["firewall"].(map[string]any)["nat"].(map[string]any)["rules"].([]any)
	rule := rules[0].(map[string]any)
	rule["newFieldAltaAdded"] = "surprise"
	rule["zoneIn"] = 42

	problems := s.Conform(captures...)
	if len(problems) < 2 {
		t.Fatalf("expected an undescribed field and a type change, got %v", problems)
	}
}

// liveCapture reads a real site. It only reads.
func liveCapture(t *testing.T, siteID, deviceID string) Capture {
	t.Helper()
	client := cloud.NewClient(cloud.Config{Email: os.Getenv("ALTA_LABS_EMAIL"), Password: os.Getenv("ALTA_LABS_PASSWORD")})
	ctx := context.Background()
	site, err := client.Site(ctx, siteID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.State(ctx, siteID)
	if err != nil {
		t.Fatal(err)
	}
	live, err := CaptureFrom(site, state, deviceID)
	if err != nil {
		t.Fatal(err)
	}
	return live
}

// TestLiveSiteConforms checks a real site against the schema. It only reads.
func TestLiveSiteConforms(t *testing.T) {
	siteID, deviceID := os.Getenv("ALTA_LABS_SITE_ID"), os.Getenv("ALTA_LABS_DEVICE_ID")
	if os.Getenv("TF_ACC") == "" || siteID == "" || deviceID == "" {
		t.Skip("set TF_ACC=1, ALTA_LABS_SITE_ID and ALTA_LABS_DEVICE_ID (plus credentials) to run")
	}
	for _, err := range load(t).Conform(liveCapture(t, siteID, deviceID)) {
		t.Error(err)
	}
}
