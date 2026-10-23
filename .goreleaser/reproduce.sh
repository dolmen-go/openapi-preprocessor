#!/bin/sh
# Reproduces locally the release <tag> made by GoReleaser in CI: GoReleaser
# builds the archives and publishes the images to a local registry, then the
# script checks:
# - unless -n, that the archives are identical to those of the GitHub Release;
# - that the archive entries are owned by 0:0 and dated at the commit time;
# - unless -n, that the image index is identical to the one on GHCR (with -n,
#   its digest is printed);
# - that the binary in each platform image is the binary of the archive.
#
# Usage: .goreleaser/reproduce.sh [-n] <tag>
#   -n  don't compare with the GitHub Release and GHCR (for a tag that has none)
#
# Nothing is installed: run it with the Go and GoReleaser versions shown in
# the CI logs of the release, e.g.:
#   mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh v1.0.0-rc.3
# Requires: git, Go, GoReleaser, jq, curl, GNU tar, and network access (Go
# module proxy for crane, GHCR for the base and the published images, GitHub
# for the checksums). No Docker, no Podman.
set -eu

# Not read from GoReleaser's outputs: the GitHub repository (GoReleaser needs
# it as the remote URL before it runs)
github=dolmen-go/openapi-preprocessor
# Also in base.sh and ../.github/workflows/release.yml: the
# go-containerregistry version GoReleaser embeds
crane_version=v0.22.1

compare=1
opt_n=
if [ "${1:-}" = -n ]; then
	compare=0
	opt_n="-n "
	shift
fi
tag=${1:?usage: $0 [-n] <tag>}
here=$(cd "$(dirname "$0")" && pwd)
repo=$(git -C "$here" rev-parse --show-toplevel)

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

tmp=$(mktemp -d)
registry_pid=
cleanup() {
	[ -z "$registry_pid" ] || kill "$registry_pid" 2> /dev/null || true
	rm -rf "$tmp"
}
trap cleanup EXIT
# dash runs no EXIT trap when a signal kills it (e.g. Ctrl-C, or the reader
# of the output exiting early): exit instead, with the status of a death by
# that signal
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 141' PIPE
trap 'exit 143' TERM

# Go settings not set in CI, which would change the binaries: from the
# environment, and from the user's go env file (go env -w). ko lets the
# environment override its own target settings. Use the Go on PATH (the
# version of the CI logs), never a toolchain switch.
unset GOFLAGS GOEXPERIMENT GOFIPS140 GOOS GOARCH GOAMD64 GOARM64
export GOENV=off GOTOOLCHAIN=local
# No token: none is needed (release.disable), and a stale GITHUB_TOKEN would
# be sent to GHCR for the public base image, which GHCR refuses
unset GITHUB_TOKEN GITLAB_TOKEN GITEA_TOKEN
# ko writes the image creation time in the local time zone; CI runs in UTC
export TZ=UTC

echo "# $(go version)"
echo "# goreleaser $(goreleaser --version 2>&1 | sed -n 's/^GitVersion: *//p')"

# A private repository holding only the tag and its history, as in CI:
# untracked files of the working copy would make Go stamp +dirty into the
# version, and another tag on the same commit would change the version Go
# and GoReleaser pick. The user's git configuration must not change the
# checkout either.
git init --quiet "$tmp/src"
cd "$tmp/src"
git config core.autocrlf false
git config core.attributesFile /dev/null
git config log.showSignature false
git fetch --quiet --no-tags "$repo" "refs/tags/$tag:refs/tags/$tag" || fail "tag $tag not found in $repo"
git checkout --quiet --detach "refs/tags/$tag"
# GoReleaser needs the GitHub repository (nothing is published there)
git remote add origin "https://github.com/$github.git"
epoch=$(git show -s --format=%ct HEAD)

# Tags released with dockers_v2: KO_DOCKER_REPO would not apply, GoReleaser
# would push the images to GHCR (and, with a GITHUB_TOKEN, change the GitHub
# Release). Their own reproduce.sh handles them.
grep -q '^kos:' .goreleaser.yaml || {
	echo "$tag builds its images without ko: run the reproduce.sh of $tag instead:" >&2
	echo "  git clone --branch $tag $repo <directory>" >&2
	echo "  <directory>/.goreleaser/reproduce.sh $opt_n$tag" >&2
	fail "$tag: no kos in .goreleaser.yaml"
}

# crane, built once: with 'go run', stopping the registry would stop 'go run'
# but not the registry it started
GOBIN="$tmp/bin" go install "github.com/google/go-containerregistry/cmd/crane@$crane_version" ||
	fail "can't install crane $crane_version"
crane=$tmp/bin/crane
echo "# crane $crane_version"

# A local registry, in memory, on a free port
"$crane" registry serve --address 127.0.0.1:0 > "$tmp/registry.log" 2>&1 &
registry_pid=$!
port=
tries=0
while [ -z "$port" ]; do
	kill -0 "$registry_pid" 2> /dev/null || {
		cat "$tmp/registry.log" >&2
		fail "the local registry stopped"
	}
	tries=$((tries + 1))
	[ "$tries" -le 100 ] || fail "the local registry did not start in 10 s"
	sleep 0.1
	port=$(sed -n 's/.*serving on port \([0-9]*\).*/\1/p' "$tmp/registry.log")
done
local_repo=127.0.0.1:$port/openapi-preprocessor
echo "# registry 127.0.0.1:$port"

