# Container images built with ko — design

Date: 2026-10-16
Partially supersedes:
[2026-10-07-container-image-release-design.md][spec-1007] (the image
definition, `dockers_v2`, buildx, the image SBOM, the release dry run) and
[2026-10-09-reproducible-release-design.md][spec-1009] (everything about
images: the `dockers_v2` settings, the fingerprint, the image part of
`reproduce.sh`, the definition of reproducible images). Everything else there
still applies.

[spec-1007]: 2026-10-07-container-image-release-design.md
[spec-1009]: 2026-10-09-reproducible-release-design.md

## Goal

- GoReleaser builds and pushes the container images with ko (`kos`) instead
  of `dockers_v2`: no `docker buildx`, no BuildKit, no Dockerfile in the
  release. CI gets lighter and faster.
- [`reproduce.sh`][reproduce.sh] drops all the image building it does today
  (buildx builders, Podman builds, image cleanup): it had grown too complex.
  It keeps the archive checks, and still checks the images, which GoReleaser
  now builds in the same run as the archives.
- New: the images become reproducible between CI and a local build, like the
  archives. `reproduce.sh <tag>` checks that the images it builds are
  identical to those published on GHCR.

[reproduce.sh]: ../../../.goreleaser/reproduce.sh

## Findings

From GoReleaser 2.18's source (its `kos` step embeds ko 0.19.1 and
go-containerregistry 0.22.1), and from a spike on a throwaway tag in a scratch
clone, with GoReleaser 2.18.3 and Go 1.27.2:

- GoReleaser runs ko only when it publishes: with `--skip=publish`, no image
  is built. In snapshot mode, ko loads the images into a Docker daemon, and
  only one platform (`linux/amd64`): ko's daemon publisher keeps one image of
  the index.
- The environment variable `KO_DOCKER_REPO` replaces `kos.repositories`.
- Publishing also creates the GitHub Release. There is no `--skip` for it;
  `release.disable` is a template.
- `crane registry serve` (go-containerregistry) runs a registry in memory on a
  free port. ko pushes to `127.0.0.1:<port>` over plain HTTP.
- With `KO_DOCKER_REPO` set to such a registry and `release.disable` true, a
  release-mode run (`--skip=announce,sbom`) builds the archives and pushes the
  index and both platform images there, in about 15 s, with no Docker, no
  Podman and no `GITHUB_TOKEN`.
- Identical index and platform images (same digests) between builds in other
  directories, with umask 0022 and 0002, separate Go caches, other registry
  ports, and between this machine and a `golang:1.27.2` container (root,
  another Go install path) — **only with `TZ=UTC`**: ko writes the image
  creation time in the local time zone (`2026-10-16T17:56:28+02:00` versus
  `…15:56:28Z`), which changes the image config, so every digest. GitHub's
  runners run in UTC.
- `base_image: scratch` is rejected: ko pulls `docker.io/library/scratch`,
  which does not exist.
