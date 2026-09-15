report() {
  if out=$("$@" 2>&1); then printf '%s\tok\t\n' "$name"
  else printf '%s\tfail\t%s\n' "$name" "$(printf '%s' "$out" | tail -n 1)"; fi
}
resolves() { nslookup "$1" "$2" 2>&1 | grep -q '^Address [0-9][0-9]*: '; }
{{if .WANTarget}}name=wan; report ping -c 2 -W 2 {{q .WANTarget}}
{{end}}{{if .DNSName}}name=dns; report resolves {{q .DNSName}} {{q .DNSServer}}
{{end}}{{range .LANTargets}}name={{q (printf "lan:%s" .)}}; report ping -c 2 -W 2 {{q .}}
{{end}}
