# Container image and release binaries — design

Date: 2026-10-07
Status: approved in brainstorming, pending spec review

## Goal

Make `openapi-preprocessor` easy to install and use without a Go toolchain:

- a minimal, hardened container image usable with Docker and Podman;
- standalone binaries attached to GitHub Releases.

Both are produced automatically when a version tag is pushed.

## Use cases

Supported:

1. Ad-hoc use on a developer machine: `docker run …` / `podman run …`.
2. Use from CI pipelines by running the image with `docker run` (or by
   downloading a release binary).
3. Copying the binary into another image: `COPY --from=…`.

Explicitly **not** supported: using the image as a CI *job image* (GitLab CI
`image:`, GitHub Actions `container:`), because the image has no shell. The
README documents the alternatives (`docker run` step, `COPY --from`, release
binary).

## Decisions

| Topic | Decision |
|---|---|
| Registry | GitHub Container Registry only: `ghcr.io/dolmen-go/openapi-preprocessor` |
| Tooling | GoReleaser v2 (config + `goreleaser/goreleaser-action`) |
| Trigger | Push of a tag matching `v*`. No `workflow_dispatch`. |
| Binary platforms | {linux, darwin, windows} × {amd64, arm64} |
| Image platforms | `linux/amd64`, `linux/arm64` (single multi-arch manifest) |
| Image base | `scratch` — binary only, no shell |
| Image user | `65532:65532` (numeric: no `/etc/passwd` in `scratch`) |
| Secrets | None beyond the built-in `GITHUB_TOKEN` |

## Files

| Path | Purpose |
|---|---|
| `.goreleaser.yaml` | GoReleaser config, at repo root (default location, so local `goreleaser check` / `goreleaser release --snapshot` need no flags) |
| `.goreleaser/Dockerfile` | Image definition, used only by GoReleaser (referenced via `dockerfile:`); not at repo root |
| `.github/workflows/release.yml` | Release workflow |
| `.github/workflows/test.yml` | Gains a release-dry-run job |
| `README.md` | New install/usage sections |

## The image

`.goreleaser/Dockerfile`:

```dockerfile
# Used by GoReleaser only (see .goreleaser.yaml): it copies a prebuilt binary
# and cannot be built from a source checkout with 'docker build'.
FROM scratch
ARG TARGETPLATFORM
COPY --chown=0:0 --chmod=0555 $TARGETPLATFORM/openapi-preprocessor /usr/local/bin/openapi-preprocessor
WORKDIR /work
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/openapi-preprocessor"]
```

Properties:

- Contains only the static binary (`CGO_ENABLED=0`). No CA certificates or
  tzdata: the tool does no network access and no time handling.
- Binary path `/usr/local/bin/openapi-preprocessor` is stable and documented
  (for `COPY --from`).
- `/work` is the documented mount point and the working directory, so paths
  given on the command line are relative to the mount.
- Read-only in practice: everything in the image is owned by `0:0` and the
  process runs as UID 65532, so it cannot write anywhere in the container
  filesystem even without `--read-only`. A read-only root filesystem cannot be
  enforced by an image; `--read-only` is therefore part of the documented
  standard invocation.
- OCI labels: `org.opencontainers.image.source`, `.version`, `.revision`,
  `.created`, `.licenses=Apache-2.0`, `.title`, `.description`. The `source`
  label links the GHCR package to the repository.

### Image tags

- Always: `{{.Version}}` (e.g. `1.2.3`, `1.0.0-rc.2`).
- Only when the tag is not a pre-release: `{{.Major}}.{{.Minor}}`,
  `{{.Major}}`, `latest`.

## Release binaries

- Build flags: `CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w"`.
- No version injection via `-ldflags`: Go stamps the VCS tag into the build
  info when building a clean checkout at a tag, and the existing `version()`
  (`main.go`) reads it from `debug.ReadBuildInfo()`. GoReleaser writes into
  `dist/`, which is already in `.gitignore`, so the tree stays clean.
  `-s -w` strips symbols/DWARF but not the build info.
- Archives: `tar.gz` for linux/darwin, `zip` for windows; GoReleaser default
  name template (`openapi-preprocessor_<version>_<os>_<arch>`).
- Archive contents: binary, `LICENSE`, `README.md`,
  `man/man1/openapi-preprocessor.1` (already committed).
- `checksums.txt` (sha256) for all archives.
- GitHub Release created by GoReleaser with `prerelease: auto` (tags such as
  `v1.0.0-rc.2` are marked pre-release). Changelog from git log since the
  previous tag.