- ko's default base (`cgr.dev/chainguard/static`) brings about 1,340 files
  (tzdata, CA certificates, apk metadata), no working directory, and its own
  labels and annotations on our image (title `static`, vendor Chainguard, the
  base's creation date).
- crane builds a base image without Docker (`crane append`, `crane mutate`,
  `crane index append`), reproducibly: two builds give the same digest. Our
  image on that base has `/work` as working directory, user `65532:65532`,
  only our labels and annotations, 6 entries, 1.76 MB compressed (2.37 MB on
  Chainguard's base). It runs with Podman, `--read-only`, with and without a
  directory mounted on `/work`.
- ko writes the full reference of the base (registry included) into the
  annotations `org.opencontainers.image.base.name` and
  `org.opencontainers.image.base.digest` of each platform image and of the
  index. The same base served under another reference gives other digests:
  a local build matches CI's only if it pulls the base under the same
  reference.
- What ko puts in the image: the binary at `/ko-app/openapi-preprocessor`
  (`-r-xr-xr-x 0/0`, dated 1970-01-01, not configurable), an empty
  `/var/run/ko` (dated `ko_data_creation_time`), `PATH=/ko-app` and
  `KO_DATA_PATH=/var/run/ko` in the environment, the binary as entrypoint.
  ko builds the binary itself, with the settings of the `builds` entry
  (`-trimpath`, `-s -w`, `CGO_ENABLED=0`). The binary in each platform image
  is byte-for-byte the binary of the archive for that platform (same SHA-256
  for `linux/amd64` and `linux/arm64`).
- ko's SBOM (`sbom: spdx`, the default) is not an attestation in the index:
  ko pushes each platform's and the index's SPDX document as a separate
  artifact, tagged `sha256-<digest>.sbom`. The index lists only the two
  platform images. The platform SBOM lists the Go modules linked into the
  binary (our module and its dependencies) and no `stdlib` package; its
  `created` time is the image's creation time.
- Compared with BuildKit's SBOM (syft, in the index as an in-toto
  attestation), ko's SBOM:
  - is reproducible: its namespace comes from the image digest, its
    `created` time from the commit;
  - describes the image and its base (`DESCENDANT_OF`);
  - lacks the Go version (the `stdlib` package), CPEs, and the binary's
    path and checksum; the version of our module is only in its package
    URL;
  - is not found by `docker buildx imagetools inspect` nor by tools looking
    for BuildKit attestations: it is read with crane or
    `cosign download sbom`;
  - adds three `sha256-<digest>.sbom` tags per release on GHCR.

  It is kept as the image SBOM. The archive SBOMs, which list the Go
  version, describe the same binary.

## Definition

Replaces the table of [2026-10-09-reproducible-release-design.md][spec-1009].
For the same commit:

| Artifact | Must be identical |
|---|---|
| Release archives (`.tar.gz`, `.zip`) | yes, between CI and a local build with CI's Go and GoReleaser versions (unchanged) |
| Image index and per-platform images (`linux/amd64`, `linux/arm64`) | yes, between CI and a local build with CI's Go and GoReleaser versions, in UTC |
| Image SBOMs (ko's `.sbom` artifacts) | not checked |
| Archive SBOMs (`*.sbom.json`), `checksums.txt` as a whole | no (unchanged) |

The Go toolchain stays `go-version: stable` and GoReleaser `~> v2` (not
pinned).

[spec-1009]: 2026-10-09-reproducible-release-design.md

## Design

### Base image

A base image of our own, so that the image keeps `/work` and nothing else
comes in:

- [`.goreleaser/base.sh <reference>`][base.sh] (new, POSIX `sh`, clean under
  shellcheck): builds with crane a multi-platform image (`linux/amd64`,
  `linux/arm64`), pushes it to `<reference>`, and prints its digest
  (`sha256:…`) on standard output.
  - One layer: the directory `work/`, mode `0755`, owner `0:0`, dated
    1970-01-01 (a ustar archive made with `tar --format=ustar --owner=0
    --group=0 --numeric-owner --mtime=@0`).
  - Config: working directory `/work`, user `65532:65532`, the platform of
    each image.
  - crane is run with `go run
    github.com/google/go-containerregistry/cmd/crane@v0.22.1`, the version
    defined once at the top of the script (the go-containerregistry version
    GoReleaser 2.18 embeds). It needs Go and `tar`, nothing else.
  - The same versions of crane and Go give the same digest.
- The base is published by the user, from their machine, with `base.sh`:
  the first time before the merge (see Delivery), and again only when the
  base has to change (its layout, its platforms) or the package is lost.
  There is no workflow to publish it: it changes too rarely to be worth one.
- [`.goreleaser.yaml`][goreleaser.yaml] pins the base by digest:
  `base_image: ghcr.io/dolmen-go/openapi-preprocessor-base@sha256:<digest>`.
  The base changes only with a new publication and a commit that changes
  the pin. The `latest` tag is only for people browsing GHCR.
- One-time manual step in the settings of the GHCR package
  `openapi-preprocessor-base`, after the first publication: make it public,
  so that `reproduce.sh` and the dry runs of pull requests from forks pull
  it without credentials.

[base.sh]: ../../../.goreleaser/base.sh
[goreleaser.yaml]: ../../../.goreleaser.yaml

### [`.goreleaser.yaml`][goreleaser.yaml]

- `kos` replaces `dockers_v2`:
  - `repositories: [ghcr.io/dolmen-go/openapi-preprocessor]`, `bare: true`
    (the image is named exactly that, with no import path appended);
  - `base_image`: the pinned base above; no `user` (it comes from the base);
  - `platforms`: `linux/amd64`, `linux/arm64`;
  - `tags`: the same templates as today (`{{ .Version }}`, then `X.Y`, `X`
    and `latest` only for a public version);
  - `creation_time` and `ko_data_creation_time`: `"{{ .CommitTimestamp }}"`;
  - `labels`: the same as today;
  - `annotations`: `org.opencontainers.image.description`, `.source` and
    `.licenses`, as today. ko sets them on each platform image and on the
    index, so the `index:` prefix and the snapshot condition go away.
- `release.disable: '{{ isEnvSet "KO_DOCKER_REPO" }}'`, with a comment:
  when the images are sent to another registry (`reproduce.sh`), nothing is
  published on GitHub.
- `.goreleaser/Dockerfile` is deleted.

[goreleaser.yaml]: ../../../.goreleaser.yaml

### [`release.yml`][release.yml]

- `docker/setup-buildx-action` is removed. `docker/login-action` stays: ko
  pushes with Docker's credentials.
- The release step sets `TZ: UTC` (the runners are already in UTC; this makes
  it explicit, see Findings). (Claude's call.)
- The post-publish checks stay: the smoke test of each platform (QEMU kept for
  `linux/arm64`) with `SOURCE_DATE_EPOCH`, then the SBOM check, which now
  reads ko's SBOM of each platform: from the `sha256-<platform digest>.sbom`
  artifact, its SPDX document must list every module linked into the binary.
  That list is computed when the check runs, never written in the workflow:
  the modules of the packages that `go list -deps` reports for the code base
  on that platform (`GOOS=linux` and the platform's `GOARCH`). Modules used
  only by tests are neither in the binary nor in the SBOM. No more `stdlib`,
  which ko's SBOM does not list. Read with crane, installed as in `base.sh`.

[release.yml]: ../../../.github/workflows/release.yml

### `release-dry-run` job ([`test.yml`][test.yml])

- `docker/setup-buildx-action`, the fingerprints, the cache prune and the
  second build are removed.
- Steps: `goreleaser check`; `goreleaser release --snapshot --clean` (the
  archives, and the images: ko loads the `linux/amd64` one into the runner's
  Docker daemon); [`check-archives.sh`][check-archives.sh] on the `tar.gz`
  archives; the smoke test, with `SOURCE_DATE_EPOCH`, of the image recorded in
  `dist/artifacts.json` (type `Docker Manifest`).
- Why one build: two builds in the same job share Go's build cache, so they
  prove little. Reproducibility is checked by `reproduce.sh` against a
  published release, images included. (Claude's call.)
- `.goreleaser/fingerprint.sh` is deleted (its only other user,
  `reproduce.sh`, reads `dist/artifacts.json` directly).

[test.yml]: ../../../.github/workflows/test.yml
[check-archives.sh]: ../../../.goreleaser/check-archives.sh

### [`smoke-test.sh`][smoke-test.sh]

- The binary is `ko-app/openapi-preprocessor`, `-r-xr-xr-x 0/0`. `work` is
  required, `drwxr-xr-x 0/0` (it comes from the base).
- Unchanged: the user `65532:65532`, nothing owned by UID 65532 or writable by
  others, the processing checks, `-version`.
- With `SOURCE_DATE_EPOCH`: the image creation time is still checked. The
  file dates are no longer checked: ko and the base date their files
  1970-01-01 by construction, and reproducibility is checked by digests.
  (Claude's call.)
- The comments about BuildKit and Buildah go away.

[smoke-test.sh]: ../../../.goreleaser/smoke-test.sh

### [`reproduce.sh [-n] <tag>`][reproduce.sh]

Requires git, Go, GoReleaser, `jq`, `curl`, GNU `tar`, and network access
(Go module proxy for crane, GHCR for the base and the published images,
GitHub for the checksums). No Docker, no Podman. Run, as today, with the Go
and GoReleaser versions shown in the release's CI logs.

1. The private repository holding only `<tag>`: unchanged. Then, if the
   tag's `.goreleaser.yaml` has no top-level `kos` key (the tags released
   with `dockers_v2`: `v1.0.0-rc.1` to `v1.0.0-rc.3`), the script fails
   before starting anything else. Run on such a tag, GoReleaser would push
   the images to GHCR (`KO_DOCKER_REPO` does not apply to `dockers_v2`) and,
   with a `GITHUB_TOKEN` set, change the GitHub Release. The message tells
   the user to run the `reproduce.sh` of that tag instead, from a checkout of
   it, with the commands to do so (`git clone --branch <tag> <repository>
   <directory>`, then `<directory>/.goreleaser/reproduce.sh <tag>`).
2. Go isolated from the user's settings: unchanged. Plus `TZ=UTC`.
3. crane, built once from the same module and version as in `base.sh`
   (`GOBIN=<temporary directory> go install
   github.com/google/go-containerregistry/cmd/crane@<version>`); every crane
   command runs that binary. Not `go run`: stopping `go run` does not stop
   the program it started, so the registry would outlive the script.
   A local registry: `crane registry serve --address 127.0.0.1:0`, its port
   read from its log (`serving on port <n>`). Stopped at exit.
4. `KO_DOCKER_REPO=127.0.0.1:<port>/openapi-preprocessor
   GORELEASER_CURRENT_TAG=<tag> goreleaser release --clean
   --skip=announce,sbom`: the archives in `dist/`, the images pushed to the
   local registry, no GitHub Release (`release.disable`). Then the check
   that `dist/metadata.json` names `<tag>`.
5. Archives: unchanged (the comparison with the GitHub Release's checksums
   unless `-n`, then `check-archives.sh`).
6. Images: the local index digest is the `.extra.Digest` of the
   `Docker Manifest` artifact in `dist/artifacts.json`. Unless `-n`, it must
   equal the digest of `ghcr.io/dolmen-go/openapi-preprocessor:<version>`
   (`<version>` from `dist/metadata.json`), read with `crane digest`. If they
   differ, the script prints the digest of each platform, local and
   published (`crane digest --platform`), then fails. With `-n`, it prints
   the local digest.
7. Binaries, with or without `-n`: for each platform of the local index
   (read from its manifest with `crane manifest`, not from a constant), the
   binary in the image, `ko-app/<name>` extracted with `crane export`, must
   have the same SHA-256 as the `Binary` artifact of that OS and
   architecture in `dist/artifacts.json` (the binary that went into the
   archive; `<name>` is that artifact's `.name`). A missing binary, on
   either side, fails.
8. One `ok - …` line per check; exits non-zero on the first failure. The
   `EXIT` trap stops the registry and removes the temporary directory; the
   `HUP`, `INT` and `TERM` traps stay (`dash` runs no `EXIT` trap when a
   signal kills it, which would leave the registry running).

Removed: the choice of an image builder, the buildx builders, the Podman
builds, the platform and Dockerfile constants, the image cleanup and the list
of images present before the run, the local smoke test (identical digests
mean the image is the published one, which `release.yml` smoke-tests).
Constants at the top: the GitHub repository and the crane version.

[reproduce.sh]: ../../../.goreleaser/reproduce.sh

### [`README.md`][README.md]

- `COPY --from=ghcr.io/dolmen-go/openapi-preprocessor:1
  /ko-app/openapi-preprocessor /usr/local/bin/`.
- The image SBOM: how to read ko's SBOM (replaces `docker buildx imagetools
  inspect`), and that the Go version is in the SBOM of the matching
  `linux` archive of the GitHub Release, whose binary is the image's.
- The description of the image stays accurate (the static binary, no shell,
  an unprivileged user).

[README.md]: ../../../README.md

### Earlier specs

A notice at the top of [2026-10-07-container-image-release-design.md][spec-1007]
and of [2026-10-09-reproducible-release-design.md][spec-1009] says they are
partially superseded by this spec. Nothing else changes in them.

[spec-1007]: 2026-10-07-container-image-release-design.md
[spec-1009]: 2026-10-09-reproducible-release-design.md

## Delivery

In one merge. The user publishes the first base from their machine, during
the work and before the change of `.goreleaser.yaml` that pins it:

1. Log in to `ghcr.io` with a token that can write packages. crane reads
   Docker's credentials (`~/.docker/config.json`), or, when there are none,
   Podman's (`podman login ghcr.io`, as on this machine).
2. `.goreleaser/base.sh ghcr.io/dolmen-go/openapi-preprocessor-base:latest`,
   with the Go on `PATH`; it prints the digest that `.goreleaser.yaml` pins.
3. The one-time step in the package settings (see Base image).

## Verification

On this machine, before any release:

- `base.sh` twice into a local registry: the same digest.
- `reproduce.sh -n` on a throwaway tag in a scratch clone, twice: identical
  archives and index digest, archive checks pass, the image binaries match
  the archived ones. Once with a config whose image binary differs from the
  archived one (e.g. other `kos.ldflags`): the binary check fails.
- `smoke-test.sh` with Podman on the image of that run.
- `reproduce.sh v1.0.0-rc.3`: fails with the message pointing to that tag's
  `reproduce.sh`, without running GoReleaser.

In CI:

- The dry run on the branch (pushed by the user): the snapshot build, the
  archive checks and the smoke test of the `linux/amd64` image pass.

After the next release (tagged by the user):

- The release workflow passes: smoke tests of both platforms, SBOM check.
- `reproduce.sh <tag>` with the Go and GoReleaser versions of the release's CI
  logs: archives and images identical to the published ones.

## Out of scope

- Signing, and attestations of any kind (cosign, GitHub attestations).
  BuildKit's provenance attestation is no longer produced.
- Reproducible archive SBOMs (syft, unchanged).
- Checking the base image in `reproduce.sh`: its digest depends on the crane
  and Go versions that built it; it is pinned by digest.
- A smoke test in `reproduce.sh`.
- A workflow publishing the base image: `base.sh` is run by hand.
- The binary at `/usr/local/bin`: ko puts it in `/ko-app`.