# Release mode, as in CI: the archives in dist/, the images pushed to the
# local registry, no GitHub Release (release.disable in .goreleaser.yaml)
KO_DOCKER_REPO=$local_repo GORELEASER_CURRENT_TAG=$tag \
	goreleaser release --clean --skip=announce,sbom > "$tmp/goreleaser.log" 2>&1 || {
	tail -n 20 "$tmp/goreleaser.log" >&2
	fail "goreleaser release"
}
[ "$(jq -r .tag dist/metadata.json)" = "$tag" ] || fail "goreleaser built $(jq -r .tag dist/metadata.json), not $tag"
echo "ok - goreleaser built the release $tag"

# What GoReleaser built, read from dist/artifacts.json: one checksum line per
# archive (any format), as in its checksums file, sorted by name
jq -r '.[] | select(.type == "Archive") | "\(.extra.Checksum | ltrimstr("sha256:"))  \(.name)"' dist/artifacts.json |
	sort -k2 > "$tmp/local-archives.txt"
[ -s "$tmp/local-archives.txt" ] || fail "no archives in dist/artifacts.json"
if [ "$compare" = 1 ]; then
	checksums=$(jq -r '.[] | select(.type == "Checksum") | .name' dist/artifacts.json)
	[ -n "$checksums" ] || fail "no checksums file in dist/artifacts.json"
	url=https://github.com/$github/releases/download/$tag/$checksums
	curl -fsSL -o "$tmp/published.txt" "$url" || fail "can't download $url (no published release? use -n)"
	# The published file lists the archives and their SBOMs (<archive>.sbom.json,
	# not built locally). Anything else was published but not built locally.
	awk 'NR == FNR { local[$2] = 1; next }
		!($2 in local) && !($2 ~ /\.sbom\.json$/ && substr($2, 1, length($2) - 10) in local)' \
		"$tmp/local-archives.txt" "$tmp/published.txt" > "$tmp/not-built.txt"
	[ ! -s "$tmp/not-built.txt" ] || {
		cat "$tmp/not-built.txt" >&2
		fail "published files above were not built locally"
	}
	# The published lines of the archives built locally; a missing one shows in the diff
	awk 'NR == FNR { local[$2] = 1; next } $2 in local' "$tmp/local-archives.txt" "$tmp/published.txt" |
		sort -k2 > "$tmp/published-archives.txt"
	diff -u "$tmp/published-archives.txt" "$tmp/local-archives.txt" || fail "archives differ from the GitHub Release"
	echo "ok - $(wc -l < "$tmp/local-archives.txt") archives identical to the GitHub Release"
else
	cat "$tmp/local-archives.txt"
fi

# shellcheck disable=SC2046 # one path per archive, without spaces
"$here/check-archives.sh" "$epoch" $(jq -r '.[] | select(.type == "Archive" and .extra.Format == "tar.gz") | .path' dist/artifacts.json)

# Images: the index GoReleaser pushed to the local registry
jq -r '.[] | select(.type == "Docker Manifest") | .extra.Digest' dist/artifacts.json > "$tmp/index.txt"
[ "$(wc -l < "$tmp/index.txt")" -eq 1 ] || fail "want one Docker Manifest in dist/artifacts.json, got $(wc -l < "$tmp/index.txt")"
index=$(cat "$tmp/index.txt")
"$crane" manifest "$local_repo@$index" |
	jq -r '.manifests[] | "\(.platform.os) \(.platform.architecture) \(.digest)"' > "$tmp/platforms.txt"
[ -s "$tmp/platforms.txt" ] || fail "no platform images in the index $index"
if [ "$compare" = 1 ]; then
	published=ghcr.io/$github:$(jq -r .version dist/metadata.json)
	published_index=$("$crane" digest "$published") || fail "can't read $published"
	[ "$index" = "$published_index" ] || {
		while read -r os arch digest; do
			echo "$os/$arch: local $digest, published $("$crane" digest --platform "$os/$arch" "$published")" >&2
		done < "$tmp/platforms.txt"
		fail "image index: local $index, published $published_index"
	}
	echo "ok - image index identical to $published ($index)"
else
	echo "# index $index"
fi

# The binary in each platform image is the one of the archive
while read -r os arch digest; do
	jq -r --arg os "$os" --arg arch "$arch" \
		'.[] | select(.type == "Binary" and .goos == $os and .goarch == $arch) | "\(.path) \(.name)"' \
		dist/artifacts.json > "$tmp/binary"
	read -r bin name < "$tmp/binary" || fail "$os/$arch: no binary in dist/artifacts.json"
	"$crane" export "$local_repo@$digest" "$tmp/image.tar"
	# ko's layer names the binary /ko-app/<name>: -P keeps the leading /
	tar -xOPf "$tmp/image.tar" --wildcards "*ko-app/$name" > "$tmp/image-bin" 2> "$tmp/tar.err" || {
		cat "$tmp/tar.err" >&2
		fail "$os/$arch: no ko-app/$name in the image"
	}
	want=$(sha256sum < "$bin" | cut -d' ' -f1)
	got=$(sha256sum < "$tmp/image-bin" | cut -d' ' -f1)
	[ "$got" = "$want" ] || fail "$os/$arch: image binary $got, archived binary $want"
	echo "ok - $os/$arch: image binary identical to the archived one"
done < "$tmp/platforms.txt"
