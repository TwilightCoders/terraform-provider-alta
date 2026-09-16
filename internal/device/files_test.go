package device

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fileRouter(t *testing.T) (*Extensions, string) {
	t.Helper()
	layout := Route10Layout()
	if runtime.GOOS == "darwin" {
		layout.StatMode = "stat -f %Lp" // the router's busybox uses stat -c
	}
	return NewExtensions(localRunner{path: os.Getenv("PATH")}, layout), t.TempDir()
}

func TestFileIsWrittenWithItsMode(t *testing.T) {
	ext, root := fileRouter(t)
	f := File{Path: filepath.Join(root, "sub", "script.sh"), Content: "#!/bin/sh\ntrue\n", Mode: "0755", Backup: true}

	state, err := ext.PutFile(context.Background(), f)
	must(t, err)
	if !state.Present || state.Mode != "0755" || state.SHA256 == "" {
		t.Fatalf("state = %+v", state)
	}
	info, err := os.Stat(f.Path)
	must(t, err)
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode on disk = %v", info.Mode().Perm())
	}

	got, err := ext.GetFile(context.Background(), f)
	must(t, err)
	if got.Content != f.Content || got.SHA256 != state.SHA256 || got.Mode != "0755" {
		t.Errorf("read back %+v", got)
	}
}

func TestFileAdoptionKeepsOneBackup(t *testing.T) {
	ext, root := fileRouter(t)
	path := filepath.Join(root, "post-cfg.sh")
	must(t, os.WriteFile(path, []byte("original\n"), 0o755))
	f := File{Path: path, Content: "managed\n", Mode: "0755", Backup: true}

	_, err := ext.PutFile(context.Background(), f)
	must(t, err)
	backup, err := os.ReadFile(path + ".bak-alta")
	must(t, err)
	if string(backup) != "original\n" {
		t.Errorf("backup = %q", backup)
	}

	f.Content = "managed again\n"
	_, err = ext.PutFile(context.Background(), f)
	must(t, err)
	if again, _ := os.ReadFile(path + ".bak-alta"); string(again) != "original\n" {
		t.Errorf("the backup was overwritten with %q", again)
	}
}

func TestFilePutOnlyChangesWhatItMust(t *testing.T) {
	ext, root := fileRouter(t)
	path := filepath.Join(root, "vpn.conf")
	f := File{Path: path, Content: "WG_IFACE=wg0\n", Mode: "0644"}
	first, err := ext.PutFile(context.Background(), f)
	must(t, err)
	before, err := os.Stat(path)
	must(t, err)

	second, err := ext.PutFile(context.Background(), f)
	must(t, err)
	after, err := os.Stat(path)
	must(t, err)
	if first.SHA256 != second.SHA256 {
		t.Error("hash changed for identical content")
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("the file was rewritten despite identical content")
	}

	// A mode-only change is applied without touching content.
	f.Mode = "0600"
	state, err := ext.PutFile(context.Background(), f)
	must(t, err)
	if state.Mode != "0600" || state.SHA256 != first.SHA256 {
		t.Errorf("state = %+v", state)
	}
}

func TestFileReadsMissingAndDeletes(t *testing.T) {
	ext, root := fileRouter(t)
	f := File{Path: filepath.Join(root, "gone.conf"), Content: "x\n", Mode: "0644"}

	missing, err := ext.GetFile(context.Background(), f)
	must(t, err)
	if missing.Present {
		t.Fatal("reported present before being written")
	}
	_, err = ext.PutFile(context.Background(), f)
	must(t, err)
	must(t, ext.DeleteFile(context.Background(), f))
	if _, err := os.Stat(f.Path); !os.IsNotExist(err) {
		t.Error("the file survived deletion")
	}
}

func TestFileValidation(t *testing.T) {
	ext, root := fileRouter(t)
	for name, f := range map[string]File{
		"relative path": {Path: "cfg/x", Content: "x", Mode: "0644"},
		"directory":     {Path: root + "/", Content: "x", Mode: "0644"},
		"bad mode":      {Path: root + "/x", Content: "x", Mode: "rwx"},
		"missing mode":  {Path: root + "/x", Content: "x"},
	} {
		if _, err := ext.PutFile(context.Background(), f); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestSourceLineNamesTheLoader(t *testing.T) {
	line := Route10Layout().SourceLine()
	if !strings.Contains(line, "/cfg/tf.d/loader.sh") || !strings.HasPrefix(line, "[ -x ") {
		t.Errorf("SourceLine() = %q", line)
	}
}
