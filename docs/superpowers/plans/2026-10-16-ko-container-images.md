# Container Images Built with ko Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** GoReleaser builds and pushes the container images with ko (`kos`) on a base image of our own, and `reproduce.sh` checks archives, images and image binaries against a local registry, without Docker or Podman.

**Architecture:** A new [`base.sh`][base.sh] builds the base image with crane; the user publishes it once on GHCR and [`.goreleaser.yaml`][goreleaser.yaml] pins it by digest. `kos` replaces `dockers_v2`; `release.disable` turns off the GitHub Release whenever `KO_DOCKER_REPO` sends the images elsewhere. [`reproduce.sh`][reproduce.sh] publishes into an in-memory crane registry and compares digests; the workflows lose buildx and the double build.

**Tech Stack:** GoReleaser v2 (2.18.3 here, ko 0.19.1 embedded), crane v0.22.1 (go-containerregistry), POSIX sh, jq, GNU tar, Podman (local smoke test only), GitHub Actions.

**Spec:** [`docs/superpowers/specs/2026-10-16-ko-container-images-design.md`][spec]

[base.sh]: ../../../.goreleaser/base.sh
[goreleaser.yaml]: ../../../.goreleaser.yaml
[reproduce.sh]: ../../../.goreleaser/reproduce.sh
[spec]: ../specs/2026-10-16-ko-container-images-design.md

## Global Constraints

- Go stays `go-version: stable`, GoReleaser `version: '~> v2'`: no pinning.
- crane: module `github.com/google/go-containerregistry/cmd/crane`, version `v0.22.1`, defined once at the top of each file that uses it (`base.sh`, `reproduce.sh`, `release.yml`), each with a comment naming the others.
- Base image: `ghcr.io/dolmen-go/openapi-preprocessor-base`; one layer `work/` (`0755`, `0:0`, mtime 0, ustar); config `WorkingDir=/work`, `User=65532:65532`; platforms `linux/amd64`, `linux/arm64`.
- Image: `ghcr.io/dolmen-go/openapi-preprocessor`, platforms `linux/amd64`, `linux/arm64`, binary `/ko-app/openapi-preprocessor`.
- SBOM packages required on each platform: the modules linked into the binary, from `go list -deps -f '{{with .Module}}{{.Path}}{{end}}' .` with `GOOS=linux` and the platform's `GOARCH`; never a hardcoded list.
- `TZ=UTC` for every build whose image digests are compared.
- Shell scripts: POSIX `sh`, `set -eu`, tab indentation, clean under `shellcheck`, in the style of the existing `.goreleaser/*.sh` (`fail()`, `ok - …` lines, `# …` info lines).
- Use the `go` and `goreleaser` on `PATH` (go1.27.3, GoReleaser 2.18.3 here). No `mise` commands: they can upgrade the user's tools. No Docker on this machine: the snapshot path (ko loading into a Docker daemon) is only verified in CI (Task 8).
- `$SCRATCH` = the session's scratchpad directory. Throwaway clones, tags, registries and binaries live there, never in the repository. Throwaway tags are made only in scratch clones of the local repository (`git clone` of the working copy, never GitHub).
- Only the user writes to GHCR (Task 2) and pushes. Don't touch the untracked `.config/`.
- Commits: plain-sentence subject as in `git log`, message written to a file in `$SCRATCH` and committed with `git commit -F`, ending with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Fixes to an earlier task's commit on this unpushed branch are folded into it (`git commit --fixup=<sha>`, then `GIT_SEQUENCE_EDITOR=: git rebase -i --autosquash master`).

## Review Focus

