set -eu
D={{q .Dir}}
read -r state _ < "$D/state"
if [ "$state" != committed ]; then echo "cannot confirm from state $state" >&2; exit 3; fi
echo confirmed > "$D/state"
i=0
while [ -f "$D/timer.pid" ] && kill -0 "$(cat "$D/timer.pid")" 2>/dev/null; do
  i=$((i + 1))
  if [ "$i" -gt 10 ]; then echo "the rollback timer did not stand down" >&2; exit 4; fi
  sleep 1
done
{{.Log}} "confirmed: transaction complete"