## Release workflow (`.github/workflows/release.yml`)

- `on: push: tags: ['v*']`
- `permissions: contents: write, packages: write`
- Steps:
  1. `actions/checkout` with `fetch-depth: 0`.
  2. `actions/setup-go` with `go-version-file: go.mod`.
  3. `go test ./...`
  4. `docker/setup-buildx-action` (no QEMU needed: the Dockerfile has no `RUN`).
  5. `docker/login-action` to `ghcr.io` with `GITHUB_TOKEN`.
  6. `goreleaser/goreleaser-action` (GoReleaser `~> v2`), `release --clean`.
  7. Post-publish check: pull `ghcr.io/dolmen-go/openapi-preprocessor:<version>`
     and assert that `-version` prints the git tag.
- Action major versions follow the style already used in `test.yml`.

## Release dry run on every push/PR (`test.yml`)

New job, `needs: [test]`, on ubuntu:

1. `goreleaser check`.
2. `goreleaser release --snapshot --clean`: builds all binaries and the image
   locally, pushes nothing. The image for the runner's platform must be
   available in the local Docker daemon for the next steps (mechanism —
   snapshot behavior of `dockers_v2` or an explicit tag template — to be
   confirmed in the implementation plan).
3. Functional smoke test with the documented hardened invocation:
   `docker run --rm --read-only --network none -v "$PWD/testdata:/work:ro" <image> 11-ref-relative/input.yml`,
   output compared to `testdata/11-ref-relative/result.json` after
   normalizing both with `jq -S`. This case exercises relative `$ref`.
4. Image hardening checks:
   - `docker image inspect` reports `Config.User` = `65532:65532`.
   - `docker create` + `docker export | tar -tv` shows exactly the binary
     (`-r-xr-xr-x 0/0 usr/local/bin/openapi-preprocessor`) and `work/`
     (`0/0`, mode `0755`), and nothing writable by UID 65532.

## README changes

Under "Install", two new subsections.

**Download a binary**: link to GitHub Releases, list of platforms, how to
verify with `checksums.txt`.

**Run with Docker or Podman**:

- Standard (hardened) invocation:

  ```
  docker run --rm --read-only --network none -v "$PWD:/work:ro" ghcr.io/dolmen-go/openapi-preprocessor spec.yaml > spec.json
  podman run --security-opt label=disable --rm --read-only --network none -v "$PWD:/work:ro" ghcr.io/dolmen-go/openapi-preprocessor spec.yaml > spec.json
  ```

- Tags: exact `1.2.3`, minor `1.2`, major `1`, `latest`; pre-releases only
  under their exact version.
- Mounting: every file reached through `$ref` (including via `../`) must be
  inside the mount — mount the project root and pass a path relative to it.
  On SELinux hosts, Podman uses `--security-opt label=disable` rather than a
  `:Z` mount, which would permanently relabel the user's files.
- Permissions: the image runs as UID 65532; for files not world-readable use
  `--user "$(id -u):$(id -g)"` (Docker) or `--userns=keep-id` (Podman).
- Shell alias example to use the image like a locally installed command.
- `COPY --from=ghcr.io/dolmen-go/openapi-preprocessor:1 /usr/local/bin/openapi-preprocessor /usr/local/bin/`
- CI: GitHub Actions step running `docker run`; note that the image has no
  shell and cannot be used as a job image (GitLab `image:`, GitHub
  `container:`), with `COPY --from` / release binary as alternatives.

## One-time manual steps

- After the first publish, set the GHCR package visibility to **public**
  (new GHCR packages are private by default).

## Acceptance

Validated by tagging `v1.0.0-rc.2`:

- GitHub Release exists, marked pre-release, with 6 archives and
  `checksums.txt`; each binary's `-version` prints `v1.0.0-rc.2`.
- GHCR has `1.0.0-rc.2` only (no `latest`, `1`, `1.0`), as a multi-arch
  manifest for `linux/amd64` and `linux/arm64`.
- The README's standard invocation works on the published image.

## Out of scope

- Docker Hub publishing.
- A rolling `edge`/`master` image tag.
- A shell-based image variant (e.g. alpine) for use as a CI job image.
- A Dockerfile that builds from source (`docker build .`).
- Manual rebuild of an existing release (`workflow_dispatch`).
- Package manager distribution (Homebrew, deb/rpm, …), signing/SBOM.
