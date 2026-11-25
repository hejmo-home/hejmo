#!/usr/bin/env bash
# Runs one shard of the Go tests (ticket 228). internal/server holds nine tenths
# of the test time, so its tests are split in two by a hash of the test name;
# the third shard runs every other package. Together the shards run each test
# once. Usage: ci/test-shard.sh SHARD [go test flags...], SHARD is 1, 2 or 3.
# With HEJMO_SHARD_LIST=1 it prints what the shard would run instead.
set -euo pipefail

shard=${1:?usage: test-shard.sh SHARD [go test flags]}
shift

server=./internal/server

# The two halves of the rendered layout check each take one shard (ticket 347):
# the only shards with Chrome are 1 and 2, so they are pinned there, not left to
# the hash. Everything else goes by the hash of its name.
pinned_shard() {
	case "$1" in
	TestRenderedLayout) echo 0 ;;
	TestRenderedLayoutPart2) echo 1 ;;
	*) echo $(( $(printf '%s' "$1" | cksum | cut -d' ' -f1) % 2 )) ;;
	esac
}

# Top-level tests and examples of the server package, whose name picks the shard.
server_tests() {
	local want=$1 name
	go test -list '^(Test|Example)' "$server" | grep -E '^(Test|Example)' | while read -r name; do
		if [ "$(pinned_shard "$name")" -eq "$want" ]; then
			echo "$name"
		fi
	done
}

case "$shard" in
1 | 2)
	names=$(server_tests $(( shard - 1 )) | paste -sd'|' -)
	if [ -n "${HEJMO_SHARD_LIST:-}" ]; then
		echo "$names" | tr '|' '\n'
		exit 0
	fi
	exec go test "$@" -run "^($names)\$" "$server"
	;;
3)
	pkgs=$(go list ./... | grep -v '/internal/server$')
	if [ -n "${HEJMO_SHARD_LIST:-}" ]; then
		echo "$pkgs"
		exit 0
	fi
	# shellcheck disable=SC2086 # one word per package
	exec go test "$@" $pkgs
	;;
*)
	echo "shard must be 1, 2 or 3" >&2
	exit 2
	;;
esac
