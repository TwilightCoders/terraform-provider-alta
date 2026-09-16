H={{q .HookDir}}
{{if .Hook.Interface}}target="$H/hotplug/{{.Hook.FileName}}"{{else}}target="$H/boot/{{.Hook.FileName}}"{{end}}
if [ ! -r "$target" ]; then echo present=0; exit 0; fi
echo present=1
printf 'sha256=%s\n' "$(sha256sum "$target" | cut -d' ' -f1)"
if [ -x {{q .HookDir}}/loader.sh ]; then echo loader=1; else echo loader=0; fi
if grep -qF {{q .SourceLine}} {{q .PostCfg}} 2>/dev/null; then echo sourced=1; else echo sourced=0; fi
{{if .Hook.Interface}}
if [ -r {{q .HotplugDir}}/{{.Hook.FileName}} ]; then echo installed=1; else echo installed=0; fi
{{end}}
echo '--script--'
cat "$target"
