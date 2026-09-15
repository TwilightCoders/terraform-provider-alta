set -eu
D={{q .Dir}}
sh "$D/rollback.sh"
read -r state < "$D/state"
if [ "$state" != rolled-back ]; then echo "rollback did not run (state $state)" >&2; exit 3; fi
