# Reproducible release artifacts — design

Date: 2026-10-09
Status: approved in brainstorming, pending spec review
Partially supersedes: [2026-10-07-container-image-release-design.md](2026-10-07-container-image-release-design.md)
(timestamps, the `created` label, archive ownership, verification); everything
else there still applies.

## Goal

- Anyone can reproduce from a Git checkout of a release tag the archives
  published by CI, with the same toolchain versions as CI. Those versions are
  not pinned: they are visible in the GitHub Actions logs of the release
  (`actions/setup-go`, `goreleaser/goreleaser-action`,
  `docker/setup-buildx-action`), and the person reproducing sets up the same
  ones.
- Container images are reproducible within a toolchain: two builds with the
  same toolchain give the same image. CI proves it with BuildKit on every
  push. Locally, with `docker buildx`, GoReleaser builds the images twice
  (snapshot mode) as in CI; on a machine without it (this one), two Podman
  builds give the same image. Neither is expected to match CI's images.

## Findings that motivate it

Observed on the published `1.0.0-rc.2` image and archives, on GoReleaser
2.18.2's buildx command (logging `docker` shim) and in GoReleaser's source:

- GoReleaser `dockers_v2` passes no `SOURCE_DATE_EPOCH` and no
  `rewrite-timestamp` to buildx. The image config `created` and `history`
  timestamps are the build's wall-clock time; layer file mtimes are build or
  compile time.
- Our `org.opencontainers.image.created` label is `{{ .Date }}` (GoReleaser's
  start time).
- `builds.mod_timestamp` is unset: the binary's mtime is its compile time.
- Archive entries carry checkout/compile mtimes and the runner's owner
  (`runner/runner`, `1001/1001`).
- The image config has no builder version label (history comments are the
  constant `buildkit.dockerfile.v0`). Builder versions, timestamps and random
  IDs are only in the attestations (SBOM and SLSA provenance).

## Definition

For the same commit:

