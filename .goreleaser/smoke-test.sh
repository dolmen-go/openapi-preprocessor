#!/bin/sh
# Smoke tests for the openapi-preprocessor container image built by GoReleaser.
# The image is run the way the README documents it.
#
# Usage: .goreleaser/smoke-test.sh <image> [<expected -version output>]
#
# Environment:
#   CONTAINER_ENGINE  docker (default) or podman
#   PLATFORM          if set, passed as --platform to 'run' (ex: linux/arm64)
#   MOUNT_OPTS        options of the testdata mounts (default: ro; ro,Z on SELinux hosts)
set -eu

image=${1:?usage: $0 <image> [<expected -version output>]}
expected_version=${2:-}
engine=${CONTAINER_ENGINE:-docker}
mount_opts=${MOUNT_OPTS:-ro}
platform_opt=${PLATFORM:+--platform=$PLATFORM}
testdata=$(cd "$(dirname "$0")/../testdata" && pwd)

# Options to run as the current user, to read files only readable by their owner
owner_opt="--user=$(id -u):$(id -g)"
# Rootless Podman: also map the current user to the same UID in the container
[ "$engine" != podman ] || owner_opt="--userns=keep-id $owner_opt"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

# Image hardening: unprivileged user, nothing writable by that user.
# Checked on a container created for $PLATFORM: with a multi-platform image,
# 'image inspect' would show the host's platform.
# shellcheck disable=SC2086
cid=$("$engine" create $platform_opt "$image")
user=$("$engine" container inspect --format '{{.Config.User}}' "$cid")
"$engine" export "$cid" | tar --numeric-owner -tvf - > "$tmp/files"
"$engine" rm "$cid" > /dev/null

[ "$user" = 65532:65532 ] || fail "image user: got '$user', want '65532:65532'"
echo "ok - image user is 65532:65532"

# entry <path>: print the line of <path> in the list of the image files
entry() {
	awk -v path="$1" '{ p = $NF; sub(/^\.\//, "", p); sub(/\/$/, "", p) } p == path' "$tmp/files"
}
# check_entry <path> <mode>: the image has <path>, owned by 0:0, with <mode>
check_entry() {
	case "$(entry "$1")" in
	"$2 0/0 "*) ;;
	*) fail "image file $1: want $2 0/0, got: '$(entry "$1")'" ;;
	esac
}
check_entry usr/local/bin/openapi-preprocessor -r-xr-xr-x
# Docker (BuildKit) creates /work in the image, Podman (Buildah) only at run time
[ -z "$(entry work)" ] || check_entry work drwxr-xr-x
if awk '$2 ~ /^65532\// || ($1 !~ /^l/ && substr($1, 9, 1) == "w")' "$tmp/files" | grep .; then
	fail "image files above are writable by the image's user"
fi
echo "ok - image files are read-only for the image's user"

# run_tool <dir> <arg>...: run the image with <dir> mounted on /work.
# $extra_opts holds additional options for the container engine.
extra_opts=
run_tool() {
	dir=$1
	shift
	# shellcheck disable=SC2086 # $platform_opt and $extra_opts are lists of options
	"$engine" run --rm --read-only --network none $platform_opt $extra_opts \
		-v "$dir:/work:$mount_opts" "$image" "$@"
}

# check_case <dir> <case>: process <dir>/<case>/input.yml and compare
# with testdata/<case>/result.json
check_case() {
	run_tool "$1" "$2/input.yml" > "$tmp/out.json" || fail "$2: exit status $?"
	jq -S . "$testdata/$2/result.json" > "$tmp/want.json"
	jq -S . "$tmp/out.json" > "$tmp/got.json" || fail "$2: output is not JSON"
	diff -u "$tmp/want.json" "$tmp/got.json" || fail "$2: unexpected output"
}

# Processing, with relative refs (11) and refs to ../common (10)
for c in 10-ref-ext 11-ref-relative; do
	check_case "$testdata" "$c"
	echo "ok - $c"
done

# A ref to a file outside of the mount fails cleanly
if run_tool "$testdata/10-ref-ext" input.yml > "$tmp/out" 2> "$tmp/err"; then
	fail "ref outside of the mount: unexpected success"
fi
[ ! -s "$tmp/out" ] || fail "ref outside of the mount: unexpected output on stdout"
grep -q "can't load" "$tmp/err" || fail "ref outside of the mount: unexpected error: $(cat "$tmp/err")"
echo "ok - ref outside of the mount fails"

# Files readable only by their owner: not readable by the image's user...
mkdir "$tmp/private"
cp -R "$testdata/10-ref-ext" "$testdata/common" "$tmp/private/"
find "$tmp/private" -type d -exec chmod 700 {} +
find "$tmp/private" -type f -exec chmod 600 {} +
if run_tool "$tmp/private" 10-ref-ext/input.yml > /dev/null 2>&1; then
	fail "private files: readable by the image's default user"
fi
echo "ok - private files not readable by the default user"
# ...but readable with the workaround documented in the README
extra_opts=$owner_opt
check_case "$tmp/private" 10-ref-ext
extra_opts=
echo "ok - private files readable with $owner_opt"

# -version
# shellcheck disable=SC2086
got_version=$("$engine" run --rm $platform_opt "$image" -version) || fail "-version: exit status $?"
[ -n "$got_version" ] || fail "-version: empty output"
if [ -n "$expected_version" ] && [ "$got_version" != "$expected_version" ]; then
	fail "-version: got '$got_version', want '$expected_version'"
fi
echo "ok - -version: $got_version"
