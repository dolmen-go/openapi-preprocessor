#!/bin/sh
# Checks that the entries of GoReleaser's tar.gz archives are reproducible:
# owned by 0:0, with user and group names root or empty, mode 0644 or 0755
# (not depending on the umask), and dated <epoch>.
#
# Usage: .goreleaser/check-archives.sh <epoch> <archive.tar.gz>...
set -eu

epoch=${1:?usage: $0 <epoch> <archive.tar.gz>...}
shift
[ $# -gt 0 ] || { echo "usage: $0 <epoch> <archive.tar.gz>..." >&2; exit 2; }
want_time=$(TZ=UTC date -d "@$epoch" '+%Y-%m-%d %H:%M:%S')

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

listing=$(mktemp)
trap 'rm -f "$listing"' EXIT

for a in "$@"; do
	# Mode, numeric owner and mtime of every entry
	TZ=UTC tar --numeric-owner --full-time -tvzf "$a" > "$listing" || fail "$a: can't read the archive"
	[ -s "$listing" ] || fail "$a: empty archive"
	awk -v t="$want_time" '
		($1 != "-rw-r--r--" && $1 != "-rwxr-xr-x") || $2 != "0/0" || $4 " " $5 != t { print; bad = 1 }
		END { exit bad }
	' "$listing" || fail "$a: entries above are not 0644/0755, 0/0, dated $want_time UTC"
	# Owner names: root or empty (tar shows an empty name as its number)
	TZ=UTC tar --full-time -tvzf "$a" > "$listing" || fail "$a: can't read the archive"
	awk '
		$2 !~ /^(root|0)\/(root|0)$/ { print; bad = 1 }
		END { exit bad }
	' "$listing" || fail "$a: entries above have user/group names other than root"
	echo "ok - $a: entries 0644/0755, 0/0 (root or no names), dated $want_time UTC"
done
