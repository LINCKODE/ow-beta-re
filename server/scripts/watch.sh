#!/usr/bin/env bash
# Runs bin/<name> (built from ./cmd/<name>) and rebuilds + restarts it whenever
#   - a .go file in one of its packages changes (its module-local dependencies and the local cgo wrapper
#     modules under ../systems, so a change to the game server doesn't restart lobbylogic),
#   - a C++ archive it links changes (../systems/*/build/*.a, rebuilt by `make libs`; only for binaries
#     with cgo dependencies),
#   - its args file changes (server/<name>.args: extra flags, read on every start; `#` starts a comment,
#     shell quoting works),
# and restarts it when it exits on its own (a crash, a bad flag), backing off up to 30 s while it keeps
# dying. If a build fails, the previous binary keeps running. Extra args go to the binary before the ones
# from the args file (so the file can override them).
#   scripts/watch.sh lobbylogic
#   scripts/watch.sh instance --debug
set -u
cd "$(dirname "$0")/.."

name=${1:?usage: scripts/watch.sh <cmd name> [args...]}
shift
bin=bin/$name
argsfile=$name.args
pid=""
backoff=1

# The packages' directories are listed again on every check, so new imports are picked up: the main module's
# packages and those of modules replaced by a local path (the cgo wrappers).
dirs() {
	go list -deps -f '{{if not .Standard}}{{with .Module}}{{if .Main}}{{$.Dir}}{{else}}{{with .Replace}}{{if not .Version}}{{$.Dir}}{{end}}{{end}}{{end}}{{end}}{{end}}' "./cmd/$name" 2>/dev/null
}

# uses_cgo: whether one of the binary's local packages has cgo files (then the C++ archives count).
uses_cgo() {
	[[ -n $(go list -deps -f '{{if not .Standard}}{{if .CgoFiles}}x{{end}}{{end}}' "./cmd/$name" 2>/dev/null) ]]
}

archives() {
	ls ../systems/*/build/*.a 2>/dev/null
}

fingerprint() {
	local d
	{
		for d in $(dirs); do
			find "$d" -maxdepth 1 -name '*.go' -printf '%T@ %p\n'
		done
		[[ -n $cgo ]] && archives | xargs -r stat -c '%Y %s %n'
		[[ -f $argsfile ]] && stat -c '%Y %s %n' "$argsfile"
	} | sort | md5sum
}

# fileargs: the args file's words, comments dropped (bash quoting rules).
fileargs() {
	extra=()
	[[ -f $argsfile ]] || return 0
	local text
	text=$(sed 's/#.*//' "$argsfile")
	eval "extra=( $text )"
}

stop() {
	if [[ -n $pid ]] && kill -0 "$pid" 2>/dev/null; then
		kill -TERM "$pid" 2>/dev/null
		wait "$pid" 2>/dev/null
	fi
	pid=""
}

start() {
	fileargs
	echo "watch: starting $name $* ${extra[*]}" >&2
	"$bin" "$@" "${extra[@]}" &
	pid=$!
	started=$SECONDS
}

build_and_start() {
	# Stamp a hash of the C++ archives the cgo wrappers link (see Makefile LIBS_HASH): Go does not relink when
	# only an archive changed.
	libs=$(cat $(archives) 2>/dev/null | md5sum | cut -c1-12)
	if go build -ldflags "-X main.libs=$libs" -o "$bin.new" "./cmd/$name"; then
		stop
		mv "$bin.new" "$bin"
		start "$@"
		backoff=1
	else
		echo "watch: build failed; keeping the running $name" >&2
		[[ -z $pid && -x $bin ]] && start "$@"
	fi
}

trap 'stop; exit 0' INT TERM
trap stop EXIT

cgo=""
uses_cgo && cgo=1
last=$(fingerprint)
build_and_start "$@"
while true; do
	sleep 1
	now=$(fingerprint)
	if [[ $now != "$last" ]]; then
		sleep 0.3 # let editors finish writing
		last=$(fingerprint)
		echo "watch: change detected, rebuilding $name" >&2
		build_and_start "$@"
		continue
	fi
	if [[ -n $pid ]] && ! kill -0 "$pid" 2>/dev/null; then
		wait "$pid" 2>/dev/null
		status=$?
		pid=""
		# A process that ran for a while gets restarted at once; one that keeps dying waits longer each time.
		if ((SECONDS - started > 30)); then
			backoff=1
		fi
		echo "watch: $name exited (status $status); restarting in ${backoff}s" >&2
		sleep "$backoff"
		backoff=$((backoff * 2 > 30 ? 30 : backoff * 2))
		start "$@"
	fi
done
