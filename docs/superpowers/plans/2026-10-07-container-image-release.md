# Container Image and Release Binaries Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On every `v*` tag, publish standalone binaries (with SBOMs and checksums) to a GitHub Release and a hardened multi-arch `scratch` image (with SBOM attestations) to `ghcr.io/dolmen-go/openapi-preprocessor`.

**Architecture:** GoReleaser v2 does everything from one config (`.goreleaser.yaml`): cross-compiled binaries, archives, syft SBOMs, checksums, GitHub Release, and a multi-arch image built by buildx from `.goreleaser/Dockerfile`, which only copies the prebuilt binary. A reusable smoke-test script (`.goreleaser/smoke-test.sh`) checks an image the way the README tells users to run it; it runs on a snapshot image in CI on every push, and on the published image in the release workflow.

**Tech Stack:** GoReleaser v2 (≥ 2.18), Docker buildx/BuildKit, syft, GitHub Actions, POSIX sh + jq, Podman for local checks.

**Spec:** `docs/superpowers/specs/2026-10-07-container-image-release-design.md`

## Global Constraints

- Registry: `ghcr.io/dolmen-go/openapi-preprocessor` only. No secrets beyond the built-in `GITHUB_TOKEN`.
- GoReleaser: `version: '~> v2'` in the action (config uses scoped annotations, which need ≥ v2.18).
- Release trigger: push of tags matching `v*`. **No `workflow_dispatch`.**
- Binaries: {linux, darwin, windows} × {amd64, arm64}; `CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w"`. **No `-X` version injection**: `version()` in `main.go` reads the VCS tag from the build info.
- Archives: `tar.gz` (linux, darwin), `zip` (windows), containing the binary, `LICENSE`, `README.md`, `man/man1/openapi-preprocessor.1`. Checksums file is named exactly `checksums.txt`.
- Image platforms: `linux/amd64`, `linux/arm64`. `FROM scratch`; binary at `/usr/local/bin/openapi-preprocessor`, owner `0:0`, mode `0555`; `WORKDIR /work`; `USER 65532:65532`; exec-form `ENTRYPOINT`.
- Image tags: `{{.Version}}` always; `{{.Major}}.{{.Minor}}`, `{{.Major}}`, `latest` only when not a pre-release.
- SBOMs: image attestation (`sbom: "true"`, explicit) and one syft SBOM per archive (GoReleaser `sboms:` with `artifacts: archive`).
- File locations: GoReleaser config at repo root (`.goreleaser.yaml`); Dockerfile at `.goreleaser/Dockerfile` (not at repo root).
- Action versions: `actions/checkout@v7`, `actions/setup-go@v7` (as in `test.yml`), `docker/setup-buildx-action@v4`, `docker/setup-qemu-action@v4`, `docker/login-action@v4`, `goreleaser/goreleaser-action@v7`, `anchore/sbom-action/download-syft@v0`.
- Go toolchain in CI: `go-version: stable` (confirmed by the user; spec updated). `go.mod` says `go 1.26.0`, and `go-version-file: go.mod` would build the release binaries with Go 1.26.0 exactly, missing later stdlib security fixes.
- Podman, for files readable only by their owner: `--userns=keep-id --user "$(id -u):$(id -g)"` (`--userns=keep-id` alone still runs as the image's user). On SELinux hosts: `--security-opt label=disable`, never a `:Z` mount in user-facing docs (it permanently relabels the user's files).
- Commit messages: plain sentence style as in `git log` (e.g. "Add release workflow"), ending with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Local tools are run with `mise exec <tool>@<version> -- …` (never `mise use`, which would modify the untracked `mise.toml`). There is no Docker locally, only Podman: everything buildx-specific is verified in CI.

## Review Focus

