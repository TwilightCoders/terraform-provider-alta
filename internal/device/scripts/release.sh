set -eu
D={{q .Dir}}
state=clean
[ -f "$D/state" ] && read -r state _ < "$D/state"
if [ "$state" = committed ]; then echo "a committed transaction must be confirmed or rolled back first" >&2; exit 3; fi
( {{.AgentStart}} ) >/dev/null 2>&1 || true
i=0
until ( {{.AgentRunning}} ); do
  i=$((i + 1))
  if [ "$i" -gt 20 ]; then echo "the cloud agent did not start" >&2; exit 4; fi
  sleep 1
done
echo released > "$D/state"
{{.Log}} "released: cloud agent resumed"
