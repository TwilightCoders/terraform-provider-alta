set -eu
p={{q .File.Path}}
mkdir -p "$(dirname "$p")"
cat > "$p.alta-new"
chmod {{q .File.Mode}} "$p.alta-new"

if [ -e "$p" ] && [ "$(sha256sum "$p" | cut -d' ' -f1)" = "$(sha256sum "$p.alta-new" | cut -d' ' -f1)" ]; then
  rm -f "$p.alta-new"
  chmod {{q .File.Mode}} "$p"
else
  # Keep one copy of whatever was here before this resource first wrote it.
  {{if .File.Backup}}if [ -e "$p" ] && [ ! -e "$p.bak-alta" ]; then cp "$p" "$p.bak-alta"; fi{{end}}
  mv "$p.alta-new" "$p"
fi
printf 'sha256=%s\n' "$(sha256sum "$p" | cut -d' ' -f1)"
printf 'mode=%s\n' "$({{.StatMode}} "$p")"