1. **A pre-release tag must not move `latest`, `1` or `1.0`.** Snapshot builds can't show this; it is checked by listing the registry tags after the `v1.0.0-rc.2` release (Task 6, step 4).
2. **The arm64 image must actually run.** CI runners are amd64; the release workflow runs the smoke test on the arm64 image under QEMU (Task 4, `PLATFORM=linux/arm64` run).
3. **Files readable only by their owner (0600/0700).** Users will hit "permission denied" with the default UID 65532; the README's `--user` (Docker) / `--userns=keep-id --user` (Podman) workaround is exercised by the smoke test, which also asserts that the default user *cannot* read them (Task 1).
4. **`$ref` to a file outside the mount (`../`).** Expected: non-zero exit, a "can't load" message on stderr, nothing on stdout; asserted by the smoke test (Task 1).
5. **Mis-stamped version** (dirty tree, wrong tag → `-version` prints a pseudo-version, or image tag keeps the leading `v`). Asserted on the published image by pulling `${TAG#v}` and comparing `-version` with the git tag (Task 4); archives checked in Task 6.

---

## File Structure

| Path | Status | Responsibility |
|---|---|---|
| `.goreleaser/Dockerfile` | create | Image definition; copies the prebuilt binary from `$TARGETPLATFORM/` in the GoReleaser build context |
| `.goreleaser/smoke-test.sh` | create | Checks an image: functional runs as documented, permissions, `$ref` outside the mount, `-version`, image user and file modes. Engine-agnostic (Docker/Podman) |
| `.goreleaser.yaml` | create | GoReleaser config: builds, archives, SBOMs, checksums, release, image |
| `.github/workflows/test.yml` | modify | Add `release-dry-run` job |
| `.github/workflows/release.yml` | create | Release on `v*` tag + post-publish checks |
| `README.md` | modify | "Download a binary" and "Run with Docker or Podman" subsections under "Install" |

---

### Task 1: Image definition and smoke test script

**Files:**
- Create: `.goreleaser/smoke-test.sh`
- Create: `.goreleaser/Dockerfile`

**Interfaces:**
- Consumes: `testdata/10-ref-ext/` (its `input.yml` has `$ref: ../common/info.yml#/info`), `testdata/common/`, `testdata/11-ref-relative/`; each case has `input.yml` and the expected `result.json`.
- Produces:
  - `.goreleaser/smoke-test.sh <image> [<expected -version output>]` — exit 0 when all checks pass, prints `ok - …` lines; on failure prints `FAIL: …` to stderr and exits 1. Environment: `CONTAINER_ENGINE` (`docker` default, or `podman`), `PLATFORM` (optional, e.g. `linux/arm64`, passed as `--platform` to `run`), `MOUNT_OPTS` (default `ro`; `ro,Z` on SELinux hosts). Requires `jq` and GNU `tar`.
  - `.goreleaser/Dockerfile` — expects the binary at `$TARGETPLATFORM/openapi-preprocessor` in the build context (GoReleaser `dockers_v2` layout).

- [ ] **Step 1: Write the smoke test script**

Create `.goreleaser/smoke-test.sh`:

```sh
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
```

Then: `chmod +x .goreleaser/smoke-test.sh`

- [ ] **Step 2: Lint the script**

Run: `mise exec shellcheck@latest -- shellcheck .goreleaser/smoke-test.sh`
Expected: no output, exit 0.

- [ ] **Step 3: Run the script without an image to verify it fails**

