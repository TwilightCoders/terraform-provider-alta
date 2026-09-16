package device

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Hook is a small script the router runs for itself: at boot, or when an interface comes
// up. It exists for the handful of behaviours the Alta cloud has no concept of.
//
// Hooks live in the router's persistent storage and are installed by one managed block in
// post-cfg.sh, because /etc is rebuilt on every boot and every configuration push. A hook
// must be idempotent: it runs again on every boot, push, and matching interface event.
type Hook struct {
	Name string
	// Interface and Action make this an interface hotplug hook, e.g. "wg0" and "ifup".
	// With no Interface, the hook runs at boot.
	Interface string
	Action    string
	// Priority orders the file among the router's other hotplug scripts.
	Priority string
	Script   string
	// Run asks for the hook to run once now, rather than only at the next event.
	Run bool
	// Requires are binaries the script needs. They are checked on the router before the
	// hook is written, so a missing one is a clear error now rather than a silent failure
	// at the next boot.
	Requires []string
}

// DefaultPriority sorts provider hooks after the router's own interface scripts.
const DefaultPriority = "99-zz"

// FileName is the hook's name on the router.
func (h Hook) FileName() string {
	if h.Interface == "" {
		return h.Name + ".sh"
	}
	return h.normalise().Priority + "-alta-" + h.Name
}

// normalise fills in the defaults the scripts and file names rely on.
func (h Hook) normalise() Hook {
	if h.Interface != "" && h.Action == "" {
		h.Action = "ifup"
	}
	if h.Interface != "" && h.Priority == "" {
		h.Priority = DefaultPriority
	}
	return h
}

// body wraps the script so a hotplug hook only acts on its own event.
func (h Hook) body() string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# Managed by terraform-provider-alta. Edits here are overwritten.\n")
	if h.Interface != "" {
		fmt.Fprintf(&b, "[ \"$ACTION\" = %s ] || exit 0\n[ \"$INTERFACE\" = %s ] || exit 0\n",
			shellQuote(h.normalise().Action), shellQuote(h.Interface))
	}
	b.WriteString(strings.TrimRight(h.Script, "\n"))
	b.WriteString("\n")
	return b.String()
}

func (h Hook) validate() error {
	switch {
	case h.Name == "":
		return errors.New("a hook needs a name")
	case strings.ContainsAny(h.Name, "/ \t"):
		return fmt.Errorf("hook name %q may not contain spaces or slashes", h.Name)
	case h.Interface == "" && h.Action != "":
		return errors.New("an action needs an interface")
	case strings.TrimSpace(h.Script) == "":
		return errors.New("a hook needs a script")
	}
	return nil
}

// HookState is what the router currently holds for a hook.
type HookState struct {
	Present bool
	// Loader reports whether post-cfg.sh sources the provider's hook loader, without which
	// hooks do not survive a boot or configuration push.
	Loader bool
	// Installed reports whether a hotplug hook is in place for the current boot.
	Installed bool
	SHA256    string
	Script    string
}

// Extensions manages hooks on one router.
type Extensions struct {
	run    Runner
	layout Layout
}

// NewExtensions returns a manager using runner, defaulting to a Route10 layout.
func NewExtensions(runner Runner, layout Layout) *Extensions {
	if layout == (Layout{}) {
		layout = Route10Layout()
	}
	return &Extensions{run: runner, layout: layout}
}

// Put writes the hook, ensures the loader block exists, installs a hotplug hook for the
// current boot, and runs it once when asked.
func (e *Extensions) Put(ctx context.Context, h Hook) (HookState, error) {
	if err := h.validate(); err != nil {
		return HookState{}, err
	}
	h = h.normalise()
	out, err := e.run.Run(ctx, render("hook-put.sh", e.data(h)), []byte(h.body()))
	if err != nil {
		return HookState{}, fmt.Errorf("installing hook %q: %w", h.Name, err)
	}
	fields := parseKeyValues(out)
	return HookState{
		Present:   true,
		Loader:    fields["loader"] == "1",
		Installed: h.Interface != "",
		SHA256:    fields["sha256"],
		Script:    h.body(),
	}, nil
}

// Get reads what the router holds for the hook.
func (e *Extensions) Get(ctx context.Context, h Hook) (HookState, error) {
	h = h.normalise()
	out, err := e.run.Run(ctx, render("hook-get.sh", e.data(h)), nil)
	if err != nil {
		return HookState{}, fmt.Errorf("reading hook %q: %w", h.Name, err)
	}
	head, script, _ := strings.Cut(string(out), "--script--\n")
	fields := parseKeyValues([]byte(head))
	return HookState{
		Present:   fields["present"] == "1",
		Loader:    fields["loader"] == "1",
		Installed: fields["installed"] == "1",
		SHA256:    fields["sha256"],
		Script:    script,
	}, nil
}

// Delete removes the hook and runs destroy, which should undo whatever the hook asserted.
// Without a destroy script the hook's effect stays until the next boot.
func (e *Extensions) Delete(ctx context.Context, h Hook, destroy string) error {
	h = h.normalise()
	_, err := e.run.Run(ctx, render("hook-delete.sh", e.data(h)), []byte(destroy))
	if err != nil {
		return fmt.Errorf("removing hook %q: %w", h.Name, err)
	}
	return nil
}

type hookData struct {
	Layout
	Hook         Hook
	LoaderScript string
	SourceLine   string
}

func (e *Extensions) data(h Hook) hookData {
	return hookData{Layout: e.layout, Hook: h, LoaderScript: e.loaderScript(), SourceLine: e.layout.SourceLine()}
}
