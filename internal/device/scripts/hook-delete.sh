set -eu
H={{q .HookDir}}
{{if .Hook.Interface}}target="$H/hotplug/{{.Hook.FileName}}"{{else}}target="$H/boot/{{.Hook.FileName}}"{{end}}
{{if .Hook.Interface}}rm -f {{q .HotplugDir}}/{{.Hook.FileName}}{{end}}
rm -f "$target"
destroy=$(cat)
if [ -n "$destroy" ]; then printf '%s' "$destroy" | sh >/dev/null 2>&1 || true; fi
