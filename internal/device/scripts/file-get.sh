p={{q .File.Path}}
if [ ! -e "$p" ]; then echo present=0; exit 0; fi
echo present=1
printf 'sha256=%s\n' "$(sha256sum "$p" | cut -d' ' -f1)"
printf 'mode=%s\n' "$({{.StatMode}} "$p")"
echo '--content--'
cat "$p"
