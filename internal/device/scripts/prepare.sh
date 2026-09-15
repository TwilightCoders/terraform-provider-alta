set -eu
D={{q .Dir}}
if [ -f "$D/timer.pid" ] && kill -0 "$(cat "$D/timer.pid")" 2>/dev/null; then
  echo "a rollback timer from an earlier transaction is still armed" >&2; exit 3
fi
mkdir -p "$D/snap"
cp -p {{q .ConfigFile}} "$D/snap/config.json"
cp -p {{q .HashFile}} "$D/snap/hash.txt"
cat > "$D/journal.json.tmp"
mv "$D/journal.json.tmp" "$D/journal.json"
cat > "$D/rollback.sh" <<'ROLLBACK'
{{template "rollback.sh" .}}
ROLLBACK
echo gated > "$D/state"
( {{.AgentStop}} ) >/dev/null 2>&1 || true
i=0
while ( {{.AgentRunning}} ); do
  i=$((i + 1))
  if [ "$i" -gt 20 ]; then echo "the cloud agent did not stop" >&2; exit 4; fi
  sleep 1
done
{{.Log}} "gated: cloud agent paused for a transaction"
