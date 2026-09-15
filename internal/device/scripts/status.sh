D={{q .Dir}}
state=clean
[ -f "$D/state" ] && read -r state _ < "$D/state"
printf 'config_md5=%s\n' "$(md5sum {{q .ConfigFile}} | cut -d' ' -f1)"
printf 'applied_hash=%s\n' "$(cat {{q .HashFile}})"
printf 'state=%s\n' "$state"
if ( {{.AgentRunning}} ); then echo agent=running; else echo agent=stopped; fi
if [ -f "$D/timer.pid" ] && kill -0 "$(cat "$D/timer.pid")" 2>/dev/null; then echo timer=armed; else echo timer=idle; fi
echo '--journal--'
if [ -f "$D/journal.json" ]; then cat "$D/journal.json"; fi
