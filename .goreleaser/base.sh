#!/bin/sh
# Builds and pushes the base image of the container images that GoReleaser
# builds with ko (see kos in ../.goreleaser.yaml): the directory /work, owned
# by root, as working directory, the user 65532:65532, and nothing else.
# Prints its digest (sha256:...) on standard output.
#
# Usage: .goreleaser/base.sh <repository>:<tag>
#
# .goreleaser.yaml pins the base by digest: publishing a new base changes
# nothing until a commit changes that pin. The digest depends on the versions
# of crane and Go (with the same versions, the same digest).
#
# Publication (with a token that can write packages, e.g. 'podman login
# ghcr.io': crane reads Docker's or Podman's credentials):
#   .goreleaser/base.sh ghcr.io/dolmen-go/openapi-preprocessor-base:latest
#
# Requires: Go, GNU tar.
set -eu

# Also in reproduce.sh and .github/workflows/release.yml: the
# go-containerregistry version GoReleaser embeds
crane_version=v0.22.1
platforms="linux/amd64 linux/arm64"

usage() {
	echo "usage: $0 <repository>:<tag>" >&2
	exit 2
}

[ $# -eq 1 ] || usage
reference=$1
# A tag is required (the image is pushed there), a digest is not a tag
case "$reference" in *@*) usage ;; esac
case "${reference##*/}" in *:*) ;; *) usage ;; esac
repository=${reference%:*}

crane() {
	go run "github.com/google/go-containerregistry/cmd/crane@$crane_version" "$@"
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# One layer: the directory work/, mode 0755 whatever the umask, owned by root,
# dated 1970-01-01
mkdir -m 0755 "$tmp/work"
tar -C "$tmp" --format=ustar --owner=0 --group=0 --numeric-owner --mtime=@0 -cf "$tmp/work.tar" work

# The layer image is pushed to <reference> itself, so that no other tag is
# created: the index below replaces it
crane append --oci-empty-base -f "$tmp/work.tar" -t "$reference" > /dev/null
manifests=
for platform in $platforms; do
	# Pushed by digest, prints <repository>@sha256:...
	manifest=$(crane mutate "$reference" --set-platform "$platform" -w /work -u 65532:65532 --repo "$repository")
	manifests="$manifests -m $manifest"
done
# shellcheck disable=SC2086 # $manifests is a list of options
crane index append $manifests -t "$reference" > /dev/null
crane digest "$reference"