1. **The image name the CI dry run smoke-tests.** In snapshot mode GoReleaser records ko's daemon tag (`goreleaser.ko.local:<hex>`, per GoReleaser 2.18's `internal/pipe/ko`), a path never run locally. If the selection finds nothing, the smoke test must fail with a clear message, not run `smoke-test.sh ""`. Task 4 adds that assertion; Task 8 runs it in CI.
2. **The user's time zone** (`Europe/Paris` here) changes ko's image `created` time, hence every digest. Task 6 runs `reproduce.sh` with `TZ=Pacific/Auckland` and requires the same index digest as in UTC.
3. **An interrupted `reproduce.sh`** must not leave the crane registry running. Task 6 sends `TERM` and `HUP` to a run and requires the registry port to be closed afterwards.
4. **A `GITHUB_TOKEN` in the user's environment** must not let a local run touch the GitHub Release. Tasks 3 and 6 run with `GITHUB_TOKEN=bogus` and require success (any GitHub API call would fail with that token).
5. **The user's umask** (`0002` here) must not change the base image. Task 1 builds under umask `0002` and `0022` and requires the same digest.

---

## File Structure

| Path | Status | Responsibility |
|---|---|---|
| [`.goreleaser/base.sh`][base.sh] | create | Builds and pushes the base image with crane, prints its digest |
| [`.goreleaser.yaml`][goreleaser.yaml] | modify | `kos` replaces `dockers_v2`; `release.disable` hook |
| [`.goreleaser/smoke-test.sh`][smoke-test.sh] | modify | ko image layout; no file-date checks |
| [`.github/workflows/test.yml`][test.yml] | modify | `release-dry-run`: one snapshot build, archive checks, smoke test |
| [`.github/workflows/release.yml`][release.yml] | modify | No buildx, `TZ: UTC`, SBOM check on ko's SBOM |
| [`.goreleaser/reproduce.sh`][reproduce.sh] | rewrite | Local publish into a crane registry; archive, image and binary checks |
| `.goreleaser/fingerprint.sh`, `.goreleaser/Dockerfile` | delete | No users left |
| [`README.md`][README.md] | modify | `COPY --from` path, reading ko's SBOM |
| [2026-10-07 spec][spec-1007], [2026-10-09 spec][spec-1009] | modify | "Partially superseded" notice |

[base.sh]: ../../../.goreleaser/base.sh
[goreleaser.yaml]: ../../../.goreleaser.yaml
[smoke-test.sh]: ../../../.goreleaser/smoke-test.sh
[test.yml]: ../../../.github/workflows/test.yml
[release.yml]: ../../../.github/workflows/release.yml
[reproduce.sh]: ../../../.goreleaser/reproduce.sh
[README.md]: ../../../README.md
[spec-1007]: ../specs/2026-10-07-container-image-release-design.md
[spec-1009]: ../specs/2026-10-09-reproducible-release-design.md

## Scratch procedures

Used by several tasks; each task says which.

**P1 — crane and a scratch registry.** Build crane once, start a registry, read its port, stop it by PID (never `pkill -f`: it can match the calling shell):

```sh
GOBIN=$SCRATCH/bin go install github.com/google/go-containerregistry/cmd/crane@v0.22.1
"$SCRATCH/bin/crane" registry serve --address 127.0.0.1:0 > "$SCRATCH/registry.log" 2>&1 &
echo $! > "$SCRATCH/registry.pid"
sleep 1; port=$(sed -n 's/.*serving on port \([0-9]*\).*/\1/p' "$SCRATCH/registry.log"); echo "$port"
# when done:
kill "$(cat "$SCRATCH/registry.pid")"
```

**P2 — a scratch clone at a throwaway tag.** From the working copy, with the branch's committed state:

```sh
rm -rf "$SCRATCH/clone"
git clone -q --branch ko-images "$PWD" "$SCRATCH/clone"
git -C "$SCRATCH/clone" remote set-url origin https://github.com/dolmen-go/openapi-preprocessor.git
git -C "$SCRATCH/clone" tag -a -m 'Throwaway' v0.0.1-ko.1
```

**P3 — publish the clone into the scratch registry** (needs P1 and P2, and Task 3's config):

```sh
cd "$SCRATCH/clone" && TZ=UTC GITHUB_TOKEN=bogus KO_DOCKER_REPO=127.0.0.1:$port/openapi-preprocessor \
  GORELEASER_CURRENT_TAG=v0.0.1-ko.1 goreleaser release --clean --skip=announce,sbom
```

The images are then at `127.0.0.1:$port/openapi-preprocessor:0.0.1-ko.1`.

---

### Task 1: Base image script

**Files:**
- Create: `.goreleaser/base.sh` (mode `0755`)

**Interfaces:**
- Produces: `.goreleaser/base.sh <reference>`. `<reference>` is `<repository>:<tag>`; a reference without a tag, or with a digest, is a usage error (exit 2). Pushes a multi-platform index to `<reference>` and prints its digest (`sha256:<hex>`) alone on standard output; crane's output goes to standard error or `/dev/null`. Needs only Go and GNU `tar`; reads registry credentials from Docker's or Podman's files (crane's keychain).

