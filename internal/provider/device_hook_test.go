package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/TwilightCoders/terraform-provider-alta/internal/device"
)

// fakeHooks is a router that remembers the hooks written to it.
type fakeHooks struct {
	stored  map[string]device.Hook
	ran     []string
	deleted []string
	destroy []string
	loader  bool
}

func newFakeHooks() *fakeHooks { return &fakeHooks{stored: map[string]device.Hook{}} }

func (f *fakeHooks) Put(_ context.Context, h device.Hook) (device.HookState, error) {
	f.stored[h.Name] = h
	f.loader = true
	if h.Run {
		f.ran = append(f.ran, h.Name)
	}
	return f.state(h), nil
}

func (f *fakeHooks) Get(_ context.Context, h device.Hook) (device.HookState, error) {
	stored, ok := f.stored[h.Name]
	if !ok {
		return device.HookState{}, nil
	}
	return f.state(stored), nil
}

func (f *fakeHooks) Delete(_ context.Context, h device.Hook, destroy string) error {
	delete(f.stored, h.Name)
	f.deleted = append(f.deleted, h.Name)
	f.destroy = append(f.destroy, destroy)
	return nil
}

func (f *fakeHooks) state(h device.Hook) device.HookState {
	sum := sha256.Sum256([]byte(h.Script))
	return device.HookState{
		Present: true, Loader: f.loader, Installed: h.Interface != "",
		SHA256: hex.EncodeToString(sum[:]), Script: h.Script,
	}
}

func hookConfig(script string) string {
	return fmt.Sprintf(`
resource "alta_device_hook" "resolver" {
  name      = "resolver-exemption"
  interface = "wg0"
  script    = %q

  destroy_script = "true"
}
`, script)
}

const hookResource = "alta_device_hook.resolver"

func TestDeviceHookLifecycle(t *testing.T) {
	h := newHarness(t)
	hooks := newFakeHooks()
	h.hooks = hooks

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				Config: providerBlock(false) + hookConfig("iptables -C CHAIN -j RETURN || iptables -I CHAIN -j RETURN\n"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(hookResource, "path", "99-zz-alta-resolver-exemption"),
					resource.TestCheckResourceAttr(hookResource, "run_on_apply", "true"),
					resource.TestCheckResourceAttrSet(hookResource, "sha256"),
					func(*terraform.State) error {
						if len(hooks.ran) != 1 {
							return fmt.Errorf("hook ran %d times, want 1", len(hooks.ran))
						}
						if got := hooks.stored["resolver-exemption"].Interface; got != "wg0" {
							return fmt.Errorf("stored interface = %q", got)
						}
						return nil
					},
				),
			},
			{
				Config:   providerBlock(false) + hookConfig("iptables -C CHAIN -j RETURN || iptables -I CHAIN -j RETURN\n"),
				PlanOnly: true,
			},
			{
				// A changed script reinstalls and runs again.
				Config: providerBlock(false) + hookConfig("iptables -w -C CHAIN -j RETURN || iptables -w -I CHAIN -j RETURN\n"),
				Check: func(*terraform.State) error {
					if len(hooks.ran) != 2 {
						return fmt.Errorf("hook ran %d times after an edit, want 2", len(hooks.ran))
					}
					return nil
				},
			},
		},
	})

	if len(hooks.deleted) != 1 || hooks.destroy[0] != "true" {
		t.Errorf("destroy: deleted=%v scripts=%v", hooks.deleted, hooks.destroy)
	}
}

func TestDeviceHookRefusedWhenReadOnlyOrWithoutRouter(t *testing.T) {
	readOnly := newHarness(t)
	readOnly.hooks = newFakeHooks()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: readOnly.factories(),
		Steps: []resource.TestStep{{
			Config:      providerBlock(true) + hookConfig("true\n"),
			ExpectError: regexp.MustCompile(`read_only`),
		}},
	})

	noRouter := newHarness(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: noRouter.factories(),
		Steps: []resource.TestStep{{
			Config: `provider "alta" {
  email    = "test"
  password = "test"
}` + hookConfig("true\n"),
			ExpectError: regexp.MustCompile(`needs the provider's ssh block`),
		}},
	})
}

func TestDeviceHookRejectsUnusableNames(t *testing.T) {
	h := newHarness(t)
	h.hooks = newFakeHooks()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: providerBlock(false) + `
resource "alta_device_hook" "bad" {
  name   = "Resolver Exemption"
  script = "true"
}`,
			ExpectError: regexp.MustCompile(`lowercase letters, digits and dashes`),
		}},
	})
}
