package device

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// File is a file the provider keeps on the router: a script, a configuration fragment,
// anything the Alta cloud has no field for. Content lives in Terraform configuration and
// the router is made to match it.
type File struct {
	Path    string
	Content string
	// Mode is required, in octal ("0755"): omitting it must never be able to loosen a
	// permission by accident.
	Mode string
	// Backup keeps one copy of whatever was at Path before the provider first wrote it.
	Backup bool
}

// FileState is what the router currently holds at a path.
type FileState struct {
	Present bool
	SHA256  string
	Mode    string
	Content string
}

var (
	modePattern = regexp.MustCompile(`^0?[0-7]{3}$`)
	pathPattern = regexp.MustCompile(`^/[^\x00]+$`)
)

func (f File) validate() error {
	switch {
	case !pathPattern.MatchString(f.Path):
		return fmt.Errorf("path %q must be absolute", f.Path)
	case strings.HasSuffix(f.Path, "/"):
		return fmt.Errorf("path %q is a directory", f.Path)
	case !modePattern.MatchString(f.Mode):
		return fmt.Errorf("mode %q must be octal, e.g. 0755", f.Mode)
	}
	return nil
}

// PutFile makes the router's copy match f, writing atomically and preserving one backup
// of content the provider did not write.
func (e *Extensions) PutFile(ctx context.Context, f File) (FileState, error) {
	if err := f.validate(); err != nil {
		return FileState{}, err
	}
	out, err := e.run.Run(ctx, render("file-put.sh", fileData{Layout: e.layout, File: f}), []byte(f.Content))
	if err != nil {
		return FileState{}, fmt.Errorf("writing %s: %w", f.Path, err)
	}
	fields := parseKeyValues(out)
	return FileState{Present: true, SHA256: fields["sha256"], Mode: normaliseMode(fields["mode"]), Content: f.Content}, nil
}

// GetFile reads the router's copy.
func (e *Extensions) GetFile(ctx context.Context, f File) (FileState, error) {
	out, err := e.run.Run(ctx, render("file-get.sh", fileData{Layout: e.layout, File: f}), nil)
	if err != nil {
		return FileState{}, fmt.Errorf("reading %s: %w", f.Path, err)
	}
	head, content, _ := strings.Cut(string(out), "--content--\n")
	fields := parseKeyValues([]byte(head))
	if fields["present"] != "1" {
		return FileState{}, nil
	}
	return FileState{Present: true, SHA256: fields["sha256"], Mode: normaliseMode(fields["mode"]), Content: content}, nil
}

// DeleteFile removes the router's copy. Callers decide whether removing a file the
// router depends on is wise; the default elsewhere is to leave it.
func (e *Extensions) DeleteFile(ctx context.Context, f File) error {
	if err := f.validate(); err != nil {
		return err
	}
	if _, err := e.run.Run(ctx, render("file-delete.sh", fileData{Layout: e.layout, File: f}), nil); err != nil {
		return fmt.Errorf("removing %s: %w", f.Path, err)
	}
	return nil
}

// normaliseMode renders a mode the way configuration writes it.
func normaliseMode(mode string) string {
	if mode == "" {
		return ""
	}
	return fmt.Sprintf("0%03s", strings.TrimPrefix(mode, "0"))
}

type fileData struct {
	Layout
	File File
}

// ErrLoaderNotSourced says post-cfg.sh does not run the provider's hook loader, so hooks
// will not survive the next boot or configuration push.
var ErrLoaderNotSourced = errors.New("post-cfg.sh does not source the hook loader")

// SourceLine is the single line post-cfg.sh needs so that hooks survive a boot. It lives
// in whatever manages that file: the provider never edits post-cfg.sh itself.
func (l Layout) SourceLine() string {
	loader := l.HookDir + "/loader.sh"
	return fmt.Sprintf("[ -x %s ] && %s", loader, loader)
}

// loaderScript installs hotplug hooks and runs boot hooks. The provider owns this file
// outright, so nothing has to parse or merge anyone else's text.
func (e *Extensions) loaderScript() string {
	return fmt.Sprintf(`#!/bin/sh
# Managed by terraform-provider-alta. Edits here are overwritten.
# Runs from post-cfg.sh after every boot and configuration push.
for f in %s/hotplug/*; do
  [ -r "$f" ] || continue
  { cp "$f" %s/ && chmod 755 %s/"$(basename "$f")"; } 2>&1 | logger -t alta-hooks
done
for f in %s/boot/*.sh; do
  [ -r "$f" ] || continue
  sh "$f" 2>&1 | logger -t alta-hooks
done
`, e.layout.HookDir, e.layout.HotplugDir, e.layout.HotplugDir, e.layout.HookDir)
}
