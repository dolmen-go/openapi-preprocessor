#!/bin/sh
# Reproduces locally the release <tag> made by GoReleaser in CI, and checks:
# - unless -n, that the archives are identical to those of the GitHub Release;
# - that the archive entries are owned by 0:0 and dated at the commit time;
# - that two builds of the images give the same images: by GoReleaser in
#   snapshot mode if docker buildx is available, else by Podman (the images are
#   not compared with CI's);
# - the amd64 image with the smoke test.
#
# Usage: .goreleaser/reproduce.sh [-n] <tag>
#   -n  don't compare with the GitHub Release (for a tag that has none)
#
# Nothing is installed: run it with the Go and GoReleaser versions shown in
# the CI logs of the release, e.g.:
#   mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh v1.0.0-rc.3
# Requires: git, Docker with buildx (or Podman), jq, curl, GNU tar.
set -eu

# Not read from GoReleaser's outputs: the GitHub repository (GoReleaser needs
# it as the remote URL before it runs), and the image platforms and Dockerfile
# of dockers_v2 in .goreleaser.yaml (only in dist/config.yaml, which is YAML)
github=dolmen-go/openapi-preprocessor
platforms="linux/amd64 linux/arm64"
dockerfile=.goreleaser/Dockerfile

compare=1
if [ "${1:-}" = -n ]; then
	compare=0
	shift
fi
tag=${1:?usage: $0 [-n] <tag>}
here=$(cd "$(dirname "$0")" && pwd)
repo=$(git -C "$here" rev-parse --show-toplevel)

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

# Image builder: Docker with buildx (GoReleaser builds the images), else
# Podman. The version check rejects Podman's docker emulation (podman-docker),
# which cannot take the buildx options of dockers_v2.
if docker buildx version 2> /dev/null | grep -q 'github.com/docker/buildx' && docker info > /dev/null 2>&1; then
	builder=docker
else
	builder=podman
fi

tmp=$(mktemp -d)
# The images built here, by name and by ID
image_names=
image_ids=
# The buildx builders created here (Docker path)
builders=
# The images present before the run (IDs without sha256:, and names), which
# cleanup must never remove, even when a build here gives the same image
preexisting=" $("$builder" images -a --no-trunc --format '{{.ID}} {{.Repository}}:{{.Tag}}' 2> /dev/null |
	sed 's/sha256://g' | tr '\n' ' ') "
cleanup() {
	# Names first: removing a name untags the image, and removes it with its
	# last name; then IDs, for the images left untagged. One by one and
	# without -f: an image that still has another name stays.
	for image in $image_names $image_ids; do
		case "$preexisting" in *" ${image#sha256:} "*) continue ;; esac
		"$builder" rmi "$image" > /dev/null 2>&1 || true
	done
	for buildx_builder in $builders; do
		docker buildx rm "$buildx_builder" > /dev/null 2>&1 || true
	done
	rm -rf "$tmp"
}
trap cleanup EXIT
# dash runs no EXIT trap when a signal kills it (e.g. Ctrl-C): exit instead,
# with the status of a death by that signal
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# Go settings not set in CI, which would change the binaries: from the
# environment, and from the user's go env file (go env -w). Use the Go on
# PATH (the version of the CI logs), never a toolchain switch.
unset GOFLAGS GOEXPERIMENT GOFIPS140
export GOENV=off GOTOOLCHAIN=local

echo "# $(go version)"
echo "# goreleaser $(goreleaser --version 2>&1 | sed -n 's/^GitVersion: *//p')"
case "$builder" in
docker) echo "# docker: $(docker buildx version)" ;;
podman) echo "# podman $(podman version --format '{{.Client.Version}}')" ;;
esac

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
# GoReleaser needs the GitHub repository (nothing is published)
git remote add origin "https://github.com/$github.git"
epoch=$(git show -s --format=%ct HEAD)

