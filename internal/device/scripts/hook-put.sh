set -eu
# The router's shell is busybox: it has no `install`, and a hook that relies on a missing
# binary fails at boot where nobody sees it. Check before writing anything.
for cmd in cp chmod mkdir sha256sum logger{{range .Hook.Requires}} {{q .}}{{end}}; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "the router has no $cmd" >&2; exit 3; }
done
H={{q .HookDir}}
name={{q .Hook.Name}}
mkdir -p "$H/hotplug" "$H/boot"
{{if .Hook.Interface}}target="$H/hotplug/{{.Hook.FileName}}"{{else}}target="$H/boot/{{.Hook.FileName}}"{{end}}
cat > "$target.tmp"
chmod 755 "$target.tmp"
mv "$target.tmp" "$target"

# The loader is a file this provider owns. post-cfg.sh only has to source it, which is
# one line a human keeps in their own copy — no generated block, nothing to parse.
cat > "$H/loader.sh.new" <<'LOADER'
{{.LoaderScript}}
LOADER
chmod 755 "$H/loader.sh.new"
mv "$H/loader.sh.new" "$H/loader.sh"

if grep -qF {{q .SourceLine}} {{q .PostCfg}} 2>/dev/null; then echo sourced=1; else echo sourced=0; fi

{{if .Hook.Interface}}
cp "$target" {{q .HotplugDir}}/{{.Hook.FileName}}
chmod 755 {{q .HotplugDir}}/{{.Hook.FileName}}
{{end}}
{{if .Hook.Run}}
# Run it the way its event would, so a hotplug hook's own guards let it through.
{{if .Hook.Interface}}ACTION={{q .Hook.Action}} INTERFACE={{q .Hook.Interface}} sh "$target" >/dev/null 2>&1 || true
{{else}}sh "$target" >/dev/null 2>&1 || true
{{end}}{{end}}
printf 'sha256=%s\n' "$(sha256sum "$target" | cut -d' ' -f1)"