- [ ] **Step 1: Write `base.sh`**

Header comment: what the image is (`/work` owned by root, user `65532:65532`, nothing else), usage, that the digest depends on the crane and Go versions, that `.goreleaser.yaml` pins it, and the publication command from the spec's Delivery section. Constants at the top: `crane_version=v0.22.1`, `platforms="linux/amd64 linux/arm64"`.

Body:
- `crane() { go run "github.com/google/go-containerregistry/cmd/crane@$crane_version" "$@"; }` (no background process here, so `go run` is fine).
- Temporary directory with an `EXIT` trap; `mkdir -m 0755 "$tmp/work"` (exact mode whatever the umask); `tar -C "$tmp" --format=ustar --owner=0 --group=0 --numeric-owner --mtime=@0 -cf "$tmp/work.tar" work`.
- `crane append --oci-empty-base -f "$tmp/work.tar" -t "$reference"`: the layer image, pushed to `<reference>` itself so that no other tag is created; the final index overwrites it.
- For each platform: `crane mutate "$reference" --set-platform "$platform" -w /work -u 65532:65532 --repo "${reference%:*}"`, which prints the pushed `<repository>@sha256:…`; collect them as `-m` options.
- `crane index append -m … -m … -t "$reference"`, then `crane digest "$reference"` on standard output.

- [ ] **Step 2: Lint**

Run: `shellcheck .goreleaser/base.sh`
Expected: no output.

- [ ] **Step 3: Usage errors**

Run: `.goreleaser/base.sh; echo "rc=$?"; .goreleaser/base.sh 127.0.0.1:5000/base; echo "rc=$?"; .goreleaser/base.sh 127.0.0.1:5000/base@sha256:0000000000000000000000000000000000000000000000000000000000000000; echo "rc=$?"`
Expected: a usage message and `rc=2` three times, nothing pushed.

- [ ] **Step 4: Two builds, other umasks and time zones (Review Focus 5)**

With P1 running: `(umask 0002; TZ=Europe/Paris .goreleaser/base.sh "127.0.0.1:$port/base:latest")` and `(umask 0022; TZ=UTC .goreleaser/base.sh "127.0.0.1:$port/base2:latest")`.
Expected: the same `sha256:…` printed twice.

- [ ] **Step 5: Content**

Run, with `c=$SCRATCH/bin/crane` and `r=127.0.0.1:$port/base:latest`:
- `$c manifest "$r" | jq -c '[.manifests[].platform]'` → `[{"architecture":"amd64","os":"linux"},{"architecture":"arm64","os":"linux"}]`
- `$c config --platform linux/arm64 "$r" | jq -c '[.architecture, .config.WorkingDir, .config.User]'` → `["arm64","/work","65532:65532"]`
- `$c export --platform linux/amd64 "$r" - | TZ=UTC tar -tv --full-time --numeric-owner -f -` → one line: `drwxr-xr-x 0/0 0 1970-01-01 00:00:00 work/`
- `$c ls 127.0.0.1:$port/base` → `latest` only.

Stop P1.

- [ ] **Step 6: Commit**

```sh
git add .goreleaser/base.sh
git commit -F "$SCRATCH/msg-task1.txt"   # "Base image for the ko-built images, built with crane"
```

---

### Task 2: First publication of the base (the user)

**Files:** none.

**Interfaces:**
- Consumes: `.goreleaser/base.sh` (Task 1).
- Produces: `ghcr.io/dolmen-go/openapi-preprocessor-base:latest`, public, and its digest `sha256:<hex>`, used by Task 3 as `<base digest>`.

