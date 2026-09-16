set -eu
H={{q .HookDir}}
name={{q .Hook.Name}}
mkdir -p "$H/hotplug" "$H/boot"
{{if .Hook.Interface}}target="$H/hotplug/{{.Hook.FileName}}"{{else}}target="$H/boot/{{.Hook.FileName}}"{{end}}
cat > "$target.tmp"
chmod 755 "$target.tmp"
mv "$target.tmp" "$target"

# The loader lives in post-cfg.sh because /etc is rebuilt on every boot and every config
# push. It installs hotplug hooks and runs boot hooks, and is idempotent.
P={{q .PostCfg}}
if ! grep -q {{q .LoaderMarker}} "$P" 2>/dev/null; then
  cp "$P" "$P.bak-alta-$(date +%Y%m%d-%H%M%S)"
  # Keep everything except a trailing "exit 0", append the block, then restore it:
  # nothing after that line ever runs.
  lines=$(wc -l < "$P")
  if [ "$(tail -n 1 "$P")" = "exit 0" ]; then
    sed -n "1,$((lines - 1))p" "$P" > "$P.next"
  else
    cat "$P" > "$P.next"
  fi
  cat >> "$P.next" <<'LOADER'
{{.LoaderBlock}}
LOADER
  echo "exit 0" >> "$P.next"
  chmod 755 "$P.next"
  mv "$P.next" "$P"
fi

{{if .Hook.Interface}}
install -m 755 "$target" {{q .HotplugDir}}/{{.Hook.FileName}}
{{end}}
{{if .Hook.Run}}
# Run it the way its event would, so a hotplug hook's own guards let it through.
{{if .Hook.Interface}}ACTION={{q .Hook.Action}} INTERFACE={{q .Hook.Interface}} sh "$target" >/dev/null 2>&1 || true
{{else}}sh "$target" >/dev/null 2>&1 || true
{{end}}{{end}}
printf 'sha256=%s\n' "$(sha256sum "$target" | cut -d' ' -f1)"