# Release mode, as in CI, for the archives. No images: dockers_v2 builds and
# pushes in a single step, so it builds nothing without publishing. With
# Podman, docker is skipped too: not needed today, but it states that
# GoReleaser builds no image on this path, whatever future versions do.
skip=publish,announce,sbom
[ "$builder" = docker ] || skip=$skip,docker
GORELEASER_CURRENT_TAG=$tag goreleaser release --clean --skip="$skip" > "$tmp/goreleaser.log" 2>&1 || {
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

# Images. With Docker, GoReleaser builds them in snapshot mode, as in the CI
# dry run (this replaces the release-mode dist/, done with above).
if [ "$builder" = docker ]; then
	for run in 1 2; do
		# A throwaway BuildKit builder for each build, with the docker-container
		# driver as in CI (docker/setup-buildx-action): it starts with an empty
		# cache, so the second build cannot reuse the first one, and the user's
		# builders and their caches are left alone. It pulls moby/buildkit.
		# Named after the random suffix of $tmp, not the PID: the BuildKit
		# container is named after the builder, and a run in another PID
		# namespace (e.g. a devcontainer) on the same daemon would share it.
		# Lowercase, as buildx stores it.
		buildx_builder=reproduce-$(printf %s "${tmp##*.}" | tr '[:upper:]' '[:lower:]')-$run
		# Recorded before its creation: a builder whose bootstrap fails (e.g. the
		# pull of moby/buildkit) may still be registered, and must be removed
		builders="$builders $buildx_builder"
		docker buildx create --name "$buildx_builder" --driver docker-container --bootstrap > "$tmp/buildx.log" 2>&1 || {
			cat "$tmp/buildx.log" >&2
			fail "can't create the buildx builder $buildx_builder"
		}
		if [ "$run" = 1 ]; then
			# "Buildkit:" before buildx 0.13
			buildkit=$(docker buildx inspect "$buildx_builder" | sed -n 's/^Build[Kk]it\( version\)\{0,1\}: *//p')
			echo "# buildkit ${buildkit:-version unknown} (docker-container driver)"
		fi
		BUILDX_BUILDER=$buildx_builder GORELEASER_CURRENT_TAG=$tag \
			goreleaser release --snapshot --clean --skip=sbom > "$tmp/goreleaser.log" 2>&1 || {
			tail -n 20 "$tmp/goreleaser.log" >&2
			fail "goreleaser release --snapshot"
		}
		image_names="$image_names $(jq -r '.[] | select(.type == "Docker Image") | .name' dist/artifacts.json)"
		CONTAINER_ENGINE=docker "$here/fingerprint.sh" > "$tmp/fingerprint-$run.txt"
		# Also by ID: if the second build differs, it takes the names of the first
		image_ids="$image_ids $(awk '$1 == "image" { print $3 }' "$tmp/fingerprint-$run.txt")"
	done
	diff -u "$tmp/fingerprint-1.txt" "$tmp/fingerprint-2.txt" || fail "two snapshot builds give different images or archives"
	echo "ok - two GoReleaser snapshot builds give the same images and archives"
	amd64_image=$(jq -r 'first(.[] | select(.type == "Docker Image" and .extra.Platforms == ["linux/amd64"])) | .name' dist/artifacts.json)
	[ -n "$amd64_image" ] || fail "no linux/amd64 image in dist/artifacts.json"
	CONTAINER_ENGINE=docker SOURCE_DATE_EPOCH=$epoch "$here/smoke-test.sh" "$amd64_image" "$tag"
	exit 0
fi

# With Podman: build context laid out as GoReleaser does: <os>/<arch>/<binary name>
for platform in $platforms; do
	jq -r --arg os "${platform%/*}" --arg arch "${platform#*/}" \
		'.[] | select(.type == "Binary" and .goos == $os and .goarch == $arch) | "\(.path) \(.name)"' \
		dist/artifacts.json > "$tmp/binary"
	read -r bin name < "$tmp/binary" || fail "no $platform binary in dist/artifacts.json"
	mkdir -p "$tmp/ctx/$platform"
	cp -p "$bin" "$tmp/ctx/$platform/$name"
done

# build <platform>: build the image of the tag, print its ID
build() {
	podman build --quiet --no-cache --layers=false --platform "$1" --identity-label=false \
		--source-date-epoch="$epoch" --rewrite-timestamp \
		-f "$dockerfile" "$tmp/ctx"
}
for platform in $platforms; do
	id1=$(build "$platform")
	id2=$(build "$platform")
	image_ids="$image_ids $id1 $id2"
	[ "$id1" = "$id2" ] || fail "$platform: two builds give different images: $id1 $id2"
	echo "ok - $platform: two builds give the same image $id1"
	[ "$platform" != linux/amd64 ] || amd64_image=$id1
done

CONTAINER_ENGINE=podman SOURCE_DATE_EPOCH=$epoch "$here/smoke-test.sh" "$amd64_image" "$tag"