- [ ] **Step 1: Hand over to the user**

Stop and ask the user to run, from this working copy:
1. `podman login ghcr.io` (or `docker login ghcr.io`) with a token that can write packages;
2. `.goreleaser/base.sh ghcr.io/dolmen-go/openapi-preprocessor-base:latest`, with the Go on `PATH`;
3. in the settings of the GHCR package `openapi-preprocessor-base`: make it public;

then to paste the printed digest. Wait for it.

- [ ] **Step 2: Check it is public and matches**

Run: `mkdir -p "$SCRATCH/noauth" && DOCKER_CONFIG=$SCRATCH/noauth REGISTRY_AUTH_FILE=$SCRATCH/noauth/auth.json XDG_RUNTIME_DIR=$SCRATCH/noauth "$SCRATCH/bin/crane" digest ghcr.io/dolmen-go/openapi-preprocessor-base:latest`
Expected: the digest the user pasted (no credentials used, so the package is public).

---

### Task 3: GoReleaser builds the images with ko

**Files:**
- Modify: `.goreleaser.yaml` (`release`, `dockers_v2` → `kos`, header comment)

**Interfaces:**
- Consumes: `<base digest>` (Task 2).
- Produces: in release mode, one `Docker Manifest` artifact in `dist/artifacts.json` whose `.extra.Digest` is the index digest (`sha256:…`) and `Binary` artifacts per platform (unchanged); tags `<version>` (+ `X.Y`, `X`, `latest` for a public version) and ko's `sha256-<digest>.sbom` tags. `KO_DOCKER_REPO` set ⇒ no GitHub Release.

- [ ] **Step 1: Replace `dockers_v2` with `kos`**

```yaml
kos:
  - repositories:
      - ghcr.io/dolmen-go/openapi-preprocessor
    # The image is named exactly as the repository, without the import path
    bare: true
    # Our own base (.goreleaser/base.sh): /work, user 65532:65532, nothing else
    base_image: ghcr.io/dolmen-go/openapi-preprocessor-base@<base digest>
    platforms:
      - linux/amd64
      - linux/arm64
    tags: # copy the four templates of dockers_v2 here, unchanged
    # Reproducible images: dated at the commit (ko writes this time in the
    # local time zone: build in UTC, as CI does)
    creation_time: "{{ .CommitTimestamp }}"
    ko_data_creation_time: "{{ .CommitTimestamp }}"
    # One SPDX document per platform and for the index, pushed as
    # sha256-<digest>.sbom (checked by release.yml)
    sbom: spdx
    labels: # copy the seven labels of dockers_v2 here, unchanged
    # ko sets them on each platform image and on the index
    annotations:
      org.opencontainers.image.description: Preprocessor for OpenAPI specifications ($ref to external files, $inline, $merge)
      org.opencontainers.image.source: https://github.com/dolmen-go/openapi-preprocessor
      org.opencontainers.image.licenses: Apache-2.0
```

Under `release:` add `disable: '{{ isEnvSet "KO_DOCKER_REPO" }}'` with the spec's comment (images sent to another registry, by `reproduce.sh`: nothing published on GitHub). In the header comment, say that the local dry run needs a Docker daemon (ko loads the `linux/amd64` image into it).

- [ ] **Step 2: Check the config**

Run: `goreleaser check`
Expected: `config is valid` (a deprecation notice is a failure: report it).

- [ ] **Step 3: Commit** (P2 clones committed state)

```sh
git add .goreleaser.yaml
git commit -F "$SCRATCH/msg-task3.txt"   # "Build the container images with ko instead of dockers_v2"
```

- [ ] **Step 4: Publish into a scratch registry (Review Focus 4)**

Run P1, P2, P3 (P3 sets `GITHUB_TOKEN=bogus`).
Expected: GoReleaser succeeds; `jq -r '.[] | select(.type == "Docker Manifest") | .extra.Digest' dist/artifacts.json` in the clone prints one `sha256:…`.

- [ ] **Step 5: Check the images**

