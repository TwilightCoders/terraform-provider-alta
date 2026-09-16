package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/TwilightCoders/terraform-provider-alta/internal/device"
)

func fileConfig(content, mode, onDestroy string) string {
	return fmt.Sprintf(`
resource "alta_device_file" "post_cfg" {
  path       = "/cfg/post-cfg.sh"
  content    = %q
  mode       = %q
  on_destroy = %q
}
`, content, mode, onDestroy)
}

const fileResource = "alta_device_file.post_cfg"

func TestDeviceFileLifecycle(t *testing.T) {
	h := newHarness(t)
	hooks := newFakeHooks()
	h.hooks = hooks

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				Config: providerBlock(false) + fileConfig("#!/bin/ash\nexit 0\n", "0755", "keep"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(fileResource, "mode", "0755"),
					resource.TestCheckResourceAttr(fileResource, "drift_detection", "content"),
					resource.TestCheckResourceAttr(fileResource, "backup", "true"),
					resource.TestCheckResourceAttrSet(fileResource, "sha256"),
				),
			},
			{
				Config:   providerBlock(false) + fileConfig("#!/bin/ash\nexit 0\n", "0755", "keep"),
				PlanOnly: true,
			},
			{
				// An edit made on the router shows up as drift, not as silence.
				PreConfig: func() {
					hooks.files["/cfg/post-cfg.sh"] = device.File{
						Path: "/cfg/post-cfg.sh", Content: "#!/bin/ash\n# someone edited the box\nexit 0\n", Mode: "0755",
					}
				},
				Config:             providerBlock(false) + fileConfig("#!/bin/ash\nexit 0\n", "0755", "keep"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: providerBlock(false) + fileConfig("#!/bin/ash\nexit 0\n", "0755", "keep"),
				Check: func(*terraform.State) error {
					if got := hooks.files["/cfg/post-cfg.sh"].Content; got != "#!/bin/ash\nexit 0\n" {
						return fmt.Errorf("apply did not restore the file: %q", got)
					}
					return nil
				},
			},
		},
	})

	// on_destroy defaults to keeping a file the router depends on.
	for _, path := range hooks.deleted {
		if path == "/cfg/post-cfg.sh" {
			t.Error("the file was deleted despite on_destroy = keep")
		}
	}
}

func TestDeviceFileDeletesWhenAsked(t *testing.T) {
	h := newHarness(t)
	hooks := newFakeHooks()
	h.hooks = hooks

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: providerBlock(false) + fileConfig("x\n", "0644", "delete"),
		}},
	})
	if len(hooks.deleted) != 1 || hooks.deleted[0] != "/cfg/post-cfg.sh" {
		t.Errorf("deleted = %v", hooks.deleted)
	}
}

func TestDeviceFileRejectsUnsafeInput(t *testing.T) {
	h := newHarness(t)
	h.hooks = newFakeHooks()
	cases := map[string]string{
		`must be octal`:            fileConfig("x", "rwx", "keep"),
		`must be an absolute path`: "resource \"alta_device_file\" \"bad\" {\n  path    = \"cfg/x\"\n  content = \"x\"\n  mode    = \"0644\"\n}",
		`read_only`:                "",
	}
	for want, config := range cases {
		if config == "" {
			config = providerBlock(true) + fileConfig("x", "0644", "keep")
		} else {
			config = providerBlock(false) + config
		}
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: h.factories(),
			Steps:                    []resource.TestStep{{Config: config, ExpectError: regexp.MustCompile(want)}},
		})
	}
}
