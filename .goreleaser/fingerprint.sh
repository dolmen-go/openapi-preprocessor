#!/bin/sh
# Prints what must be identical between two GoReleaser snapshot builds of the
# same commit: the image IDs and the checksums of the archives (the SBOMs are
# left out: they are not reproducible). The images and the archives are those
# GoReleaser recorded in dist/artifacts.json.
#
# Usage: .goreleaser/fingerprint.sh
#   Run from the repository root after 'goreleaser release --snapshot'.
#
# Environment:
#   CONTAINER_ENGINE  docker (default) or podman
set -eu

engine=${CONTAINER_ENGINE:-docker}

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

# One line per image (one per platform in a snapshot build): its reference and ID
images=$(jq -r '.[] | select(.type == "Docker Image") | .name' dist/artifacts.json | sort)
[ -n "$images" ] || fail "no images in dist/artifacts.json"
for image in $images; do
	id=$("$engine" image inspect --format '{{.Id}}' "$image")
	echo "image $image $id"
done
# One checksum line per archive (any format), as in GoReleaser's checksums file
archives=$(jq -r '.[] | select(.type == "Archive") | "\(.extra.Checksum | ltrimstr("sha256:"))  \(.name)"' dist/artifacts.json | sort -k2)
[ -n "$archives" ] || fail "no archives in dist/artifacts.json"
echo "$archives"
