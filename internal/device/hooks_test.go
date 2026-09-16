package device

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hookRouter is a fake router with the directories a hook touches.
type hookRouter struct {
	t    *testing.T
	root string
	ext  *Extensions
}

func newHookRouter(t *testing.T) *hookRouter {
	t.Helper()
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "etc", "hotplug.d", "iface"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "post-cfg.sh"), []byte("#!/bin/ash\n\nip route add 203.0.113.0/24 dev br-lan\n\nexit 0\n"), 0o755))

	layout := Route10Layout()
	layout.HookDir = filepath.Join(root, "tf.d")
	layout.HotplugDir = filepath.Join(root, "etc", "hotplug.d", "iface")
	layout.PostCfg = filepath.Join(root, "post-cfg.sh")
	return &hookRouter{t: t, root: root, ext: NewExtensions(localRunner{path: os.Getenv("PATH")}, layout)}
}

func (r *hookRouter) read(path string) string {
	data, err := os.ReadFile(filepath.Join(r.root, path))
	if err != nil {
		return ""
	}
	return string(data)
}

var resolverHook = Hook{
	Name:      "resolver-exemption",
	Interface: "wg0",
	Script:    "touch \"$MARKER\"\n",
	Run:       true,
}

func TestHookIsStoredInstalledAndLoadable(t *testing.T) {
	r := newHookRouter(t)
	marker := filepath.Join(r.root, "ran")
	hook := resolverHook
	hook.Script = "touch " + shellQuote(marker) + "\n"

	state, err := r.ext.Put(context.Background(), hook)
	must(t, err)
	if !state.Present || !state.Loader || !state.Installed || state.SHA256 == "" {
		t.Fatalf("state = %+v", state)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("Run did not execute the hook")
	}

	stored := r.read(filepath.Join("tf.d", "hotplug", hook.FileName()))
	if !strings.Contains(stored, `[ "$ACTION" = 'ifup' ]`) || !strings.Contains(stored, `[ "$INTERFACE" = 'wg0' ]`) {
		t.Errorf("hook body lacks its event guards:\n%s", stored)
	}
	if installed := r.read(filepath.Join("etc", "hotplug.d", "iface", hook.FileName())); installed != stored {
		t.Error("the hook was not installed for the current boot")
	}

	postCfg := r.read("post-cfg.sh")
	if !strings.Contains(postCfg, loaderMarker) {
		t.Fatalf("post-cfg.sh has no loader block:\n%s", postCfg)
	}
	if !strings.HasSuffix(strings.TrimSpace(postCfg), "exit 0") {
		t.Errorf("the loader block must sit before the trailing exit 0:\n%s", postCfg)
	}
	if !strings.Contains(postCfg, "ip route add 203.0.113.0/24") {
		t.Error("existing post-cfg.sh content was lost")
	}
}

func TestHookPutIsIdempotent(t *testing.T) {
	r := newHookRouter(t)
	hook := resolverHook
	hook.Script, hook.Run = "true\n", false

	first, err := r.ext.Put(context.Background(), hook)
	must(t, err)
	before := r.read("post-cfg.sh")

	second, err := r.ext.Put(context.Background(), hook)
	must(t, err)
	if first.SHA256 != second.SHA256 {
		t.Errorf("hash changed for identical content: %s vs %s", first.SHA256, second.SHA256)
	}
	if after := r.read("post-cfg.sh"); after != before {
		t.Errorf("the loader block was added twice:\n%s", after)
	}
	if n := strings.Count(before, loaderMarker); n != 2 { // one open marker, one close
		t.Errorf("loader markers = %d, want 2", n)
	}
}

func TestHookGetReportsWhatTheRouterHolds(t *testing.T) {
	r := newHookRouter(t)
	hook := resolverHook
	hook.Script, hook.Run = "true\n", false

	missing, err := r.ext.Get(context.Background(), hook)
	must(t, err)
	if missing.Present {
		t.Fatal("reported present before it was written")
	}

	put, err := r.ext.Put(context.Background(), hook)
	must(t, err)
	got, err := r.ext.Get(context.Background(), hook)
	must(t, err)
	if !got.Present || !got.Loader || !got.Installed || got.SHA256 != put.SHA256 {
		t.Fatalf("state = %+v, want the hook as written (%s)", got, put.SHA256)
	}
	if strings.TrimSpace(got.Script) != strings.TrimSpace(hook.body()) {
		t.Errorf("script = %q", got.Script)
	}
}

func TestHookDeleteRemovesAndUndoes(t *testing.T) {
	r := newHookRouter(t)
	hook := resolverHook
	hook.Script, hook.Run = "true\n", false
	_, err := r.ext.Put(context.Background(), hook)
	must(t, err)

	undone := filepath.Join(r.root, "undone")
	must(t, r.ext.Delete(context.Background(), hook, "touch "+shellQuote(undone)))

	state, err := r.ext.Get(context.Background(), hook)
	must(t, err)
	if state.Present || state.Installed {
		t.Errorf("state after delete = %+v", state)
	}
	if _, err := os.Stat(undone); err != nil {
		t.Error("the destroy script did not run")
	}
}

func TestBootHookHasNoEventGuards(t *testing.T) {
	r := newHookRouter(t)
	hook := Hook{Name: "boot-thing", Script: "true\n"}

	state, err := r.ext.Put(context.Background(), hook)
	must(t, err)
	if state.Installed {
		t.Error("a boot hook has nothing to install into hotplug")
	}
	stored := r.read(filepath.Join("tf.d", "boot", hook.FileName()))
	if strings.Contains(stored, "ACTION") {
		t.Errorf("boot hook carries hotplug guards:\n%s", stored)
	}
}

func TestHookValidation(t *testing.T) {
	for name, h := range map[string]Hook{
		"no name":           {Script: "true"},
		"slash in name":     {Name: "a/b", Script: "true"},
		"action without if": {Name: "a", Action: "ifup", Script: "true"},
		"no script":         {Name: "a", Script: "  \n"},
	} {
		if _, err := newHookRouter(t).ext.Put(context.Background(), h); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestHookFileNames(t *testing.T) {
	cases := map[string]Hook{
		"99-zz-alta-dns":  {Name: "dns", Interface: "wg0"},
		"10-alta-dns":     {Name: "dns", Interface: "wg0", Priority: "10"},
		"housekeeping.sh": {Name: "housekeeping"},
	}
	for want, h := range cases {
		if got := h.FileName(); got != want {
			t.Errorf("FileName() = %q, want %q", got, want)
		}
	}
}