With `r=127.0.0.1:$port/openapi-preprocessor`:
- `crane ls $r` → `0.0.1-ko.1` and three `sha256-….sbom` tags, nothing else.
- `crane manifest $r:0.0.1-ko.1 | jq -c '[.manifests[].platform.architecture], .annotations'` → `["amd64","arm64"]`, and annotations with the description, source, licenses, and `org.opencontainers.image.base.name` naming `ghcr.io/dolmen-go/openapi-preprocessor-base@<base digest>`.
- `crane config --platform linux/arm64 $r:0.0.1-ko.1 | jq -c '[.created, .config.WorkingDir, .config.User, .config.Entrypoint, (.config.Labels | keys)]'` → the commit time in UTC (`…Z`, equal to `git -C "$SCRATCH/clone" log -1 --format=%cI` converted to UTC), `/work`, `65532:65532`, `["/ko-app/openapi-preprocessor"]`, and exactly the seven `org.opencontainers.image.*` label keys.

- [ ] **Step 6: Reproducible**

Run P2 again into `$SCRATCH/clone2` (umask `0002`), then P3 from it into the same registry under `KO_DOCKER_REPO=127.0.0.1:$port/again`.
Expected: the same `Docker Manifest` digest as Step 4.

Keep the registry running for Tasks 4 and 5, or redo P1–P3 there.

---

### Task 4: Smoke test and CI dry run of the ko image

**Files:**
- Modify: `.goreleaser/smoke-test.sh`
- Modify: `.github/workflows/test.yml` (job `release-dry-run`)

**Interfaces:**
- Consumes: the image of Task 3 (P1–P3).
- Produces: `.goreleaser/smoke-test.sh <image> [<expected -version output>]`, same environment variables; with `SOURCE_DATE_EPOCH` it checks only the image creation time.

- [ ] **Step 1: Run the current smoke test on the ko image (failing test)**

Run: `podman pull --tls-verify=false 127.0.0.1:$port/openapi-preprocessor:0.0.1-ko.1 && SOURCE_DATE_EPOCH=$(git -C "$SCRATCH/clone" log -1 --format=%ct) CONTAINER_ENGINE=podman .goreleaser/smoke-test.sh 127.0.0.1:$port/openapi-preprocessor:0.0.1-ko.1 v0.0.1-ko.1`
Expected: `FAIL: image file usr/local/bin/openapi-preprocessor: …`.

- [ ] **Step 2: Update `smoke-test.sh`**

