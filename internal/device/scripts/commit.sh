set -eu
D={{q .Dir}}
read -r state _ < "$D/state"
if [ "$state" != gated ]; then echo "cannot commit from state $state" >&2; exit 3; fi
old=$(cat {{q .HashFile}})
echo committed > "$D/state"
{{.Detach}} sh -c '
  echo $$ > "$1/timer.pid"
  i=0
  while [ "$i" -lt "$2" ]; do
    read -r s _ < "$1/state"
    if [ "$s" != committed ]; then rm -f "$1/timer.pid"; exit 0; fi
    sleep 1
    i=$((i + 1))
  done
  sh "$1/rollback.sh"
  rm -f "$1/timer.pid"
' alta-tf-timer "$D" {{.WindowSeconds}} </dev/null >/dev/null 2>&1 &
( {{.AgentStart}} ) >/dev/null 2>&1
{{.Log}} "committed: rollback armed for {{.WindowSeconds}}s, cloud agent resumed"
i=0
while [ "$i" -lt {{.PushTimeoutSeconds}} ]; do
  if [ "$(cat {{q .HashFile}})" != "$old" ]; then echo pushed=1; exit 0; fi
  sleep 1
  i=$((i + 1))
done
echo pushed=0