| Artifact | Must be identical |
|---|---|
| Release archives (`.tar.gz`, `.zip`) | yes, between CI and a local build with CI's Go and GoReleaser versions |
| Per-platform images (`linux/amd64`, `linux/arm64`) | yes, between two builds with the same toolchain: BuildKit in CI (manifests), BuildKit or Podman locally (image IDs); not between CI and a local build |
| Image index | no: it lists the attestation manifests |
| Attestations (SBOM, provenance) | no: timestamps, random IDs, tool versions |
| Archive SBOMs (`*.sbom.json`) | no: syft writes a random namespace and a timestamp ([anchore/syft#3931](https://github.com/anchore/syft/issues/3931)) |
| `checksums.txt` as a whole | no: it lists the archive SBOMs; its archive lines must be identical |

The Go toolchain stays `go-version: stable` and GoReleaser `~> v2` (not
pinned). The Go version used is also recorded in the binary's build info and
in the SBOMs.

## Design

Approach: everything in the GoReleaser config, verified in CI and by a local
reproduction script. If buildx rejects the output flags below, the work stops
and the options are reviewed; there is no planned fallback.

All timestamps come from the tagged commit's committer date:
`.CommitTimestamp` (Unix seconds) and `.CommitDate` (RFC 3339) in GoReleaser,
`git log -1 --format=%ct` in the workflows.

### `.goreleaser.yaml`

- `builds`: `mod_timestamp: "{{ .CommitTimestamp }}"`.
- `archives`:
  - `builds_info`: `mtime: "{{ .CommitDate }}"`, `owner: root`,
    `group: root`, `mode: 0755`.
  - `files`: `LICENSE`, `README.md`, `man/man1/openapi-preprocessor.1` as
    `src:` entries with `info:` `mtime: "{{ .CommitDate }}"`, `owner: root`,
    `group: root`, `mode: 0644`.
  - Explicit modes: Git checks files out with the user's umask (`664` with
    umask `0002`, `644` in CI), so without them local archives could never
    match CI's.
  - GoReleaser cannot write tar entries with numeric IDs and empty names: a
    set `owner`/`group` gives `Uid`/`Gid` 0 with that name; an unset one
    keeps the file's IDs and their names. Hence `0/0` with names
    `root/root`. Access and change times are not written (Go's tar writer
    drops them when no format is chosen). Zip entries have no owner; their
    mtime comes from `info`/`builds_info` too.
- `dockers_v2`:
  - Build argument `SOURCE_DATE_EPOCH`, which BuildKit uses for the image
    config `created` and `history` timestamps, and for `rewrite-timestamp`:

    ```yaml
    build_args:
      SOURCE_DATE_EPOCH: "{{ .CommitTimestamp }}"
    ```

    Not a top-level `env:` entry: GoReleaser renders `env` before it loads
    the git information (pipeline order `env`, then `git`), so
    `{{ .CommitTimestamp }}` would be the zero time there (observed:
    `SOURCE_DATE_EPOCH=-62135596800`). `build_args` are rendered when the
    image is built.
  - `org.opencontainers.image.created` label: `{{ .CommitDate }}`.
  - Rewrite all layer file timestamps to `SOURCE_DATE_EPOCH`, including the
    directories BuildKit creates (`usr/`, `usr/local/`, `usr/local/bin/`,
    `work/`). The output type matches what GoReleaser adds (`--load` for
    snapshots, `--push` for releases):

    ```yaml
    flags:
      - "{{ if .IsSnapshot }}--output=type=docker,rewrite-timestamp=true{{ else }}--output=type=image,rewrite-timestamp=true{{ end }}"
    ```

  - Attestations unchanged: `sbom: "true"`, buildx's default provenance.

### `.goreleaser/fingerprint.sh`

Run after a snapshot build. Prints one line per artifact that must be
reproducible, both read from `dist/artifacts.json` (see "Settings read from
GoReleaser's outputs" below):

- `image <reference> <id>` for each image GoReleaser built (one per platform
  in a snapshot build, e.g. `ghcr.io/dolmen-go/openapi-preprocessor:<version>-amd64`):
  its image ID (config digest);
- one checksum line per archive GoReleaser built (`<sha256>  <name>`, as in
  its checksums file).

`CONTAINER_ENGINE` selects `docker` (default) or `podman`, as in the smoke
test. Exits non-zero if an image is missing from the engine, or if
`dist/artifacts.json` lists no image or no archive.

### `.goreleaser/smoke-test.sh`

New checks, only when `SOURCE_DATE_EPOCH` is set:

- the image `created` time equals `SOURCE_DATE_EPOCH`: `image inspect` of
  the image ID (`.Image`) of the container created for `$PLATFORM`, so the
  platform variant is the right one (the container's own `.Created` is the
  container's creation time, not the image's);
- in the `export | tar` listing (read in UTC), the mtime of `usr/`,
  `usr/local/`, `usr/local/bin/`, `usr/local/bin/openapi-preprocessor` and
  `work/` (if present) equals `SOURCE_DATE_EPOCH`.

Running as the current user, for the files only readable by their owner:
`--user=<uid>:<gid>` with rootful Docker (as in CI), `--userns=keep-id
--user=<uid>:<gid>` with Podman, `--user=0:0` with rootless Docker (detected
by `rootless` in `docker info --format '{{.SecurityOptions}}'`): there the
current user is root in the container, and `--user=<uid>` maps to a
subordinate UID that cannot read them.

### `release-dry-run` job (`test.yml`)

1. `goreleaser release --snapshot --clean`, then `fingerprint.sh` into
   `fingerprint-1.txt` (outside `dist/`).
2. `docker buildx prune --all --force`: no BuildKit cache for the second
   build.
3. Same build again, `fingerprint-2.txt`.
4. `diff fingerprint-1.txt fingerprint-2.txt`: any difference fails the job.
5. Archive ownership, mode and mtime (`.goreleaser/check-archives.sh`):
   every entry of every archive of format `tar.gz` listed in
   `dist/artifacts.json` has mode `0644` or `0755`, numeric
   owner `0/0` (`tar --numeric-owner -tv`), user name `root` or empty and
   group name `root` or empty (each checked separately; in `tar -tv` an empty
   name shows as its number), and the commit time as mtime. A missing,
   unreadable or empty archive fails.
6. Smoke test with `SOURCE_DATE_EPOCH=$(git log -1 --format=%ct)` of the
   first `linux/amd64` image recorded in `dist/artifacts.json` (for a final
   tag, GoReleaser records one artifact per image tag: `X.Y.Z…`, `X.Y`, `X`,
   `latest`, all the same image).

### `release.yml`

The post-publish smoke test, on each platform, runs with
`SOURCE_DATE_EPOCH=$(git log -1 --format=%ct)`: it checks the published
image's `created` time and layer mtimes. This is the only check of the
`--push` output path (the dry run uses `--load`); nothing is rebuilt.

### `.goreleaser/reproduce.sh [-n] <tag>`

Local reproduction of a release. It installs nothing: it runs with the Go and
GoReleaser on `PATH`, which the user sets to the versions shown in the
release's CI logs, e.g.
`mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh v1.0.0-rc.3`.
It needs Docker with buildx, or Podman as a fallback, plus `jq`, `curl` and
GNU `tar`.

Image builder, chosen once at the start (and printed with its version):

- **Docker with buildx**, when `docker buildx version` names
  `github.com/docker/buildx` and `docker info` succeeds (a daemon to load the
  images into). The output check rejects Podman's `docker` emulation
  (`podman-docker`), whose `buildx version` prints Buildah's version and which
  cannot take the buildx options of `dockers_v2`.
- **Podman** otherwise.

1. A private repository in a temporary directory (removed on exit), holding
   only `<tag>` and its history (`git fetch --no-tags <repo>
   refs/tags/<tag>:refs/tags/<tag>`), checked out with `core.autocrlf=false`,
   no attributes file and `log.showSignature=false`. Not a `git worktree` of
   the working copy, for three reasons: untracked files (e.g. `mise.toml`)
   would make Go stamp `+dirty` into the binary's version; another tag on the
   same commit (an rc and the final release) would change the version Go
   stamps (the highest semver tag) and the tag GoReleaser picks; the user's
   git configuration could change the checked-out files or the commit date.
2. Go isolated from the user's settings: `GOENV=off` (ignores `go env -w`),
   `GOTOOLCHAIN=local` (the Go on `PATH`, no toolchain switch), `GOFLAGS`,
   `GOEXPERIMENT` and `GOFIPS140` unset.
3. In that repository: `GORELEASER_CURRENT_TAG=<tag> goreleaser release
   --clean --skip=publish,announce,sbom` (plus `docker` with the Podman
   builder: not needed today, since `dockers_v2` builds nothing when
   publishing is skipped, but it states that GoReleaser builds no image on
   this path, whatever future GoReleaser versions do), then a check that
   `dist/metadata.json` names `<tag>`. This builds the release-mode
   binaries and archives (version and labels as in CI); no images
   (`dockers_v2` builds and pushes in a single step, so it builds nothing
   when publishing is skipped), no syft needed, no `GITHUB_TOKEN`.
4. Unless `-n`: download the checksums file of the GitHub Release `<tag>`
   (its name from `dist/artifacts.json`). It must list exactly the archives
   GoReleaser built locally and their SBOMs (`<archive>.sbom.json`, not built
   locally): any other published file fails (published but not built
   locally). Then its lines for the local archives are compared with the
   local checksum lines (from `dist/artifacts.json`): any difference, or an
   archive missing from the release, fails.
5. Archive ownership, mode and mtime: `check-archives.sh` on every archive of
   format `tar.gz` listed in `dist/artifacts.json`.
6. Images:
   - **Docker with buildx**: GoReleaser builds them, as the CI dry run does.
     Twice, each time with a throwaway buildx builder:
     `docker buildx create --name reproduce-<id>-<run> --driver
     docker-container --bootstrap` (`<id>`: the random suffix of the
     temporary directory, not the PID, which a run in another PID namespace
     on the same daemon could share), selected for GoReleaser with
     `BUILDX_BUILDER`, removed (`docker buildx rm`) at exit. Each builder
     starts with an empty cache, so the second build cannot reuse the first
     one without pruning anything; the user's builders and their caches,
     possibly shared (remote or cloud builders), are left alone; and the
     `docker-container` driver is the one CI uses (`docker/setup-buildx-action`),
     unlike the `docker` driver of a default Docker install, where
     `rewrite-timestamp` with `--load` is unverified. Cost: creating a builder
     pulls the `moby/buildkit` image (network access; the image stays), and
     each build starts more slowly. The BuildKit version of the first builder
     is printed in the header (`version unknown` if buildx does not report
     it). With each builder:
     `GORELEASER_CURRENT_TAG=<tag> goreleaser release --snapshot --clean
     --skip=sbom` (the images are loaded into Docker, with the snapshot
     version in their labels: they are not compared with CI's), then
     `fingerprint.sh` (images and archives of the snapshot). The two
     fingerprints must be identical. The release-mode `dist/` of steps 3–5 is
     replaced by the snapshot's, which is why those steps come first. A
     builder that cannot be created (e.g. no network) fails with buildx's
     message.
   - **Podman**: for each platform of a constant list (`linux/amd64`,
     `linux/arm64`, as in `dockers_v2`), lay out the build context as
     GoReleaser does (`<os>/<arch>/<binary name>`, the binary and its name from
     `dist/artifacts.json`) and build the Dockerfile (a constant,
     `.goreleaser/Dockerfile`) twice with
     `podman build --platform <os>/<arch> --no-cache --layers=false
     --source-date-epoch=<commit time> --rewrite-timestamp
     --identity-label=false`. The two image IDs must be equal. No labels: the
     images are not compared with CI's, and `--identity-label=false` leaves
     out the builder version label (`io.buildah.version`) that Podman adds by
     default. `--layers=false`: no intermediate images left behind.
7. Smoke test of the local `linux/amd64` image (with Docker: the first one of
   type `Docker Image` for `linux/amd64` in the snapshot's
   `dist/artifacts.json`, which records one per image tag for a final tag)
   with `CONTAINER_ENGINE` set to the builder and
   `SOURCE_DATE_EPOCH=<commit time>`. The binary in it is stamped with
   `<tag>` in both cases: Go stamps the git tag, whatever GoReleaser's
   snapshot version.
8. Prints one `ok - …` line per check; exits non-zero on the first failure.
   Removes the images it built, but never an image that existed before the
   run (IDs and names listed at start), even when a build gives the same
   image: names first (untagging), then IDs, one by one and without `-f`, so
   that an image with another name stays. The cleanup (images, buildx
   builders, temporary directory) also runs when the script is interrupted
   (Ctrl-C, `SIGTERM`, `SIGHUP`): `dash` runs no `EXIT` trap when a signal
   kills it, so these signals make it `exit` (129, 130, 143) instead.

`-n` skips step 4, for a tag that has no published release.

### Settings read from GoReleaser's outputs

To keep `reproduce.sh` and `fingerprint.sh` in step with `.goreleaser.yaml`
without new tools, they read what GoReleaser wrote in `dist/` with `jq`
(already required), instead of repeating the config:

| Setting | Read from |
|---|---|
| Archives built, with their checksums (any format) | `dist/artifacts.json`: artifacts of type `Archive`, `.name` and `.extra.Checksum` |
| Archives of format `tar.gz` (for `check-archives.sh`) | `dist/artifacts.json`: `Archive` artifacts with `.extra.Format == "tar.gz"` |
| Name of the checksums file | `dist/artifacts.json`: the artifact of type `Checksum` |
| Linux binaries and their name (image build context) | `dist/artifacts.json`: artifacts of type `Binary`, `.path` and `.name` |
| Images built by a snapshot (`fingerprint.sh`, dry-run smoke test) | `dist/artifacts.json`: artifacts of type `Docker Image`, `.name` (the image reference) and `.extra.Platforms`; one artifact per image tag (several for a final tag), so the smoke test takes the first for `linux/amd64` |
| Tag built | `dist/metadata.json`: `.tag` |

Kept as constants, each defined once at the top of `reproduce.sh`:

- the GitHub repository (`dolmen-go/openapi-preprocessor`): GoReleaser needs
  it as the remote URL before it runs, and the user's `origin` may be an SSH
  host alias that GoReleaser does not recognize as GitHub;
- the image platforms and the Dockerfile, used by the Podman builder only
  (with Docker, GoReleaser's snapshot builds the images and `fingerprint.sh`
  reads them from `dist/artifacts.json`): the release-mode run uses
  `--skip=publish`, so `dockers_v2` builds no image and `dist/artifacts.json`
  records none (unlike a snapshot build); they are only in
  `dist/config.yaml`, which is YAML (another tool to install); deriving the
  platforms from the Linux binaries would silently build the wrong set if
  they ever diverged;
- the Podman build options: the config has buildx options, which Podman does
  not take, with unrendered templates.

Duplicated on purpose: `check-archives.sh` hard-codes the expected modes and
owner (`0644`/`0755`, `root`): it is a test oracle, and reading the expected
values from the config it checks would prove much less.

### `release.yml`: the pushed tag

The release step sets `GORELEASER_CURRENT_TAG: ${{ github.ref_name }}`:
when a final tag is pushed on the commit of a pre-release tag, GoReleaser
would otherwise pick the pre-release tag (`git tag --points-at HEAD
--sort=-version:refname` puts `v1.0.0-rc.3` before `v1.0.0`) and publish the
final release as the pre-release.

## Verification on this machine

Assumptions to check first (each one, if false, is reviewed with the user
before going on):

- GoReleaser 2.18 accepts `--skip=sbom`, and `--skip=publish` runs without
  `GITHUB_TOKEN`.
- Podman 5.7 `build` accepts `--source-date-epoch`, `--rewrite-timestamp`
  and `--identity-label=false`.
- buildx accepts the `--output` flags next to the `--push`/`--load` that
  GoReleaser adds (only checkable in CI).

Before any release with the new config:

- Logging `docker` shim: buildx receives `--build-arg
  SOURCE_DATE_EPOCH=<commit timestamp>` and the new `--output` flag, for both
  snapshot and release builds.
- `reproduce.sh -n` on a throwaway tag, in a scratch clone (no tag added to
  the working repository), run twice: identical archive checksums between the
  two runs; archive entries `0/0`, `root/root`, commit-time mtime; identical
  Podman image IDs within each run; smoke test passes.
- `reproduce.sh -n` and `fingerprint.sh` on a throwaway tag whose config
  renames the checksums file and adds an archive format: they cover the
  renamed file and the extra archives without any change to the scripts.
- `fingerprint.sh` on a snapshot whose config adds an image platform (built
  through a logging `docker` shim that writes the image ID file, then with
  Podman): the extra image is fingerprinted.
- `reproduce.sh` against a published checksums file served by a `curl` shim:
  an extra published archive fails, a missing one fails, the archives plus
  their SBOMs pass.
- Image builder choice: no `docker` → Podman, with `docker` added to
  GoReleaser's `--skip` (seen in an `sh -x` trace); a `docker` shim that
  emulates `podman-docker` (`buildx version` prints Buildah's version) →
  Podman; a `docker` shim that reports a real buildx and runs the builds with
  Podman underneath → the Docker path: two snapshot builds, each with its own
  throwaway `docker-container` builder (selected with `BUILDX_BUILDER`,
  removed at exit; no prune), identical fingerprints, smoke test of the
  `linux/amd64` image, images removed at the end; a builder that cannot be
  created fails cleanly. The real Docker path (with
  BuildKit) cannot run on this machine: its first real run is the user's, on a
  machine with Docker.

After the next release published by CI with the new config (tagged by the
user):

- `reproduce.sh <tag>` with the Go and GoReleaser versions shown in that
  release's CI logs: the local archives match the published ones.

## Out of scope

- Pinning the Go, GoReleaser or BuildKit versions.
- Reproducing CI's images locally: CI publishes multi-platform images with
  attestations from a release-mode build, while a local GoReleaser build of
  images runs in snapshot mode (different version in the labels), and a
  Podman build is a different toolchain.
- Reproducible SBOMs (image attestation and archive SBOMs): syft, which
  generates both, always writes the SPDX `created` timestamp and a random
  document namespace UUID. Making them deterministic (override `created`,
  e.g. from `SOURCE_DATE_EPOCH`, and a content-based UUIDv5 namespace) is
  requested in [anchore/syft#3931](https://github.com/anchore/syft/issues/3931)
  ("Reproducible output for SPDX SBOM", open as of 2026-10-09). To revisit
  when it is resolved.
- Reproducible provenance attestation; dropping provenance.
- Making the image index digest reproducible.
- Signing.