Run: `CONTAINER_ENGINE=podman .goreleaser/smoke-test.sh localhost/openapi-preprocessor:test; echo "exit=$?"`
Expected: a Podman error from `create` (the image can't be found or pulled), no `ok - …` line, and a non-zero exit (`exit=125`).

- [ ] **Step 4: Write the Dockerfile**

Create `.goreleaser/Dockerfile`:

```dockerfile
# Used by GoReleaser only (see dockers_v2 in ../.goreleaser.yaml): it copies a
# binary prebuilt for $TARGETPLATFORM and cannot build from a source checkout.
FROM scratch
ARG TARGETPLATFORM
COPY --chown=0:0 --chmod=0555 $TARGETPLATFORM/openapi-preprocessor /usr/local/bin/openapi-preprocessor
WORKDIR /work
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/openapi-preprocessor"]
```

- [ ] **Step 5: Build the image locally with Podman, laid out like GoReleaser's build context**

```bash
ctx=$(mktemp -d)
mkdir -p "$ctx/linux/amd64"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o "$ctx/linux/amd64/openapi-preprocessor" .
podman build --platform linux/amd64 -f .goreleaser/Dockerfile -t localhost/openapi-preprocessor:test "$ctx"
rm -rf "$ctx"
```

Expected: build succeeds, image `localhost/openapi-preprocessor:test` exists (`podman images localhost/openapi-preprocessor`).

- [ ] **Step 6: Run the smoke test against the local image**

Run: `CONTAINER_ENGINE=podman .goreleaser/smoke-test.sh localhost/openapi-preprocessor:test`
Expected (exit 0):

```
ok - image user is 65532:65532
ok - image files are read-only for the image's user
ok - 10-ref-ext
ok - 11-ref-relative
ok - ref outside of the mount fails
ok - private files not readable by the default user
ok - private files readable with --userns=keep-id --user=<uid>:<gid>
ok - -version: v1.0.0-rc.1.0.<…>+dirty
```

The local `-version` is a pseudo-version with `+dirty` (untracked `mise.toml`); that is expected here.
Buildah (Podman) does not store the `WORKDIR` in the image, BuildKit does: that is why the `work` entry is only checked when present.

- [ ] **Step 6b: Check that the smoke test catches broken images**

Build two variants of the image, from the same build context as Step 5: one with `--chmod=0777` instead of `--chmod=0555` (`sed 's/--chmod=0555/--chmod=0777/' .goreleaser/Dockerfile`), one without the `USER` line (`sed '/^USER/d' .goreleaser/Dockerfile`), and run the smoke test on each.
Expected: `FAIL: image file usr/local/bin/openapi-preprocessor: want -r-xr-xr-x 0/0, …` for the first, `FAIL: image user: got '', want '65532:65532'` for the second. Remove both images.

Podman ignores `--platform` for local multi-platform images in `create`, so the per-platform hardening checks (`PLATFORM=linux/arm64`) can't be checked locally: they are exercised by the release workflow (Docker).

- [ ] **Step 7: Clean up and commit**

```bash
podman rmi localhost/openapi-preprocessor:test
git add .goreleaser/Dockerfile .goreleaser/smoke-test.sh
git commit -F - <<'EOF'
Add container image definition and its smoke test

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```

---

### Task 2: GoReleaser configuration

**Files:**
- Create: `.goreleaser.yaml`

**Interfaces:**
- Consumes: `.goreleaser/Dockerfile` (Task 1).
- Produces (in `dist/`, used by Tasks 3 and 4):
  - `dist/metadata.json` with a `.version` field (version without leading `v`).
  - Snapshot images loaded in the local Docker daemon as `ghcr.io/dolmen-go/openapi-preprocessor:<version>-amd64` and `…-arm64`.
  - 6 archives `openapi-preprocessor_<version>_<os>_<arch>.{tar.gz,zip}`, 6 SBOMs `<archive>.sbom.json`, `checksums.txt`.

- [ ] **Step 1: Write the config**

Create `.goreleaser.yaml`:

```yaml
# GoReleaser config: https://goreleaser.com/customization/
# Releases are made by .github/workflows/release.yml on push of a v* tag.
# Local dry run: goreleaser release --snapshot --clean
version: 2

project_name: openapi-preprocessor

builds:
  - env:
      - CGO_ENABLED=0
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    flags:
      - -trimpath
    # No -X: the version is read from the build info (see version() in main.go)
    ldflags:
      - -s -w

archives:
  - formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
    files:
      - LICENSE
      - README.md
      - man/man1/openapi-preprocessor.1

sboms:
  - artifacts: archive

checksum:
  name_template: checksums.txt

release:
  prerelease: auto

dockers_v2:
  - dockerfile: .goreleaser/Dockerfile
    images:
      - ghcr.io/dolmen-go/openapi-preprocessor
    tags:
      - "{{ .Version }}"
      - "{{ if not .Prerelease }}{{ .Major }}.{{ .Minor }}{{ end }}"
      - "{{ if not .Prerelease }}{{ .Major }}{{ end }}"
      - "{{ if not .Prerelease }}latest{{ end }}"
    platforms:
      - linux/amd64
      - linux/arm64
    sbom: "true"
    labels:
      org.opencontainers.image.title: "{{ .ProjectName }}"
      org.opencontainers.image.description: Preprocessor for OpenAPI specifications ($ref to external files, $inline, $merge)
      org.opencontainers.image.source: https://github.com/dolmen-go/openapi-preprocessor
      org.opencontainers.image.version: "{{ .Version }}"
      org.opencontainers.image.revision: "{{ .FullCommit }}"
      org.opencontainers.image.created: "{{ .Date }}"
      org.opencontainers.image.licenses: Apache-2.0
    # For a multi-arch image, GHCR reads these from the index.
    # Snapshot builds are single-platform (no index): an empty value drops them.
    annotations:
      "index:org.opencontainers.image.description": "{{ if not .IsSnapshot }}Preprocessor for OpenAPI specifications ($ref to external files, $inline, $merge){{ end }}"
      "index:org.opencontainers.image.source": "{{ if not .IsSnapshot }}https://github.com/dolmen-go/openapi-preprocessor{{ end }}"
      "index:org.opencontainers.image.licenses": "{{ if not .IsSnapshot }}Apache-2.0{{ end }}"
```

The `index:` annotations must not reach snapshot builds: these are single-platform `--load` builds, and BuildKit rejects index annotations for a single-platform export.

- [ ] **Step 2: Validate the config**

Run: `mise exec goreleaser@2.18.2 -- goreleaser check`
Expected: `1 configuration file(s) validated` and exit 0, no deprecation warnings. Fix any reported error before going on.

- [ ] **Step 3: Snapshot build without the image (no Docker locally)**

Run: `mise exec goreleaser@2.18.2 syft@1.51.1 -- goreleaser release --snapshot --clean --skip=docker`
Expected: exit 0. (If `docker` is not accepted by `--skip`, run `goreleaser release --help` and use the value listed for Docker images.)

- [ ] **Step 4: Verify the produced artifacts**

```bash
ls dist/*.tar.gz dist/*.zip | wc -l                 # expect 6
ls dist/*.sbom.json | wc -l                         # expect 6
grep -c '\.sbom\.json$' dist/checksums.txt          # expect 6
grep -cE '\.(tar\.gz|zip)$' dist/checksums.txt      # expect 6
jq -r .version dist/metadata.json                   # expect a version without leading v
tar -tzf dist/openapi-preprocessor_*_linux_amd64.tar.gz | sort
# expect: LICENSE README.md man/man1/openapi-preprocessor.1 openapi-preprocessor
unzip -l dist/openapi-preprocessor_*_windows_arm64.zip   # expect openapi-preprocessor.exe + same 3 files
jq -r '.packages[].name' dist/openapi-preprocessor_*_linux_amd64.tar.gz.sbom.json | sort -u
# expect at least: github.com/dolmen-go/jsonptr github.com/dolmen-go/openapi-preprocessor
#                  github.com/mohae/deepcopy go.yaml.in/yaml/v3 stdlib
dist/openapi-preprocessor_linux_amd64_v1/openapi-preprocessor -version   # expect a pseudo-version, not (devel)/(unknown)
git status --porcelain                               # expect only "?? mise.toml" (dist/ is ignored)
```

With GoReleaser 2.18.2, `checksums.txt` lists the SBOM files without extra config.

- [ ] **Step 5: Check the image build commands with a logging `docker` shim**

No Docker locally: put first in `PATH` a fake `docker` script that appends its arguments to a log file and exits 0 (answering `info` with `{}`, `buildx version` with a buildx version line, `buildx inspect`/`ls` with `Driver: docker-container`). GoReleaser then fails on the missing image ID file, after logging the `buildx build` commands.

1. `goreleaser release --snapshot --clean` in the repo. Expected: 2 single-platform `buildx build … --load` commands, **no** `--annotation index:…`.
2. A tagged release, in a throwaway clone (scratch directory) with `git remote add origin https://github.com/example/example.git` and a tag `v1.0.0-rc.2`: `GITHUB_TOKEN=invalid-fake-token goreleaser release --clean --skip=announce,validate` (not `--skip=publish`: `dockers_v2` builds only when publishing). Nothing can be published: `docker` is the shim and the token and remote are fake. Expected: one `buildx build --platform linux/amd64,linux/arm64 … --push --attest=type=sbom` with the 3 `--annotation index:…` and `-t ghcr.io/dolmen-go/openapi-preprocessor:1.0.0-rc.2` only (no `latest`, `1`, `1.0`). Delete the clone.

- [ ] **Step 6: Un-ignore the config and commit**

`.gitignore` ignores `/*.yaml` at the root, which includes `.goreleaser.yaml`: append the line `!/.goreleaser.yaml` to `.gitignore`, and check with `git status --porcelain` that `.goreleaser.yaml` shows up as untracked.

```bash
git add .gitignore .goreleaser.yaml
git commit -F - <<'EOF'
Add GoReleaser config for release binaries and container image

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```

---

### Task 3: Release dry run in CI

**Files:**
- Modify: `.github/workflows/test.yml` (append a job at the end of `jobs:`)

**Interfaces:**
- Consumes: `.goreleaser.yaml` and `dist/metadata.json` (Task 2), `.goreleaser/smoke-test.sh` (Task 1).
- Produces: a `release-dry-run` job that fails on a broken GoReleaser config, Dockerfile or image.

- [ ] **Step 1: Add the job**

Append to `.github/workflows/test.yml`, after the `quality` job (same indentation as `quality:`). The job belongs in `test.yml`, not in `release.yml`: `needs: [ test ]` only works within a workflow, and `release.yml` stays triggered by tags only.

```yaml
  # Build the release as release.yml does, without publishing it
  release-dry-run:
    needs: [ test ]
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v7
        with:
          fetch-depth: 0

      - uses: actions/setup-go@v7
        with:
          go-version: stable

      - uses: docker/setup-buildx-action@v4

      # For the SBOMs of the archives
      - uses: anchore/sbom-action/download-syft@v0

      - name: Check GoReleaser config
        uses: goreleaser/goreleaser-action@v7
        with:
          version: '~> v2'
          args: check

      - name: Build release (snapshot)
        uses: goreleaser/goreleaser-action@v7
        with:
          version: '~> v2'
          args: release --snapshot --clean

      - name: Smoke test the image
        run: .goreleaser/smoke-test.sh "ghcr.io/dolmen-go/openapi-preprocessor:$(jq -r .version dist/metadata.json)-amd64"
```

- [ ] **Step 2: Lint the workflow**

Run: `mise exec actionlint@latest shellcheck@latest -- actionlint .github/workflows/test.yml`
Expected: only the 2 `[matrix]` errors that already exist on `master` (the anchor-based `exclude` of `test-others`), nothing about `release-dry-run`.

- [ ] **Step 3: No commit yet**

This job is committed together with Task 4's workflow (Task 4, Step 3).

- [ ] **Step 4: Run it in CI**

Pushing is outward-facing: **ask the user for permission** to push the branch, then `git push -u origin container-image`.
Check the result with `gh run list --branch container-image --workflow Test` / `gh run watch` if `gh` is authenticated; otherwise ask the user to report the `release-dry-run` job result from the Actions tab.
Expected: `release-dry-run` passes and its last step prints the 8 `ok - …` lines of Task 1, Step 6 (`-version` shows a pseudo-version).

Snapshot builds pass no `--attest` (no image SBOM), so `sbom: "true"` does not get in the way of `--load`; the image SBOM is checked on release, in Task 4.

---

### Task 4: Release workflow

**Files:**
- Create: `.github/workflows/release.yml`

**Interfaces:**
- Consumes: `.goreleaser.yaml` (Task 2), `.goreleaser/smoke-test.sh` (Task 1).
- Produces: GitHub Release + `ghcr.io/dolmen-go/openapi-preprocessor:<version without v>` on push of a `v*` tag.

- [ ] **Step 1: Write the workflow**

Create `.github/workflows/release.yml`:

```yaml
name: Release

# To release: git tag -a vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z
# Tags with a pre-release suffix (ex: v1.0.0-rc.2) make a GitHub pre-release
# and are not published as the latest image.
# The release is also built (snapshot, not published) by the release-dry-run
# job of test.yml on every push.
on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write  # GitHub Release
  packages: write  # ghcr.io

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v7
        with:
          fetch-depth: 0

      - uses: actions/setup-go@v7
        with:
          go-version: stable

      - name: Run tests
        run: go test ./...

      - uses: docker/setup-buildx-action@v4

      # For the SBOMs of the archives
      - uses: anchore/sbom-action/download-syft@v0

      - uses: docker/login-action@v4
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Release
        uses: goreleaser/goreleaser-action@v7
        with:
          version: '~> v2'
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}

      # To run the arm64 image
      - uses: docker/setup-qemu-action@v4

      - name: Check the published image
        env:
          TAG: ${{ github.ref_name }}
        run: |
          image="ghcr.io/dolmen-go/openapi-preprocessor:${TAG#v}"
          for platform in linux/amd64 linux/arm64; do
            echo "# $platform"
            docker pull --platform "$platform" "$image"
            PLATFORM="$platform" .goreleaser/smoke-test.sh "$image" "$TAG"
          done
          echo "# SBOM"
          docker buildx imagetools inspect "$image" --format '{{ json .SBOM }}' > sbom.json
          for platform in linux/amd64 linux/arm64; do
            for pkg in github.com/dolmen-go/openapi-preprocessor github.com/dolmen-go/jsonptr github.com/mohae/deepcopy go.yaml.in/yaml/v3 stdlib; do
              jq -e --arg p "$platform" --arg pkg "$pkg" '.[$p].SPDX.packages | map(.name) | index($pkg)' sbom.json > /dev/null \
                || { echo "FAIL: SBOM $platform: missing $pkg" >&2; exit 1; }
            done
            echo "ok - SBOM $platform"
          done
```

- [ ] **Step 2: Lint the workflow**

Run: `mise exec actionlint@latest -- actionlint .github/workflows/release.yml`
Expected: no output, exit 0 (actionlint also runs shellcheck on the `run:` scripts if shellcheck is on `PATH`: run it as `mise exec actionlint@latest shellcheck@latest -- actionlint .github/workflows/release.yml`).

- [ ] **Step 3: Commit**

One commit for Tasks 3 and 4:

```bash
git add .github/workflows/release.yml .github/workflows/test.yml
git commit -F - <<'EOF'
Add release workflow and a release dry run in CI

On push of a v* tag, release.yml runs GoReleaser to publish the binaries,
the SBOMs and the container image, which is then checked on both platforms.

On every push and pull request, the new release-dry-run job of test.yml
builds a snapshot release (not published), after the unit tests, and
smoke-tests its image.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```

This workflow can only run on a real tag: it is exercised in Task 6.

---

### Task 5: README

**Files:**
- Modify: `README.md` — insert two subsections at the end of `## Install`, i.e. just before the line `## Usage`.

**Interfaces:**
- Consumes: image name, tags, mount point, UID, binary path, SBOM commands from the Global Constraints. Every command written here must match what `.goreleaser/smoke-test.sh` runs.

- [ ] **Step 1: Insert the subsections**

Insert before `## Usage` (the README uses 4-space indented code blocks with `$ ` prompts; keep that style):

````markdown
### Download a binary

Binaries for Linux, macOS and Windows, on amd64 and arm64, are attached to each
[GitHub release](https://github.com/dolmen-go/openapi-preprocessor/releases).
Each archive contains the `openapi-preprocessor` binary, this README, the license
and the man page.

Each release also has a `checksums.txt` file (SHA-256) and, for each archive, an
SBOM (`<archive>.sbom.json`, SPDX format) listing the Go modules the binary is
built from.

Verify the archives you downloaded:

    $ sha256sum --ignore-missing -c checksums.txt

### Run with Docker or Podman

A minimal image (just the static binary: no shell, runs as an unprivileged user)
is published for `linux/amd64` and `linux/arm64` on the GitHub Container Registry:

    $ docker run --rm --read-only --network none -v "$PWD:/work:ro" ghcr.io/dolmen-go/openapi-preprocessor spec.yaml > spec.json

With Podman (`--security-opt label=disable` lets the container read your files
on hosts with SELinux, such as Fedora or RHEL, without relabeling them):

    $ podman run --security-opt label=disable --rm --read-only --network none -v "$PWD:/work:ro" ghcr.io/dolmen-go/openapi-preprocessor spec.yaml > spec.json

The tool only reads its input files and writes the result on its standard output,
so it works with a read-only container, a read-only mount and no network.

Tags:

- `1.2.3`: a release
- `1.2`, `1`: the latest release of that minor or major version
- `latest`: the latest release

Pre-releases (ex: `1.0.0-rc.2`) are only published under their exact version.

The mounted directory is `/work`, the working directory of the container: give
the path of your spec relative to it. Every file reached with `$ref`, including
through `../`, must be inside the mounted directory, so mount the root of your
project and give the path of the spec from there:

    $ docker run --rm --read-only --network none -v "$PWD:/work:ro" ghcr.io/dolmen-go/openapi-preprocessor api/spec.yaml > api.json

The container runs as UID 65532, so your files must be readable by others (as
with the usual 644 and 755 permissions). For files that only you can read, run
the container as yourself:

    $ docker run --user "$(id -u):$(id -g)" --rm --read-only --network none -v "$PWD:/work:ro" ghcr.io/dolmen-go/openapi-preprocessor spec.yaml > spec.json
    $ podman run --userns=keep-id --user "$(id -u):$(id -g)" --security-opt label=disable --rm --read-only --network none -v "$PWD:/work:ro" ghcr.io/dolmen-go/openapi-preprocessor spec.yaml > spec.json

Use it like a locally installed command:

    $ alias openapi-preprocessor='docker run --rm --read-only --network none -v "$PWD:/work:ro" ghcr.io/dolmen-go/openapi-preprocessor'
    $ openapi-preprocessor spec.yaml > spec.json

Copy the binary into your own image:

    COPY --from=ghcr.io/dolmen-go/openapi-preprocessor:1 /usr/local/bin/openapi-preprocessor /usr/local/bin/

In a GitHub Actions workflow:

    - name: Build the OpenAPI spec
      run: docker run --rm --read-only --network none -v "$PWD:/work:ro" ghcr.io/dolmen-go/openapi-preprocessor:1 api/spec.yaml > api.json

As the image has no shell, it can't be the image of a CI job (GitLab CI `image:`,
GitHub Actions `container:`). Instead, run it with `docker run` as above, copy the
binary into your own CI image with `COPY --from`, or download a release binary.

The image has an SBOM attached (SPDX format, one for each platform):

    $ docker buildx imagetools inspect ghcr.io/dolmen-go/openapi-preprocessor:1 --format '{{ json .SBOM }}'

````

- [ ] **Step 2: Check consistency with the smoke test**

Compare each `docker run`/`podman run` command above with `run_tool` and `owner_opt` in `.goreleaser/smoke-test.sh`: same options (`--rm --read-only --network none`, `-v "<dir>:/work:ro"`, `--user "$(id -u):$(id -g)"`, plus `--userns=keep-id` for Podman). Fix the README if they differ. The README has no `:Z` (see Global Constraints).

- [ ] **Step 3: Run the documented Podman commands locally against the Task 1 image**

Rebuild `localhost/openapi-preprocessor:test` as in Task 1, Step 5. Then extract each `$ podman run` line and the `$ alias` line from the README, replace the image name with `localhost/openapi-preprocessor:test` (and `docker run` with `podman run` in the alias), replace `spec.yaml > spec.json` with `10-ref-ext/input.yml`, and run them:

- from `testdata/`: the plain command and the alias (with `11-ref-relative/input.yml`, in a new `bash -O expand_aliases -c` where the alias is defined on a line of its own) → output equals `result.json` after `jq -S .` (`SAME`);
- from a copy of `testdata/10-ref-ext` and `testdata/common` with `chmod -R go-rwx`, in a directory readable by others: the plain command fails (`permission denied`, as documented), the `--userns=keep-id --user …` command → `SAME`.

Then `podman rmi localhost/openapi-preprocessor:test`.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -F - <<'EOF'
README: install from release binaries, run with Docker or Podman

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```

---

### Task 6: First release (acceptance) — done with the user

Every step here is outward-facing (merge, tag, publish): **the user runs it or explicitly approves each step.**

- [ ] **Step 1: Merge**

The `container-image` branch is merged into `master` (PR or local merge — user's choice, see superpowers:finishing-a-development-branch) and `master` is pushed. The `Test` workflow, including `release-dry-run`, passes on `master`.

- [ ] **Step 2: Tag the pre-release**

```bash
git tag -a v1.0.0-rc.2 -m v1.0.0-rc.2
git push origin v1.0.0-rc.2
```

Expected: the `Release` workflow passes, including "Check the published image" (16 `ok - …` lines for the two platforms, then `ok - SBOM linux/amd64` and `ok - SBOM linux/arm64`).

- [ ] **Step 3: Make the package public (one time)**

In https://github.com/orgs/dolmen-go/packages/container/openapi-preprocessor/settings, "Change visibility" → Public. Check that the package page links to the repository and shows the description.

- [ ] **Step 4: Check the published artifacts**

```bash
gh release view v1.0.0-rc.2 --json isPrerelease,assets --jq '.isPrerelease, (.assets | length)'
# expect: true, 13 (6 archives + 6 SBOMs + checksums.txt)
podman search --list-tags ghcr.io/dolmen-go/openapi-preprocessor
# expect: only 1.0.0-rc.2 (no latest, 1, 1.0)
podman manifest inspect ghcr.io/dolmen-go/openapi-preprocessor:1.0.0-rc.2 | jq -r '.manifests[].platform | "\(.os)/\(.architecture)"'
# expect: linux/amd64, linux/arm64 (+ unknown/unknown attestation entries)
d=$(mktemp -d) && cd "$d"
gh release download v1.0.0-rc.2 -R dolmen-go/openapi-preprocessor
sha256sum -c checksums.txt                                       # expect all OK
tar -xzf openapi-preprocessor_1.0.0-rc.2_linux_amd64.tar.gz && ./openapi-preprocessor -version   # expect v1.0.0-rc.2
unzip -l openapi-preprocessor_1.0.0-rc.2_windows_amd64.zip        # expect openapi-preprocessor.exe
```

- [ ] **Step 5: Try the README commands**

From a project with a spec, run the Podman command and the alias from the README against `ghcr.io/dolmen-go/openapi-preprocessor:1.0.0-rc.2`. Expected: same output as a locally built `openapi-preprocessor`.
