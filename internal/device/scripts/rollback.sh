#!/bin/sh
D={{q .Dir}}
state=
read -r state < "$D/state" 2>/dev/null || exit 0
[ "$state" = committed ] || exit 0
echo rolled-back > "$D/state"
{{.Log}} "rollback: restoring the pre-transaction configuration"
( {{.AgentStop}} ) >/dev/null 2>&1 || true
cp -p "$D/snap/config.json" {{q .ConfigFile}}
cp -p "$D/snap/hash.txt" {{q .HashFile}}
( {{.Apply}} ) > "$D/rollback.log" 2>&1 || true
{{.Log}} "rollback: complete; the cloud agent stays paused until the cloud is repaired"