- `entry()` also strips a leading `/` (ko's layer names its binary `/ko-app/openapi-preprocessor`).
- `check_entry ko-app/openapi-preprocessor -r-xr-xr-x`; `check_entry work drwxr-xr-x` unconditionally (it comes from the base).
- With `SOURCE_DATE_EPOCH`: keep the `created` check; remove the file-date loop and its `ok` line.
- Remove the BuildKit/Buildah comments; header: `SOURCE_DATE_EPOCH if set, the image must be created at this time (Unix seconds)`.

- [ ] **Step 3: Run it again**

Run: the Step 1 command, then the same with `TZ=Pacific/Auckland`.
Expected: all `ok - …` lines both times, including `ok - image created at SOURCE_DATE_EPOCH (…)` and `ok - -version: v0.0.1-ko.1`.

- [ ] **Step 4: It still catches a wrong date**

Run: the Step 1 command with `SOURCE_DATE_EPOCH=1`.
Expected: `FAIL: image created: got …, want 1`.

- [ ] **Step 5: Update the `release-dry-run` job (Review Focus 1)**

Remove `docker/setup-buildx-action`, the fingerprint steps, the cache prune and the second build. Keep checkout, setup-go, syft, `goreleaser check`, and one `release --snapshot --clean` (its comment: ko loads the `linux/amd64` image into the runner's Docker daemon). Then:

```yaml
      - name: Check the archives
        run: |
          # shellcheck disable=SC2046 # one path per archive, without spaces
          .goreleaser/check-archives.sh "$(git log -1 --format=%ct)" $(jq -r '.[] | select(.type == "Archive" and .extra.Format == "tar.gz") | .path' dist/artifacts.json)

      - name: Smoke test the image
        run: |
          image=$(jq -r 'first(.[] | select(.type == "Docker Manifest")) | .name' dist/artifacts.json)
          [ -n "$image" ] || { echo "FAIL: no Docker Manifest in dist/artifacts.json" >&2; exit 1; }
          echo "# $image"
          SOURCE_DATE_EPOCH="$(git log -1 --format=%ct)" .goreleaser/smoke-test.sh "$image"
```

The job's comment above it says why one build (two builds in one job share Go's build cache; reproducibility is checked by `reproduce.sh` against a published release).

- [ ] **Step 6: Lint**

Run: `shellcheck .goreleaser/smoke-test.sh && actionlint .github/workflows/test.yml`
Expected: no output.

- [ ] **Step 7: Commit**

```sh
git add .goreleaser/smoke-test.sh .github/workflows/test.yml
git commit -F "$SCRATCH/msg-task4.txt"   # "Smoke test the ko image, in a single snapshot build"
```

---

### Task 5: Release workflow

**Files:**
- Modify: `.github/workflows/release.yml`

**Interfaces:**
- Consumes: ko's SBOM tags (Task 3), `smoke-test.sh` (Task 4).

- [ ] **Step 1: Update the workflow**

- Remove `docker/setup-buildx-action`. Keep `docker/login-action` with a comment: ko pushes with Docker's credentials.
- Release step `env`: add `TZ: UTC` with a comment (ko's image `created` time is in the local time zone; runners are UTC, this makes it explicit).
- Before the check step, a step installing crane: `GOBIN="$RUNNER_TEMP/bin" go install github.com/google/go-containerregistry/cmd/crane@v0.22.1` and `echo "$RUNNER_TEMP/bin" >> "$GITHUB_PATH"`, with the version comment from Global Constraints.
- The smoke-test loop is unchanged. The SBOM part becomes, for each platform: `digest=$(crane digest --platform "$platform" "$image")`; `layer=$(crane manifest "ghcr.io/dolmen-go/openapi-preprocessor:sha256-${digest#sha256:}.sbom" | jq -r '.layers[0].digest')`; `crane blob "ghcr.io/dolmen-go/openapi-preprocessor@$layer" > sbom.json`; then the existing `jq -e … '.packages | map(.name) | index($pkg)'` check on the modules of Global Constraints, computed in that loop for the platform (no `stdlib`), same `FAIL:` and `ok - SBOM $platform` lines. A failure of `go list` fails the step.

- [ ] **Step 2: Lint**

Run: `actionlint .github/workflows/release.yml`
Expected: no output.

- [ ] **Step 3: Run the SBOM check on the scratch registry**

Run the step's SBOM loop with `bash -e`, with `image=127.0.0.1:$port/openapi-preprocessor:0.0.1-ko.1` and `$SCRATCH/bin` first in `PATH`, in three copies of the source (`git archive HEAD`): unchanged; linking a local module `example.com/extra` (`go mod edit -require=example.com/extra@v0.0.0 -replace=example.com/extra=./extra`, imported from a file of package `main`); importing it only from a `_test.go` file.
Expected: unchanged and test-only: `ok - SBOM linux/amd64`, `ok - SBOM linux/arm64`. Linked: `FAIL: SBOM linux/amd64: missing example.com/extra`.

- [ ] **Step 4: Commit**

```sh
git add .github/workflows/release.yml
git commit -F "$SCRATCH/msg-task5.txt"   # "Release: images by ko, SBOM check on ko's SBOM"
```

---

### Task 6: `reproduce.sh` with a local registry

**Files:**
- Rewrite: `.goreleaser/reproduce.sh`
- Delete: `.goreleaser/fingerprint.sh`, `.goreleaser/Dockerfile`

**Interfaces:**
- Consumes: `check-archives.sh <epoch> <archive.tar.gz>...` (unchanged), `dist/artifacts.json` (`Archive`, `Checksum`, `Binary`, `Docker Manifest`), `dist/metadata.json` (`.tag`, `.version`).
- Produces: `.goreleaser/reproduce.sh [-n] <tag>`; `ok - …` per check, `# …` info lines (tool versions, `# registry 127.0.0.1:<port>`, `# index sha256:…`), `FAIL: …` and non-zero exit on the first failure.

- [ ] **Step 1: Rewrite the script**

Follow the spec's steps 1–8 for `reproduce.sh`. Keep from today's script, unchanged: option parsing, `here`/`repo`, `fail()`, the Go isolation, the private repository and its git settings, the archive comparison and `check-archives.sh` call. Constants at the top: `github=dolmen-go/openapi-preprocessor`, `crane_version=v0.22.1`. Header comment: what is checked now, usage, requirements from the spec (no Docker, no Podman), the `mise exec … --` example.

Decisions the spec leaves to the plan:
- Order: options → `tmp` and traps → Go isolation plus `export TZ=UTC` → private repository → `kos` check → crane → registry → GoReleaser → archives → images → binaries.
- `kos` check: `grep -q '^kos:' .goreleaser.yaml` in the private repository; the failure message prints `git clone --branch $tag $repo <directory>` and `<directory>/.goreleaser/reproduce.sh [-n] $tag` (with `-n` when given).
- crane: `GOBIN="$tmp/bin" go install "github.com/google/go-containerregistry/cmd/crane@$crane_version"`, then `crane="$tmp/bin/crane"`; print `# crane $crane_version`.
- Registry: started in the background, log in `$tmp/registry.log`, PID in `registry_pid`; wait for `serving on port` for at most 10 s (0.1 s steps), failing early if `kill -0 "$registry_pid"` fails. The `EXIT` trap does `[ -z "${registry_pid:-}" ] || kill "$registry_pid" 2> /dev/null || true`, then `rm -rf "$tmp"`.
- Index digest: exactly one `Docker Manifest` in `dist/artifacts.json`, else fail.
- Binaries: platforms from `"$crane" manifest "$local@$index"` (`.manifests[] | "\(.platform.os) \(.platform.architecture) \(.digest)"`), none ⇒ fail. Per platform: `"$crane" export "$local@$digest" "$tmp/image.tar"`, `tar -xOPf "$tmp/image.tar" --wildcards "*ko-app/$name" > "$tmp/image-bin"` (fails when absent), the `Binary` artifact by `.goos`/`.goarch` (absent ⇒ fail), SHA-256 of both with `sha256sum`; `ok - linux/<arch>: image binary identical to the archived one`.

- [ ] **Step 2: Delete the files without users**

Run: `git rm -q .goreleaser/fingerprint.sh .goreleaser/Dockerfile && grep -rn -i 'fingerprint\|dockerfile' --exclude-dir=docs --exclude-dir=.git .`
Expected: no output.

- [ ] **Step 3: Lint**

Run: `shellcheck .goreleaser/reproduce.sh`
Expected: no output.

- [ ] **Step 4: Old tag fails early**

Run: `time .goreleaser/reproduce.sh v1.0.0-rc.3`
Expected: `FAIL:` with the `git clone --branch v1.0.0-rc.3 …` and `<directory>/.goreleaser/reproduce.sh v1.0.0-rc.3` lines, exit 1, within seconds, no GoReleaser output.

- [ ] **Step 5: Throwaway tag, twice (Review Focus 2 and 4)**

P2 (the clone gets Tasks 3–5), then copy the new script over the clone's (the run fetches only the tag; the copy is what runs): `cp .goreleaser/reproduce.sh "$SCRATCH/clone/.goreleaser/"`. Run:
1. `GITHUB_TOKEN=bogus "$SCRATCH/clone/.goreleaser/reproduce.sh" -n v0.0.1-ko.1`
2. `TZ=Pacific/Auckland "$SCRATCH/clone/.goreleaser/reproduce.sh" -n v0.0.1-ko.1`

Expected, both: `ok - goreleaser built the release v0.0.1-ko.1`, the archive list, the `check-archives.sh` lines, `# index sha256:…` (the same digest both times, and the same as Task 3), two `ok - linux/<arch>: image binary identical…` lines, exit 0.

- [ ] **Step 6: A different image binary fails**

In the clone: add `ldflags: ["-s"]` to the `kos` entry (the archives keep `-s -w`), commit `.goreleaser.yaml` only (not the copied script), tag `v0.0.1-ko.2`. Run `"$SCRATCH/clone/.goreleaser/reproduce.sh" -n v0.0.1-ko.2`.
Expected: `FAIL:` naming `linux/amd64` and both SHA-256 values, exit 1.

- [ ] **Step 7: Interrupted run (Review Focus 3)**

For each of `TERM` and `HUP`: start `"$SCRATCH/clone/.goreleaser/reproduce.sh" -n v0.0.1-ko.1 > "$SCRATCH/int.log" 2>&1 &`, wait until `# registry` appears in the log, `kill -s <signal>` the script's PID, `wait` for it, read the port from the log.
Expected: exit status 143 (`TERM`) / 129 (`HUP`); `curl -s "http://127.0.0.1:$port/v2/"` fails to connect.

- [ ] **Step 8: Commit**

```sh
git add .goreleaser/reproduce.sh
git commit -F "$SCRATCH/msg-task6.txt"   # "reproduce.sh: publish into a local registry, compare the images with GHCR's"
```

---

### Task 7: README and earlier specs

**Files:**
- Modify: `README.md` (image section)
- Modify: `docs/superpowers/specs/2026-10-07-container-image-release-design.md`, `docs/superpowers/specs/2026-10-09-reproducible-release-design.md` (top notice)

- [ ] **Step 1: README**

- `COPY --from=ghcr.io/dolmen-go/openapi-preprocessor:1 /ko-app/openapi-preprocessor /usr/local/bin/`.
- Replace the SBOM paragraph: each platform image has an SPDX SBOM, pushed next to it as `sha256-<image digest>.sbom`; read it with crane:

  ```
  $ digest=$(crane digest --platform linux/amd64 ghcr.io/dolmen-go/openapi-preprocessor:1)
  $ crane blob ghcr.io/dolmen-go/openapi-preprocessor@$(crane manifest ghcr.io/dolmen-go/openapi-preprocessor:sha256-${digest#sha256:}.sbom | jq -r '.layers[0].digest')
  ```

  and: it does not list the Go version; the SBOM of the matching `linux` archive of the GitHub Release (`<archive>.sbom.json`) does, and the image's binary is that archive's.
- Check that the image description (static binary, no shell, unprivileged user) is still accurate.

- [ ] **Step 2: Check the README commands**

Run the two commands with `$SCRATCH/bin/crane` against `127.0.0.1:$port/openapi-preprocessor:0.0.1-ko.1`.
Expected: an SPDX JSON document whose `.packages[].name` include the modules of Global Constraints.

- [ ] **Step 3: Notices**

At the top of both earlier specs, in the format of the existing notice of the 2026-10-07 spec (the 2026-10-07 spec keeps its notice and gets a second one):

```markdown
> **Notice:** partially superseded by
> [2026-10-16-ko-container-images-design.md](2026-10-16-ko-container-images-design.md)
> (everything about the container images: built by ko, base image, SBOM,
> checks in reproduce.sh and CI).
```

- [ ] **Step 4: Commit**

```sh
git add README.md docs/superpowers/specs/2026-10-07-container-image-release-design.md docs/superpowers/specs/2026-10-09-reproducible-release-design.md
git commit -F "$SCRATCH/msg-task7.txt"   # "README: the ko image (binary path, SBOM)"
```

---

### Task 8: CI dry run on the branch

**Files:** none.

- [ ] **Step 1: Stop P1**

Run: `kill "$(cat "$SCRATCH/registry.pid")"`, and check nothing listens on the scratch port.

- [ ] **Step 2: Hand over to the user**

Ask the user to push the branch `ko-images`. Then: `gh run list --branch ko-images --workflow Test --limit 1` and `gh run watch <id>`.
Expected: `release-dry-run` passes: the snapshot build, `ok - …` lines of `check-archives.sh`, `# goreleaser.ko.local:<hex>` (Review Focus 1), all `ok - …` lines of the smoke test.

After the merge and the next release (tagged by the user), the spec's last checks are the user's: the release workflow passes, and `reproduce.sh <tag>` with CI's Go and GoReleaser versions finds archives and images identical to the published ones.
