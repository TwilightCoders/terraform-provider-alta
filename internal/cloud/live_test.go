package cloud

import (
	"context"
	"os"
	"testing"
)

// TestLiveReads exercises the read endpoints against a real site. It never writes.
// Requires TF_ACC=1, ALTA_LABS_EMAIL, ALTA_LABS_PASSWORD and ALTA_LABS_SITE_ID.
func TestLiveReads(t *testing.T) {
	siteID := os.Getenv("ALTA_LABS_SITE_ID")
	if os.Getenv("TF_ACC") == "" || siteID == "" {
		t.Skip("set TF_ACC=1 and ALTA_LABS_SITE_ID (plus credentials) to run")
	}
	c := NewClient(Config{Email: os.Getenv("ALTA_LABS_EMAIL"), Password: os.Getenv("ALTA_LABS_PASSWORD")})
	ctx := context.Background()

	site, err := c.Site(ctx, siteID)
	if err != nil {
		t.Fatal(err)
	}
	if site["id"] != siteID {
		t.Errorf("site id = %v", site["id"])
	}

	state, err := c.State(ctx, siteID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Devices) == 0 {
		t.Error("site has no devices")
	}

	if _, err := c.Audit(ctx, siteID); err != nil {
		t.Fatal(err)
	}
}
