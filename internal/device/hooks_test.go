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
	t       *testing.T
	root    string
	ext     *Extensions
	postCfg string
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
	r := &hookRouter{t: t, root: root, ext: NewExtensions(localRunner{path: os.Getenv("PATH")}, layout)}
	r.postCfg = r.read("post-cfg.sh")
	return r
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
	if !state.Present || !state.Installed || state.SHA256 == "" {
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

	// The provider owns a loader file; post-cfg.sh belongs to whoever manages that file.
	if before, after := r.postCfg, r.read("post-cfg.sh"); before != after {
		t.Errorf("post-cfg.sh was modified:\n%s", after)
	}
	loader := r.read(filepath.Join("tf.d", "loader.sh"))
	if !strings.Contains(loader, "hotplug/*") || strings.Contains(loader, "install ") {
		t.Errorf("loader.sh = %q", loader)
	}
	if !state.LoaderPresent {
		t.Error("the provider wrote the loader, so LoaderPresent must be true")
	}
	if state.Sourced {
		t.Error("post-cfg.sh does not source the loader, so Sourced must be false")
	}
}

// TestHookReportsAMissingLoader covers the case the loader exists for: the file the
// provider owns is gone, which has to reach the plan rather than a warning.
func TestHookReportsAMissingLoader(t *testing.T) {
	r := newHookRouter(t)
	hook := resolverHook
	hook.Script, hook.Run = "true\n", false
	_, err := r.ext.Put(context.Background(), hook)
	must(t, err)
	must(t, os.Remove(filepath.Join(r.root, "tf.d", "loader.sh")))

	got, err := r.ext.Get(context.Background(), hook)
	must(t, err)
	if !got.Present {
		t.Fatal("the hook itself is still there")
	}
	if got.LoaderPresent {
		t.Error("the loader was deleted, so LoaderPresent must be false")
	}
}

func TestHookReportsWhenPostCfgSourcesTheLoader(t *testing.T) {
	r := newHookRouter(t)
	must(t, os.WriteFile(filepath.Join(r.root, "post-cfg.sh"),
		[]byte("#!/bin/ash\n"+r.ext.layout.SourceLine()+"\nexit 0\n"), 0o755))
	hook := resolverHook
	hook.Script, hook.Run = "true\n", false

	state, err := r.ext.Put(context.Background(), hook)
	must(t, err)
	if !state.Sourced {
		t.Error("the source line is present, so Sourced must be true")
	}
	got, err := r.ext.Get(context.Background(), hook)
	must(t, err)
	if !got.Sourced || !got.LoaderPresent {
		t.Errorf("Get disagrees with Put about the loader: %+v", got)
	}
}

// TestLoaderRunsHooksTheWayBootDoes executes the loader itself.
func TestLoaderRunsHooksTheWayBootDoes(t *testing.T) {
	r := newHookRouter(t)
	hook := resolverHook
	hook.Script, hook.Run = "true\n", false
	_, err := r.ext.Put(context.Background(), hook)
	must(t, err)
	must(t, os.Remove(filepath.Join(r.root, "etc", "hotplug.d", "iface", hook.FileName())))

	_, err = localRunner{path: os.Getenv("PATH")}.Run(context.Background(), "sh "+filepath.Join(r.root, "tf.d", "loader.sh"), nil)
	must(t, err)
	if r.read(filepath.Join("etc", "hotplug.d", "iface", hook.FileName())) == "" {
		t.Error("the loader did not reinstall the hook")
	}
}

func TestHookPutIsIdempotent(t *testing.T) {
	r := newHookRouter(t)
	hook := resolverHook
	hook.Script, hook.Run = "true\n", false

	first, err := r.ext.Put(context.Background(), hook)
	must(t, err)
	loader := r.read(filepath.Join("tf.d", "loader.sh"))

	second, err := r.ext.Put(context.Background(), hook)
	must(t, err)
	if first.SHA256 != second.SHA256 {
		t.Errorf("hash changed for identical content: %s vs %s", first.SHA256, second.SHA256)
	}
	if again := r.read(filepath.Join("tf.d", "loader.sh")); again != loader {
		t.Error("loader.sh changed on a repeat apply")
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
	if !got.Present || !got.Installed || got.SHA256 != put.SHA256 {
		t.Fatalf("state = %+v, want the hook as written (%s)", got, put.SHA256)
	}
	if strings.TrimSpace(got.Script) != strings.TrimSpace(hook.body(r.ext.layout.HookDir)) {
		t.Errorf("script = %q", got.Script)
	}
}

// TestHookRecordsThatItRan separates "the hook is installed" from "the hook runs", which
// an intact firewall chain cannot answer while post-cfg.sh asserts the same rule.
func TestHookRecordsThatItRan(t *testing.T) {
	r := newHookRouter(t)
	hook := resolverHook
	hook.Script, hook.Run = "true\n", true
	_, err := r.ext.Put(context.Background(), hook)
	must(t, err)

	if ran := r.read(filepath.Join("tf.d", "run", hook.FileName())); ran == "" {
		t.Error("the hook left no record of running")
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

func TestHookRefusesWhenTheRouterLacksABinary(t *testing.T) {
	r := newHookRouter(t)
	hook := resolverHook
	hook.Script, hook.Run = "true\n", false
	hook.Requires = []string{"iptables-but-not-really"}

	_, err := r.ext.Put(context.Background(), hook)
	if err == nil || !strings.Contains(err.Error(), "iptables-but-not-really") {
		t.Fatalf("err = %v, want the missing binary named", err)
	}
	if r.read(filepath.Join("tf.d", "hotplug", hook.FileName())) != "" {
		t.Error("the hook was written despite the missing binary")
	}
}

// TestGeneratedShellAvoidsAbsentBusyboxCommands guards the class of bug that shipped:
// a command that exists on a developer's machine and not on the router, in generated
// shell that only runs at boot.
func TestGeneratedShellAvoidsAbsentBusyboxCommands(t *testing.T) {
	absent := []string{"install", "realpath", "timeout -k", "readlink -f", "sed -i ", "head -n -"}
	ext := NewExtensions(localRunner{}, Route10Layout())
	rendered := map[string]string{"loader": ext.loaderScript()}
	for _, name := range []string{"hook-put.sh", "hook-get.sh", "hook-delete.sh", "file-put.sh", "file-get.sh"} {
		if strings.HasPrefix(name, "file-") {
			rendered[name] = render(name, fileData{Layout: Route10Layout(), File: File{Path: "/cfg/x", Mode: "0755"}})
			continue
		}
		rendered[name] = render(name, ext.data(Hook{Name: "x", Interface: "wg0", Script: "true"}))
	}
	for name, script := range rendered {
		for _, cmd := range absent {
			if strings.Contains(script, cmd+" ") || strings.Contains(script, cmd+"\n") {
				t.Errorf("%s uses %q, which the router's busybox does not have", name, cmd)
			}
		}
	}
}
