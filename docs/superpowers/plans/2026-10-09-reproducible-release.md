# Reproducible Release Artifacts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the release archives reproducible from a Git checkout (identical to CI's with CI's Go and GoReleaser versions), and the container images reproducible within a toolchain (BuildKit in CI, Podman locally).

**Architecture:** Timestamps, owners and modes are fixed in `.goreleaser.yaml` (commit time as epoch, `root/root`, explicit modes, BuildKit `rewrite-timestamp`). Small POSIX shell scripts under `.goreleaser/` check the results: `check-archives.sh` (archive entries), `fingerprint.sh` (what must be identical between two builds), the existing `smoke-test.sh` (now also image dates) and `reproduce.sh` (local reproduction of a release tag). CI builds the snapshot twice and compares; the release workflow checks the dates of the published image.

**Tech Stack:** GoReleaser v2 (2.18.2 locally), Docker buildx/BuildKit (CI), Podman 5.7 (local), POSIX sh, jq, GNU tar, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-10-09-reproducible-release-design.md`

## Global Constraints

- No version pinning: Go stays `go-version: stable`, GoReleaser `version: '~> v2'`, BuildKit as `docker/setup-buildx-action` provides it.
- Epoch = the commit's committer date: `{{ .CommitTimestamp }}` / `{{ .CommitDate }}` in GoReleaser, `git log -1 --format=%ct` in scripts and workflows.
- Archive entries: owner `0/0` with names `root/root` (GoReleaser cannot write empty names); checks must also accept empty names.
- Image index, attestations, SBOMs and `checksums.txt` as a whole are allowed to differ between builds; archive lines of `checksums.txt` and per-platform images are not.
- If buildx rejects the `--output` flags, stop and review the options with the user: no fallback.
- `$SCRATCH` in commands = the session's scratchpad directory.
- Local tools via `mise exec <tool>@<version> -- …` (never `mise use`). No Docker locally: buildx-specific behavior is checked with a logging `docker` shim and in CI. Throwaway clones and shims live in the scratchpad directory, never in the repository.
- **Deviation from the spec, to confirm with the user before execution:** archive entries also get explicit modes (`0644` for `LICENSE`, `README.md`, the man page; `0755` for the binary). Git checks files out with the user's umask (`0002` on this machine → `664`; CI → `644`), so without an explicit mode local archives can never match CI's.
- **Deviation from the spec, to confirm with the user before execution:** `reproduce.sh` builds images with Podman only (the spec allowed `CONTAINER_ENGINE=docker`): Docker's build flags differ and can't be tested on this machine.
- Commit messages: plain sentence style as in `git log`, ending with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. On this unpushed branch, fixes to an earlier task's commit are folded into it (`git commit --fixup` + `GIT_SEQUENCE_EDITOR=: git rebase -i --autosquash master`).

## Review Focus

1. **buildx rejects `--output=type=image,rewrite-timestamp=true` next to GoReleaser's `--push`.** The dry run only exercises `--load`; a rejection would surface at the next release tag, after GoReleaser has started publishing. Task 2 reads buildx's source to confirm how `--push`/`--load` combine with `--output` before anything is merged.
2. **The user's umask** (`0002` here) changes the modes of checked-out files, hence the archives. Task 1 builds with umask `0002` and `0022` and requires identical archives.
3. **Untracked files in the user's checkout** (e.g. `mise.toml`) make Go stamp `+dirty` into the version. Task 6 runs `reproduce.sh` from a clone with an untracked file and requires `-version` to print the tag exactly.
4. **Go environment variables in the user's shell** (`GOFLAGS`, `GOEXPERIMENT`) change the binaries. Task 6 runs `reproduce.sh` with `GOFLAGS=-buildvcs=false` and requires the same archives as without.
5. **The user's time zone** in the archive and image date checks (GNU tar prints local time). Task 1 and Task 3 run their checks with `TZ=Pacific/Auckland` and require the same result.

---

## File Structure

| Path | Status | Responsibility |
|---|---|---|
| `.goreleaser.yaml` | modify | Fixed timestamps, owners, modes; image `created` label; BuildKit timestamp rewrite |
| `.goreleaser/check-archives.sh` | create | Checks every `.tar.gz` entry: owner `0/0`, names `root` or empty, mtime = epoch |
| `.goreleaser/fingerprint.sh` | create | Prints what must be identical between two snapshot builds: image IDs, archive checksums |
| `.goreleaser/smoke-test.sh` | modify | When `SOURCE_DATE_EPOCH` is set: image `created` and file mtimes = epoch |
| `.goreleaser/reproduce.sh` | create | Local reproduction of a release tag: archives vs GitHub Release, two Podman image builds |
| `.github/workflows/test.yml` | modify | `release-dry-run`: two builds, cache cleared, compare, archive check, smoke test with epoch |
| `.github/workflows/release.yml` | modify | Post-publish smoke test with `SOURCE_DATE_EPOCH` |

---

### Task 1: Reproducible archives

**Files:**
- Create: `.goreleaser/check-archives.sh`
- Modify: `.goreleaser.yaml` (`builds`, `archives`)

**Interfaces:**
- Produces: `.goreleaser/check-archives.sh <epoch> <archive.tar.gz>...` — checks mode (`0644`/`0755`), owner, names and mtime of every entry; prints `ok - <archive>: …` per archive; on failure prints the offending entries and `FAIL: …` to stderr, exit 1; exit 2 without archives.

- [ ] **Step 1: Write the check script**

Create `.goreleaser/check-archives.sh`:

```sh
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
```

Then `chmod +x .goreleaser/check-archives.sh` and lint: `mise exec shellcheck@latest -- shellcheck .goreleaser/check-archives.sh` → no output.

- [ ] **Step 2: Run it on the current config to see it fail (RED)**

```bash
mise exec goreleaser@2.18.2 -- goreleaser release --snapshot --clean --skip=docker,sbom > /dev/null 2>&1
sha256sum dist/*.tar.gz dist/*.zip | sort -k2 > "$SCRATCH/archives-run1.txt"
.goreleaser/check-archives.sh "$(git log -1 --format=%ct)" dist/*.tar.gz; echo "exit=$?"
mise exec goreleaser@2.18.2 -- goreleaser release --snapshot --clean --skip=docker,sbom > /dev/null 2>&1
sha256sum dist/*.tar.gz dist/*.zip | sort -k2 | diff -q "$SCRATCH/archives-run1.txt" - ; echo "diff exit=$?"
```

(`$SCRATCH` = the session scratchpad directory.)
Expected: `check-archives.sh` prints entries with mode `-rw-rw-r--`/`-rwxrwxr-x` (umask `0002`), owned by `1000/1000` (the local user), with checkout/compile dates, and `FAIL: …`, `exit=1`; the two runs' archives differ (`diff exit=1`: the binary's mtime is its compile time).

- [ ] **Step 3: Change the config**

In `.goreleaser.yaml`, add to the build (after `ldflags`):

```yaml
    # Reproducible builds: the binary's mtime is the commit time
    mod_timestamp: "{{ .CommitTimestamp }}"
```

and replace the `archives` entry with:

```yaml
archives:
  - formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
    # Reproducible archives: fixed mtime, owner and mode, whatever the
    # checkout time, the user and the umask
    builds_info:
      mtime: "{{ .CommitDate }}"
      owner: root
      group: root
      mode: 0755
    files:
      - src: LICENSE
        info: &file_info
          mtime: "{{ .CommitDate }}"
          owner: root
          group: root
          mode: 0644
      - src: README.md
        info: *file_info
      - src: man/man1/openapi-preprocessor.1
        info: *file_info
```

Run `mise exec goreleaser@2.18.2 -- goreleaser check` → `1 configuration file(s) validated`, no deprecation warning. (If YAML anchors are rejected, repeat the `info:` block in full for each file.)

- [ ] **Step 4: Run the checks again (GREEN), with two umasks and a far time zone**

```bash
epoch=$(git log -1 --format=%ct)
(umask 0002 && mise exec goreleaser@2.18.2 -- goreleaser release --snapshot --clean --skip=docker,sbom > /dev/null 2>&1)
sha256sum dist/*.tar.gz dist/*.zip | sort -k2 > "$SCRATCH/archives-run1.txt"
.goreleaser/check-archives.sh "$epoch" dist/*.tar.gz
TZ=Pacific/Auckland .goreleaser/check-archives.sh "$epoch" dist/*.tar.gz
(umask 0022 && mise exec goreleaser@2.18.2 -- goreleaser release --snapshot --clean --skip=docker,sbom > /dev/null 2>&1)
sha256sum dist/*.tar.gz dist/*.zip | sort -k2 | diff "$SCRATCH/archives-run1.txt" - && echo SAME
tar --full-time -tvzf dist/openapi-preprocessor_*_linux_amd64.tar.gz
```

Expected: 4 `ok - …` lines (one per `.tar.gz`; the 2 zips are covered by `SAME`) from each `check-archives.sh` run (same date printed under both time zones); `SAME`; the listing shows `-rw-r--r-- root/root` for the 3 files and `-rwxr-xr-x root/root` for the binary, all dated the commit time (local time).
Note: the build's umask changes the mode of the binary Go writes (`775` with `0002`, `755` with `0022`), so the two runs prove the `builds_info.mode` override. `LICENSE`, `README.md` and the man page keep the working copy's mode (`664` here, `644` in CI), whatever the build's umask: the mode check of `check-archives.sh` (`-rw-r--r--` only) proves the `info.mode` override for them.

- [ ] **Step 5: Commit**

```bash
git add .goreleaser/check-archives.sh .goreleaser.yaml
git commit -F - <<'EOF'
Reproducible release archives

Fixed mtime (commit time), owner (root) and modes for the archive entries
and the binaries, and a script to check the archives.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```

---

### Task 2: Reproducible image build settings

**Files:**
- Modify: `.goreleaser.yaml` (`dockers_v2` build args, labels and flags)

**Interfaces:**
- Consumes: nothing from Task 1 (same file, other sections).
- Produces: buildx receives `--build-arg SOURCE_DATE_EPOCH=<commit time>`, `--label org.opencontainers.image.created=<commit date>`, and `--output=type=docker,rewrite-timestamp=true` (snapshot) or `--output=type=image,rewrite-timestamp=true` (release).

(The first version of this task set `SOURCE_DATE_EPOCH` in a top-level `env:`; the shim showed `-62135596800`, the zero time: GoReleaser renders `env` before it loads the git information. The spec was updated to the build argument, with the user's approval.)

- [ ] **Step 1: Write the logging `docker` shim (RED setup)**

In the scratchpad (`$SCRATCH/shim/docker`, not committed):

```sh
#!/bin/sh
d=$(dirname "$0")
echo "DOCKER $* [SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH-unset}]" >> "$d/log"
case "$1" in
  info) echo '{}'; exit 0;;
  buildx) case "$2" in version) echo "github.com/docker/buildx v0.30.0 abc"; exit 0;; inspect|ls) echo "Driver: docker-container"; exit 0;; esac;;
esac
exit 0
```

`chmod +x` it. GoReleaser then fails on the missing image ID file, after logging the `buildx build` commands; that failure is expected.

- [ ] **Step 2: Run it on the current config to see it fail (RED)**

```bash
: > "$SCRATCH/shim/log"
PATH="$SCRATCH/shim:$PATH" mise exec goreleaser@2.18.2 -- goreleaser release --snapshot --clean --skip=sbom > /dev/null 2>&1
grep 'buildx build' "$SCRATCH/shim/log" | grep -o -- '--build-arg [^ ]*\|--output=[^ ]*\|image.created=[^ ]*' | sort | uniq -c
git log -1 --format='%ct %cI'
```

Expected: `image.created=<now>` for both builds, no `--build-arg SOURCE_DATE_EPOCH=…`, no `--output=…`.

- [ ] **Step 3: Change the config**

In `dockers_v2`, replace the `created` label:

```yaml
      org.opencontainers.image.created: "{{ .CommitDate }}"
```

and add after `sbom: "true"`:

```yaml
    # Reproducible images: BuildKit uses SOURCE_DATE_EPOCH for the image
    # creation time and, with rewrite-timestamp, for the files in the layers.
    # (Not in a top-level env: GoReleaser renders it before loading the git
    # information, so .CommitTimestamp would be zero there.)
    build_args:
      SOURCE_DATE_EPOCH: "{{ .CommitTimestamp }}"
    # GoReleaser adds --load (snapshot) or --push (release)
    flags:
      - "{{ if .IsSnapshot }}--output=type=docker,rewrite-timestamp=true{{ else }}--output=type=image,rewrite-timestamp=true{{ end }}"
```

`mise exec goreleaser@2.18.2 -- goreleaser check` → validated, no warning.

- [ ] **Step 4: Run the shim again (GREEN), snapshot and release mode**

Snapshot, in the repository: same commands as Step 2. Expected: 2 builds, each with `--build-arg SOURCE_DATE_EPOCH=<git log -1 --format=%ct>`, `image.created=<git log -1 --format=%cI, in UTC>` and `--output=type=docker,rewrite-timestamp=true` (next to `--load`).

Release mode, in a throwaway clone (nothing can be published: `docker` is the shim, the token and the remote are fake):

```bash
rm -rf "$SCRATCH/clone" && git clone -q . "$SCRATCH/clone"
git -C "$SCRATCH/clone" remote set-url origin https://github.com/example/example.git
git -C "$SCRATCH/clone" tag -a v9.9.9 -m test
: > "$SCRATCH/shim/log"
(cd "$SCRATCH/clone" && GITHUB_TOKEN=invalid-fake-token PATH="$SCRATCH/shim:$PATH" mise exec goreleaser@2.18.2 -- goreleaser release --clean --skip=announce,validate,sbom > /dev/null 2>&1)
grep 'buildx build' "$SCRATCH/shim/log" | grep -o -- '--build-arg [^ ]*\|--output=[^ ]*\|--push\|image.created=[^ ]*' | sort | uniq -c
rm -rf "$SCRATCH/clone"
```

Expected: 1 multi-platform build with `--push`, `--output=type=image,rewrite-timestamp=true`, `--build-arg SOURCE_DATE_EPOCH=<clone HEAD's %ct>`, `image.created=<its commit date>`.

- [ ] **Step 5: Confirm in buildx's source how `--push`/`--load` combine with `--output`**

```bash
rm -rf "$SCRATCH/buildx" && git clone -q --depth 1 https://github.com/docker/buildx "$SCRATCH/buildx"
grep -rni 'exportpush\|exportload' "$SCRATCH/buildx/commands/" "$SCRATCH/buildx/build/" "$SCRATCH/buildx/util/buildflags/" | grep -v _test | head -20
```

Read the code around those lines. Expected: with `--push`, an existing `type=image` output gets `push=true` (no second output, no error); with `--load`, an existing `type=docker` output is used for loading. Record the file and lines in the ledger.
If the code shows an error or a duplicate output for these combinations: **stop and report to the user** (spec: no fallback, options are reviewed). Then `rm -rf "$SCRATCH/buildx"`.

- [ ] **Step 6: Commit**

```bash
git add .goreleaser.yaml
git commit -F - <<'EOF'
Reproducible container images: fixed timestamps

SOURCE_DATE_EPOCH (commit time) as a build argument, file timestamps in
the layers rewritten to it, and the created label set to the commit date.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```

---

### Task 3: Smoke test checks the image dates

**Files:**
- Modify: `.goreleaser/smoke-test.sh`

**Interfaces:**
- Consumes: `CONTAINER_ENGINE`, `PLATFORM` (existing), new optional `SOURCE_DATE_EPOCH` (Unix seconds).
- Produces: when `SOURCE_DATE_EPOCH` is set, two more lines: `ok - image created at SOURCE_DATE_EPOCH (<epoch>)` and `ok - image files dated SOURCE_DATE_EPOCH`; unchanged output otherwise.

- [ ] **Step 1: Write the checks**

In `.goreleaser/smoke-test.sh`, add to the `Environment:` comment block:

```sh
#   SOURCE_DATE_EPOCH if set, the image and its files must be dated at this
#                     time (Unix seconds)
```

Replace the container block:

```sh
cid=$("$engine" create $platform_opt "$image")
user=$("$engine" container inspect --format '{{.Config.User}}' "$cid")
"$engine" export "$cid" | tar --numeric-owner -tvf - > "$tmp/files"
"$engine" rm "$cid" > /dev/null
```

with:

```sh
cid=$("$engine" create $platform_opt "$image")
user=$("$engine" container inspect --format '{{.Config.User}}' "$cid")
# The image the container was created from: the right platform variant
image_id=$("$engine" container inspect --format '{{.Image}}' "$cid")
"$engine" export "$cid" | TZ=UTC tar --numeric-owner --full-time -tvf - > "$tmp/files"
"$engine" rm "$cid" > /dev/null
```

and insert, just before the `# run_tool <dir> <arg>...` comment:

```sh
# Reproducible build: the image and its files are dated SOURCE_DATE_EPOCH
if [ -n "${SOURCE_DATE_EPOCH:-}" ]; then
	case "$engine" in
	podman) created=$("$engine" image inspect --format '{{.Created.Unix}}' "$image_id") ;;
	*) created=$(date -u -d "$("$engine" image inspect --format '{{.Created}}' "$image_id")" +%s) ;;
	esac
	[ "$created" = "$SOURCE_DATE_EPOCH" ] || fail "image created: got $created, want $SOURCE_DATE_EPOCH"
	echo "ok - image created at SOURCE_DATE_EPOCH ($SOURCE_DATE_EPOCH)"

	want_time=$(TZ=UTC date -d "@$SOURCE_DATE_EPOCH" '+%Y-%m-%d %H:%M:%S')
	for p in usr usr/local usr/local/bin usr/local/bin/openapi-preprocessor work; do
		t=$(entry "$p" | awk '{ print $4 " " $5 }')
		# Docker (BuildKit) creates /work in the image, Podman (Buildah) only at run time
		[ -n "$t" ] || [ "$p" != work ] || continue
		[ "$t" = "$want_time" ] || fail "image file $p: dated '$t' UTC, want $want_time"
	done
	echo "ok - image files dated SOURCE_DATE_EPOCH"
fi
```

Lint: `mise exec shellcheck@latest -- shellcheck .goreleaser/smoke-test.sh` → no output.

- [ ] **Step 2: Build two local images: without and with fixed timestamps**

```bash
epoch=$(git log -1 --format=%ct)
ctx=$(mktemp -d) && mkdir -p "$ctx/linux/amd64"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o "$ctx/linux/amd64/openapi-preprocessor" .
podman build -q --platform linux/amd64 -f .goreleaser/Dockerfile -t localhost/openapi-preprocessor:plain "$ctx"
podman build -q --platform linux/amd64 --no-cache --identity-label=false --source-date-epoch="$epoch" --rewrite-timestamp -f .goreleaser/Dockerfile -t localhost/openapi-preprocessor:repro "$ctx"
rm -rf "$ctx"
```

- [ ] **Step 3: Run the smoke test (RED on the plain image, GREEN on the other)**

```bash
SOURCE_DATE_EPOCH=$epoch CONTAINER_ENGINE=podman .goreleaser/smoke-test.sh localhost/openapi-preprocessor:plain; echo "exit=$?"
SOURCE_DATE_EPOCH=$epoch CONTAINER_ENGINE=podman .goreleaser/smoke-test.sh localhost/openapi-preprocessor:repro; echo "exit=$?"
TZ=Pacific/Auckland SOURCE_DATE_EPOCH=$epoch CONTAINER_ENGINE=podman .goreleaser/smoke-test.sh localhost/openapi-preprocessor:repro | grep -c '^ok'
CONTAINER_ENGINE=podman .goreleaser/smoke-test.sh localhost/openapi-preprocessor:plain | grep -c '^ok'
```

Expected: `plain` → `FAIL: image created: got <build time>, want <epoch>`, `exit=1`; `repro` → 10 `ok - …` lines, `exit=0`; same 10 under `TZ=Pacific/Auckland`; without `SOURCE_DATE_EPOCH`, `plain` → 8 (unchanged behavior).
Then `podman rmi localhost/openapi-preprocessor:plain localhost/openapi-preprocessor:repro`.

- [ ] **Step 4: Commit**

```bash
git add .goreleaser/smoke-test.sh
git commit -F - <<'EOF'
Smoke test: check the image dates when SOURCE_DATE_EPOCH is set

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```

---

### Task 4: Reproducibility check in the CI dry run

**Files:**
- Create: `.goreleaser/fingerprint.sh`
- Modify: `.github/workflows/test.yml` (`release-dry-run` job)

**Interfaces:**
- Consumes: `check-archives.sh <epoch> <archive>...` (Task 1), `smoke-test.sh` with `SOURCE_DATE_EPOCH` (Task 3), GoReleaser snapshot images `ghcr.io/dolmen-go/openapi-preprocessor:<version>-amd64|-arm64` and `dist/metadata.json` `.version`.
- Produces: `.goreleaser/fingerprint.sh <version>` (run from the repository root after a snapshot build) — prints `image linux/amd64 <id>`, `image linux/arm64 <id>`, then the archive lines of `dist/checksums.txt` sorted by name; exit non-zero if an image or `dist/checksums.txt` is missing. `CONTAINER_ENGINE`: `docker` (default) or `podman`.

- [ ] **Step 1: Write the script**

Create `.goreleaser/fingerprint.sh`:

```sh
#!/bin/sh
# Prints what must be identical between two GoReleaser snapshot builds of the
# same commit: the image IDs and the checksums of the archives (the SBOMs are
# left out: they are not reproducible).
#
# Usage: .goreleaser/fingerprint.sh <version>
#   Run from the repository root after 'goreleaser release --snapshot';
#   <version> is .version in dist/metadata.json.
#
# Environment:
#   CONTAINER_ENGINE  docker (default) or podman
set -eu

version=${1:?usage: $0 <version>}
engine=${CONTAINER_ENGINE:-docker}

for arch in amd64 arm64; do
	id=$("$engine" image inspect --format '{{.Id}}' "ghcr.io/dolmen-go/openapi-preprocessor:$version-$arch")
	echo "image linux/$arch $id"
done
[ -f dist/checksums.txt ] || { echo "FAIL: dist/checksums.txt not found" >&2; exit 1; }
grep -v '\.sbom\.json$' dist/checksums.txt | sort -k2
```

`chmod +x`, then `mise exec shellcheck@latest -- shellcheck .goreleaser/fingerprint.sh` → no output.

- [ ] **Step 2: Test it locally with Podman (RED: no images; GREEN: images built)**

```bash
mise exec goreleaser@2.18.2 -- goreleaser release --snapshot --clean --skip=docker,sbom > /dev/null 2>&1
v=$(jq -r .version dist/metadata.json)
CONTAINER_ENGINE=podman .goreleaser/fingerprint.sh "$v"; echo "exit=$?"
```

Expected: a Podman "image not known" error, `exit=125` (non-zero).

```bash
epoch=$(git log -1 --format=%ct)
ctx=$(mktemp -d)
for arch in amd64 arm64; do
	mkdir -p "$ctx/linux/$arch"
	cp "$(jq -r --arg a "$arch" '.[] | select(.type == "Binary" and .goos == "linux" and .goarch == $a) | .path' dist/artifacts.json)" "$ctx/linux/$arch/openapi-preprocessor"
	podman build -q --platform "linux/$arch" --no-cache --identity-label=false --source-date-epoch="$epoch" --rewrite-timestamp -f .goreleaser/Dockerfile -t "ghcr.io/dolmen-go/openapi-preprocessor:$v-$arch" "$ctx"
done
rm -rf "$ctx"
CONTAINER_ENGINE=podman .goreleaser/fingerprint.sh "$v"; echo "exit=$?"
podman rmi "ghcr.io/dolmen-go/openapi-preprocessor:$v-amd64" "ghcr.io/dolmen-go/openapi-preprocessor:$v-arm64"
```

Expected: 2 `image linux/… sha256:…` lines then 6 archive lines (`.tar.gz`/`.zip`, no `.sbom.json`), `exit=0`.

- [ ] **Step 3: Change the `release-dry-run` job**

In `.github/workflows/test.yml`, replace the steps from `- name: Build release (snapshot)` to the end of the job with:

```yaml
      - name: Build release (snapshot)
        uses: goreleaser/goreleaser-action@v7
        with:
          version: '~> v2'
          args: release --snapshot --clean

      - name: Fingerprint the build
        run: .goreleaser/fingerprint.sh "$(jq -r .version dist/metadata.json)" > "$RUNNER_TEMP/fingerprint-1.txt"

      # Otherwise the second build would just reuse the first one
      - name: Clear the BuildKit cache
        run: docker buildx prune --all --force

      - name: Build release again (snapshot)
        uses: goreleaser/goreleaser-action@v7
        with:
          version: '~> v2'
          args: release --snapshot --clean

      - name: Check the build is reproducible
        run: |
          .goreleaser/fingerprint.sh "$(jq -r .version dist/metadata.json)" > "$RUNNER_TEMP/fingerprint-2.txt"
          cat "$RUNNER_TEMP/fingerprint-1.txt"
          diff -u "$RUNNER_TEMP/fingerprint-1.txt" "$RUNNER_TEMP/fingerprint-2.txt"
          .goreleaser/check-archives.sh "$(git log -1 --format=%ct)" dist/*.tar.gz

      - name: Smoke test the image
        run: SOURCE_DATE_EPOCH="$(git log -1 --format=%ct)" .goreleaser/smoke-test.sh "ghcr.io/dolmen-go/openapi-preprocessor:$(jq -r .version dist/metadata.json)-amd64"
```

- [ ] **Step 4: Lint**

Run: `mise exec actionlint@latest shellcheck@latest -- actionlint .github/workflows/test.yml`
Expected: only the 2 `[matrix]` errors that already exist on `master` (anchor-based `exclude` of `test-others`), nothing about `release-dry-run`.

- [ ] **Step 5: Commit**

```bash
git add .goreleaser/fingerprint.sh .github/workflows/test.yml
git commit -F - <<'EOF'
CI: check that the release build is reproducible

The dry run builds the snapshot twice, with the BuildKit cache cleared in
between, and compares the image IDs and the archive checksums. It also
checks the archive entries and the image dates.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```

---

### Task 5: Release workflow checks the published image's dates

**Files:**
- Modify: `.github/workflows/release.yml` (step "Check the published image")

**Interfaces:**
- Consumes: `smoke-test.sh` with `SOURCE_DATE_EPOCH` and `PLATFORM` (Task 3).

- [ ] **Step 1: Pass the epoch to the smoke test**

In the `run:` script of "Check the published image", replace:

```sh
          image="ghcr.io/dolmen-go/openapi-preprocessor:${TAG#v}"
          for platform in linux/amd64 linux/arm64; do
            echo "# $platform"
            docker pull --platform "$platform" "$image"
            PLATFORM="$platform" .goreleaser/smoke-test.sh "$image" "$TAG"
          done
```

with:

```sh
          image="ghcr.io/dolmen-go/openapi-preprocessor:${TAG#v}"
          epoch=$(git log -1 --format=%ct)
          for platform in linux/amd64 linux/arm64; do
            echo "# $platform"
            docker pull --platform "$platform" "$image"
            SOURCE_DATE_EPOCH="$epoch" PLATFORM="$platform" .goreleaser/smoke-test.sh "$image" "$TAG"
          done
```

- [ ] **Step 2: Lint**

Run: `mise exec actionlint@latest shellcheck@latest -- actionlint .github/workflows/release.yml`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release.yml
git commit -F - <<'EOF'
Release: check the dates of the published image

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```

---

### Task 6: Local reproduction script

**Files:**
- Create: `.goreleaser/reproduce.sh`

**Interfaces:**
- Consumes: `check-archives.sh` (Task 1), `smoke-test.sh` with `SOURCE_DATE_EPOCH` (Task 3), the tag's `.goreleaser.yaml` and `.goreleaser/Dockerfile`.
- Produces: `.goreleaser/reproduce.sh [-n] <tag>` — prints `# <tool versions>` lines then `ok - …` lines; exit non-zero on the first failure. Uses the Go and GoReleaser on `PATH`; Podman, jq, curl, GNU tar.

- [ ] **Step 1: Write the script**

Create `.goreleaser/reproduce.sh`:

```sh
#!/bin/sh
# Reproduces locally the release <tag> made by GoReleaser in CI, and checks:
# - unless -n, that the archives are identical to those of the GitHub Release;
# - that the archive entries are owned by 0:0 and dated at the commit time;
# - that two Podman builds of each image give the same image (Podman images
#   are not compared with CI's, which are built by BuildKit);
# - the amd64 image with the smoke test.
#
# Usage: .goreleaser/reproduce.sh [-n] <tag>
#   -n  don't compare with the GitHub Release (for a tag that has none)
#
# Nothing is installed: run it with the Go and GoReleaser versions shown in
# the CI logs of the release, e.g.:
#   mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh v1.0.0-rc.3
# Requires: git, Podman, jq, curl, GNU tar.
set -eu

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

tmp=$(mktemp -d)
images=
cleanup() {
	# shellcheck disable=SC2086
	[ -z "$images" ] || podman rmi -f $images > /dev/null 2>&1 || true
	rm -rf "$tmp"
}
trap cleanup EXIT

# Go settings not set in CI, which would change the binaries: from the
# environment, and from the user's go env file (go env -w). Use the Go on
# PATH (the version of the CI logs), never a toolchain switch.
unset GOFLAGS GOEXPERIMENT GOFIPS140
export GOENV=off GOTOOLCHAIN=local

echo "# $(go version)"
echo "# goreleaser $(goreleaser --version 2>&1 | sed -n 's/^GitVersion: *//p')"
echo "# podman $(podman version --format '{{.Client.Version}}')"

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
git remote add origin "https://github.com/dolmen-go/openapi-preprocessor.git"
epoch=$(git show -s --format=%ct HEAD)

GORELEASER_CURRENT_TAG=$tag goreleaser release --clean --skip=publish,announce,sbom > "$tmp/goreleaser.log" 2>&1 || {
	tail -n 20 "$tmp/goreleaser.log" >&2
	fail "goreleaser release"
}
[ "$(jq -r .tag dist/metadata.json)" = "$tag" ] || fail "goreleaser built $(jq -r .tag dist/metadata.json), not $tag"
echo "ok - goreleaser built the release $tag"

# archives <checksums file>: the lines of the archives, sorted by name
archives() {
	grep -E '\.(tar\.gz|zip)$' "$1" | sort -k2
}
if [ "$compare" = 1 ]; then
	url=https://github.com/dolmen-go/openapi-preprocessor/releases/download/$tag/checksums.txt
	curl -fsSL -o "$tmp/published.txt" "$url" || fail "can't download $url (no published release? use -n)"
	archives "$tmp/published.txt" > "$tmp/published-archives.txt"
	archives dist/checksums.txt > "$tmp/local-archives.txt"
	diff -u "$tmp/published-archives.txt" "$tmp/local-archives.txt" || fail "archives differ from the GitHub Release"
	echo "ok - $(wc -l < "$tmp/local-archives.txt") archives identical to the GitHub Release"
else
	archives dist/checksums.txt
fi

"$here/check-archives.sh" "$epoch" dist/*.tar.gz

# Build context laid out as GoReleaser does: <os>/<arch>/openapi-preprocessor
for arch in amd64 arm64; do
	bin=$(jq -r --arg a "$arch" '.[] | select(.type == "Binary" and .goos == "linux" and .goarch == $a) | .path' dist/artifacts.json)
	[ -f "$bin" ] || fail "no linux/$arch binary in dist/artifacts.json"
	mkdir -p "$tmp/ctx/linux/$arch"
	cp -p "$bin" "$tmp/ctx/linux/$arch/openapi-preprocessor"
done

# build <platform>: build the image of the tag, print its ID
build() {
	podman build --quiet --no-cache --layers=false --platform "$1" --identity-label=false \
		--source-date-epoch="$epoch" --rewrite-timestamp \
		-f .goreleaser/Dockerfile "$tmp/ctx"
}
for platform in linux/amd64 linux/arm64; do
	id1=$(build "$platform")
	id2=$(build "$platform")
	images="$images $id1 $id2"
	[ "$id1" = "$id2" ] || fail "$platform: two builds give different images: $id1 $id2"
	echo "ok - $platform: two builds give the same image $id1"
	[ "$platform" != linux/amd64 ] || amd64_image=$id1
done

CONTAINER_ENGINE=podman SOURCE_DATE_EPOCH=$epoch "$here/smoke-test.sh" "$amd64_image" "$tag"
```

`chmod +x`, then `mise exec shellcheck@latest -- shellcheck .goreleaser/reproduce.sh` → no output.

- [ ] **Step 2: Prepare a throwaway clone with a tag**

```bash
rm -rf "$SCRATCH/clone" && git clone -q . "$SCRATCH/clone"
# GoReleaser needs a GitHub remote URL; nothing is published (--skip=publish)
git -C "$SCRATCH/clone" remote set-url origin https://github.com/dolmen-go/openapi-preprocessor.git
git -C "$SCRATCH/clone" tag -a v1.99.0-repro -m test   # v1: Go ignores v2+ tags for a module path without a /vN suffix
touch "$SCRATCH/clone/untracked-file"   # Review Focus 3
```

- [ ] **Step 3: Error cases**

```bash
(cd "$SCRATCH/clone" && mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh v0.0.0-missing; echo "exit=$?")
(cd "$SCRATCH/clone" && mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh v1.99.0-repro 2>&1 | tail -2; echo "exit=$?")
```

Expected: `FAIL: tag v0.0.0-missing not found`, `exit=1`; second: `FAIL: can't download …/v1.99.0-repro/checksums.txt (no published release? use -n)` (after the GoReleaser build), non-zero exit.

- [ ] **Step 4: Comparison against a published release that isn't reproducible (RED for the comparison)**

```bash
(cd "$SCRATCH/clone" && mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh v1.0.0-rc.2 2>&1 | tail -4; echo "exit=$?")
```

Expected: a diff of archive lines and `FAIL: archives differ from the GitHub Release`, non-zero exit (`v1.0.0-rc.2` was built before this change). CI used Go 1.27.1 for it (see its SBOM).

- [ ] **Step 5: Two runs on the throwaway tag (GREEN), one with `GOFLAGS` set and without `GITHUB_TOKEN`**

```bash
(cd "$SCRATCH/clone" && env -u GITHUB_TOKEN mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh -n v1.99.0-repro) | tee "$SCRATCH/repro-1.txt"
(cd "$SCRATCH/clone" && GOFLAGS=-buildvcs=false env -u GITHUB_TOKEN mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh -n v1.99.0-repro) | tee "$SCRATCH/repro-2.txt"
diff "$SCRATCH/repro-1.txt" "$SCRATCH/repro-2.txt" && echo SAME
git -C "$SCRATCH/clone" worktree list
```

Expected for each run: the 3 `# …` version lines, `ok - goreleaser built the release v1.99.0-repro`, the 6 archive lines, 6 `ok - …tar.gz…` lines from `check-archives.sh`, `ok - linux/amd64: two builds give the same image …`, the same for `linux/arm64`, then the smoke test's 10 `ok - …` lines with `ok - -version: v1.99.0-repro` (exactly the tag: no `+dirty` despite the untracked file); exit 0. `SAME`: archives and image IDs identical between the runs. `worktree list` shows only the clone itself (temporary worktrees removed).
If GoReleaser fails without `GITHUB_TOKEN` (spec assumption): **stop and report to the user**.

- [ ] **Step 6: Clean up and commit**

```bash
rm -rf "$SCRATCH/clone"
git add .goreleaser/reproduce.sh
git commit -F - <<'EOF'
Add a script to reproduce a release locally

It rebuilds a release tag from a clean worktree with GoReleaser, compares
the archives with those of the GitHub Release, and checks that two Podman
builds of the image are identical.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```

---

### Task 7: Read GoReleaser's outputs instead of repeating its config

Follow-up decided after the first implementation (spec: "Settings read from GoReleaser's outputs"): `reproduce.sh` and `fingerprint.sh` stop hard-coding what GoReleaser records in `dist/artifacts.json` (archives of any format with their checksums, the name of the checksums file, `tar.gz` archives, binaries and their name). The GitHub repository, the image platforms and the Dockerfile stay constants, defined once at the top of `reproduce.sh`. No new tool: `jq` only.

**Files:**
- Modify: `.goreleaser/reproduce.sh`
- Modify: `.goreleaser/fingerprint.sh`
- Modify: `.github/workflows/test.yml` (step "Check the build is reproducible")

**Interfaces:**
- Consumes: `dist/artifacts.json` written by GoReleaser: artifacts of type `Archive` (`.name`, `.path`, `.extra.Checksum` = `sha256:<hex>`, `.extra.Format`), `Checksum` (`.name`), `Binary` (`.path`, `.name`, `.goos`, `.goarch`). `check-archives.sh <epoch> <archive.tar.gz>...` (Task 1).
- Produces: unchanged command lines and output formats for `reproduce.sh [-n] <tag>` and `fingerprint.sh <version>`; the archive lines now cover every archive GoReleaser built, whatever its format, and no longer depend on the checksums file's name.

- [ ] **Step 1: Prepare a throwaway clone with a changed config (RED setup)**

The changed config is what the hard-coded values miss: a renamed checksums file and an extra archive format.

```bash
rm -rf "$SCRATCH/clone" && git clone -q . "$SCRATCH/clone"
git -C "$SCRATCH/clone" remote set-url origin https://github.com/dolmen-go/openapi-preprocessor.git
git -C "$SCRATCH/clone" tag -a v1.99.0-repro -m test   # unchanged config
```

In `$SCRATCH/clone/.goreleaser.yaml`, change `checksum.name_template` to `SHA256SUMS` and add to `archives[0].format_overrides`:

```yaml
      - goos: linux
        formats: [tar.gz, tar.xz]
```

then:

```bash
git -C "$SCRATCH/clone" -c user.name=t -c user.email=t@t commit -q -am "changed config"
git -C "$SCRATCH/clone" tag -a v1.99.2-repro -m test   # v1: Go ignores v2+ tags for this module path
```

- [ ] **Step 2: Run the current scripts on the changed config (RED)**

```bash
(cd "$SCRATCH/clone" && mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh -n v1.99.2-repro 2>&1 | grep -E 'tar|zip|checksums|FAIL'; echo "exit=${PIPESTATUS[0]}")
```

Expected: `grep: dist/checksums.txt: No such file or directory` instead of the archive lines, no line at all for the 2 `tar.xz` archives, and still `exit=0`: the change goes unnoticed.

- [ ] **Step 3: Write the new `reproduce.sh`**

Replace `.goreleaser/reproduce.sh` with:

```sh
#!/bin/sh
# Reproduces locally the release <tag> made by GoReleaser in CI, and checks:
# - unless -n, that the archives are identical to those of the GitHub Release;
# - that the archive entries are owned by 0:0 and dated at the commit time;
# - that two Podman builds of each image give the same image (Podman images
#   are not compared with CI's, which are built by BuildKit);
# - the amd64 image with the smoke test.
#
# Usage: .goreleaser/reproduce.sh [-n] <tag>
#   -n  don't compare with the GitHub Release (for a tag that has none)
#
# Nothing is installed: run it with the Go and GoReleaser versions shown in
# the CI logs of the release, e.g.:
#   mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh v1.0.0-rc.3
# Requires: git, Podman, jq, curl, GNU tar.
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

tmp=$(mktemp -d)
images=
cleanup() {
	# shellcheck disable=SC2086
	[ -z "$images" ] || podman rmi -f $images > /dev/null 2>&1 || true
	rm -rf "$tmp"
}
trap cleanup EXIT

# Go settings not set in CI, which would change the binaries: from the
# environment, and from the user's go env file (go env -w). Use the Go on
# PATH (the version of the CI logs), never a toolchain switch.
unset GOFLAGS GOEXPERIMENT GOFIPS140
export GOENV=off GOTOOLCHAIN=local

echo "# $(go version)"
echo "# goreleaser $(goreleaser --version 2>&1 | sed -n 's/^GitVersion: *//p')"
echo "# podman $(podman version --format '{{.Client.Version}}')"

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

GORELEASER_CURRENT_TAG=$tag goreleaser release --clean --skip=publish,announce,sbom > "$tmp/goreleaser.log" 2>&1 || {
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

# Build context laid out as GoReleaser does: <os>/<arch>/<binary name>
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
	images="$images $id1 $id2"
	[ "$id1" = "$id2" ] || fail "$platform: two builds give different images: $id1 $id2"
	echo "ok - $platform: two builds give the same image $id1"
	[ "$platform" != linux/amd64 ] || amd64_image=$id1
done

CONTAINER_ENGINE=podman SOURCE_DATE_EPOCH=$epoch "$here/smoke-test.sh" "$amd64_image" "$tag"
```

- [ ] **Step 4: Write the new `fingerprint.sh`**

Replace `.goreleaser/fingerprint.sh` with:

```sh
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
```

Lint: `mise exec shellcheck@latest -- shellcheck .goreleaser/reproduce.sh .goreleaser/fingerprint.sh` → no output.

- [ ] **Step 5: Run the new scripts (GREEN)**

```bash
cp .goreleaser/reproduce.sh .goreleaser/fingerprint.sh "$SCRATCH/clone/.goreleaser/"
(cd "$SCRATCH/clone" && mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh -n v1.99.2-repro 2>&1 | grep -E 'tar|zip|checksums|FAIL'; echo "exit=${PIPESTATUS[0]}")
(cd "$SCRATCH/clone" && mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh -n v1.99.0-repro | grep -c '^ok')
(cd "$SCRATCH/clone" && mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh v1.0.0-rc.2 2>&1 | tail -1; echo "exit=${PIPESTATUS[0]}")
```

Expected:
- changed config: 8 archive lines (6 as before + 2 `linux` `.tar.xz`), 4 `ok - …tar.gz…` lines from `check-archives.sh` (the `tar.xz` archives are compared by checksum only), `exit=0`;
- unchanged config: 17 `ok` lines;
- `v1.0.0-rc.2` (published before the reproducibility change, checksums file `checksums.txt`): `FAIL: archives differ from the GitHub Release`, non-zero exit: the published file is found by its name from `artifacts.json`, and its lines are matched to the local archives by name.

Then `fingerprint.sh` in the clone, on the changed config. The images must be recorded in `dist/artifacts.json`, so the snapshot runs `dockers_v2` through the logging `docker` shim of Task 2, extended to write the image ID file GoReleaser reads (for `buildx build`, write `sha256:` followed by 64 zeros to the file named by `--iidfile=`; put the shim in `PATH` by absolute path: GoReleaser runs `docker` from a temporary directory). The images themselves are then built with Podman under the recorded names:

```bash
cd "$SCRATCH/clone"
: > "$SCRATCH/shim/log"
PATH="$SCRATCH/shim:$PATH" mise exec goreleaser@2.18.2 -- goreleaser release --snapshot --clean --skip=sbom > /dev/null 2>&1
epoch=$(git log -1 --format=%ct); ctx=$(mktemp -d)
for arch in amd64 arm64; do
	mkdir -p "$ctx/linux/$arch"
	cp "$(jq -r --arg a "$arch" '.[] | select(.type == "Binary" and .goos == "linux" and .goarch == $a) | .path' dist/artifacts.json)" "$ctx/linux/$arch/openapi-preprocessor"
	podman build -q --platform "linux/$arch" --no-cache --layers=false --identity-label=false --source-date-epoch="$epoch" --rewrite-timestamp -f .goreleaser/Dockerfile \
		-t "$(jq -r --arg p "linux/$arch" '.[] | select(.type == "Docker Image" and .extra.Platforms == [$p]) | .name' dist/artifacts.json)" "$ctx"
done
rm -rf "$ctx"
CONTAINER_ENGINE=podman .goreleaser/fingerprint.sh; echo "exit=$?"
podman rmi $(jq -r '.[] | select(.type == "Docker Image") | .name' dist/artifacts.json)
cd -
```

Expected: 2 `image ghcr.io/dolmen-go/openapi-preprocessor:…` lines then 8 archive lines, `exit=0`. (With the previous `fingerprint.sh <version>`, on images named `…:<version>-<arch>`, the same snapshot printed the 2 image lines then `FAIL: dist/checksums.txt not found`.)

- [ ] **Step 6: Use `artifacts.json` in the CI dry run too**

In `.github/workflows/test.yml`, step "Check the build is reproducible", replace:

```sh
          .goreleaser/check-archives.sh "$(git log -1 --format=%ct)" dist/*.tar.gz
```

with:

```sh
          # shellcheck disable=SC2046 # one path per archive, without spaces
          .goreleaser/check-archives.sh "$(git log -1 --format=%ct)" $(jq -r '.[] | select(.type == "Archive" and .extra.Format == "tar.gz") | .path' dist/artifacts.json)
```

Run: `mise exec actionlint@latest shellcheck@latest -- actionlint .github/workflows/test.yml`
Expected: only the 2 `[matrix]` errors that already exist on `master`.

- [ ] **Step 7: Fold into the existing commits and clean up**

The branch is not pushed: fold the changes into the commits that introduced the scripts (see Global Constraints).

```bash
rm -rf "$SCRATCH/clone"
git add .goreleaser/reproduce.sh
git commit -q --fixup="$(git log --format=%h -1 --grep='^Add a script to reproduce a release locally')"
git add .goreleaser/fingerprint.sh .github/workflows/test.yml
git commit -q --fixup="$(git log --format=%h -1 --grep='^CI: check that the release build is reproducible')"
GIT_SEQUENCE_EDITOR=: git rebase -q -i --autosquash master
git log --oneline master..HEAD
```

Expected: the same 9 commit subjects as before, no `fixup!` left; `go test ./...` passes.

---

### Task 8: Docker with buildx in `reproduce.sh`, Podman as a fallback

Follow-up decided after Task 7 (spec: `reproduce.sh`, "Image builder"): with `docker buildx` (a real one, and a Docker daemon), GoReleaser builds the images itself, in snapshot mode, twice, as in the CI dry run; without it, the Podman builds of Task 6 stay, and GoReleaser's release-mode run gets `docker` added to `--skip`.

**Files:**
- Modify: `.goreleaser/reproduce.sh`

**Interfaces:**
- Consumes: `fingerprint.sh` (no argument, `CONTAINER_ENGINE`; Task 7), `smoke-test.sh` with `CONTAINER_ENGINE` and `SOURCE_DATE_EPOCH` (Task 3), `check-archives.sh` (Task 1); in a snapshot's `dist/artifacts.json`, artifacts of type `Docker Image` (`.name`, `.extra.Platforms`).
- Produces: same command line `reproduce.sh [-n] <tag>`. The third header line is `# docker: <docker buildx version output>` or `# podman <version>`. Docker path output: `ok - goreleaser built the release <tag>`, archive lines/comparison, 4 `check-archives.sh` lines, `ok - two GoReleaser snapshot builds give the same images and archives`, then the smoke test's 10 lines (16 `ok` lines with `-n`); Podman path unchanged (17).

- [ ] **Step 1: Write the test shims**

Two `docker` commands for tests, in the scratchpad (not committed). The first reports a real buildx and runs everything with Podman underneath, so that the Docker path can run on this Docker-less machine (it translates GoReleaser's buildx options to Podman's and the few Docker/Podman differences the smoke test meets); `SHIM_IGNORE_EPOCH=1` makes its builds non-reproducible on purpose.

`$SCRATCH/docker-buildx/docker`:

```bash
#!/bin/bash
# Test shim: a "docker" that reports a real buildx and runs everything with
# Podman underneath, for the Docker path of reproduce.sh on a Docker-less
# machine. Logs its arguments to "log" next to itself.
d=$(dirname "$0")
echo "DOCKER $*" >> "$d/log"
case "$1" in
info)
	# SHIM_ROOTLESS=1: rootless Docker
	if [ "$2" = --format ]; then
		echo "[name=seccomp,profile=builtin${SHIM_ROOTLESS:+ name=rootless} name=cgroupns]"
		exit 0
	fi
	exec podman info > /dev/null
	;;
buildx)
	case "$2" in
	version) echo "github.com/docker/buildx v0.30.0 podman-shim"; exit 0 ;;
	prune) exit 0 ;;
	inspect | ls) echo "Driver: docker-container"; exit 0 ;;
	build)
		# GoReleaser's dockers_v2 snapshot build: buildx options translated to
		# Podman's (rewrite-timestamp from --output, epoch from --build-arg)
		shift 2
		args=() epoch='' iid=''
		while (($#)); do
			case "$1" in
			--load | --output=* | --attest=* | --provenance=*) ;;
			--iidfile=*) iid=${1#--iidfile=} ;;
			--build-arg)
				args+=("$1" "$2")
				[[ $2 == SOURCE_DATE_EPOCH=* ]] && epoch=${2#*=}
				shift
				;;
			--platform | -t | --label | --annotation) args+=("$1" "$2"); shift ;;
			*) args+=("$1") ;;
			esac
			shift
		done
		# SHIM_IGNORE_EPOCH=1: builds dated at build time, to test a mismatch
		[ -z "${SHIM_IGNORE_EPOCH:-}" ] || epoch=''
		id=$(podman build -q --no-cache --layers=false --identity-label=false \
			${epoch:+--source-date-epoch="$epoch" --rewrite-timestamp} "${args[@]}") || exit
		[ -z "$iid" ] || echo "sha256:$id" > "$iid"
		exit 0
		;;
	esac
	;;
image)
	# Docker's .Created is an RFC 3339 string, Podman's a time
	if [ "$2" = inspect ] && [ "$3" = --format ] && [ "$4" = '{{.Created}}' ]; then
		exec podman image inspect --format '{{.Created.Format "2006-01-02T15:04:05Z07:00"}}' "${@:5}"
	fi
	;;
run | create)
	# Rootful Docker: --user=<uid> is that host uid; with rootless Podman, only
	# with keep-id. Rootless Docker (SHIM_ROOTLESS=1) is like plain rootless
	# Podman: the host user is root in the container.
	[ -z "${SHIM_ROOTLESS:-}" ] || exec podman "$@"
	args=("$1")
	shift
	for a; do
		[[ $a == --user=* ]] && args+=(--userns=keep-id)
		args+=("$a")
	done
	exec podman "${args[@]}"
	;;
esac
exec podman "$@"
```

The second mimics Podman's docker emulation (`podman-docker`), which `reproduce.sh` must not take for Docker.

`$SCRATCH/podman-docker/docker`:

```sh
#!/bin/sh
# Test shim: Podman's docker emulation (podman-docker): "buildx version"
# prints Buildah's version
case "$1 $2" in
"buildx version") echo "buildah 1.42.0" ;;
*) exec podman "$@" ;;
esac
```

`chmod +x` both, then `mise exec shellcheck@latest -- shellcheck "$SCRATCH/docker-buildx/docker" "$SCRATCH/podman-docker/docker"` → no output.

- [ ] **Step 2: Prepare a throwaway clone**

```bash
rm -rf "$SCRATCH/clone" && git clone -q . "$SCRATCH/clone"
git -C "$SCRATCH/clone" remote set-url origin https://github.com/dolmen-go/openapi-preprocessor.git
git -C "$SCRATCH/clone" tag -a v1.99.0-repro -m test   # v1: Go ignores v2+ tags for this module path
```

- [ ] **Step 3: Run the current script (RED)**

```bash
cd "$SCRATCH/clone"
# With a real-looking buildx: the Docker path is not taken
PATH="$SCRATCH/docker-buildx:$PATH" mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh -n v1.99.0-repro 2>&1 | grep -E '^# (docker|podman)|snapshot|two builds'
# Without docker: GoReleaser's --skip list has no docker
mise exec go@1.27.1 goreleaser@2.18.2 -- sh -x .goreleaser/reproduce.sh -n v1.99.0-repro 2>&1 | grep -o -- '--skip[= ][^ ]*' | head -1
cd -
```

Expected: `# podman 5.7.0` and the two `ok - linux/…: two builds give the same image …` lines (no snapshot); then `--skip=publish,announce,sbom`.

- [ ] **Step 4: Write the new `reproduce.sh`**

Replace `.goreleaser/reproduce.sh` with:

```sh
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
	rm -rf "$tmp"
}
trap cleanup EXIT

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
		# No BuildKit cache: the second build must not reuse the first one
		docker buildx prune --all --force > /dev/null
		GORELEASER_CURRENT_TAG=$tag goreleaser release --snapshot --clean --skip=sbom > "$tmp/goreleaser.log" 2>&1 || {
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
```

Lint: `mise exec shellcheck@latest -- shellcheck .goreleaser/reproduce.sh` → no output.

- [ ] **Step 5: Run the new script (GREEN), on the three kinds of machine**

```bash
cp .goreleaser/reproduce.sh "$SCRATCH/clone/.goreleaser/"
cd "$SCRATCH/clone"
run() { mise exec go@1.27.1 goreleaser@2.18.2 -- "$@"; }
# 1. No docker
run sh -x .goreleaser/reproduce.sh -n v1.99.0-repro 2>&1 | grep -o -- '--skip=[^ ]*' | head -1
run .goreleaser/reproduce.sh -n v1.99.0-repro | grep -c '^ok'
# 2. Podman's docker emulation
PATH="$SCRATCH/podman-docker:$PATH" run .goreleaser/reproduce.sh -n v1.99.0-repro | grep -E '^# (docker|podman)'
# 3. Docker with buildx (Podman underneath)
: > "$SCRATCH/docker-buildx/log"
PATH="$SCRATCH/docker-buildx:$PATH" run .goreleaser/reproduce.sh -n v1.99.0-repro > "$SCRATCH/docker-run.txt" 2>&1; echo "exit=$?"
grep -E '^# (docker|podman)|snapshot|-version|FAIL' "$SCRATCH/docker-run.txt"; grep -c '^ok' "$SCRATCH/docker-run.txt"
grep -c 'buildx prune' "$SCRATCH/docker-buildx/log"; grep -c 'buildx build' "$SCRATCH/docker-buildx/log"
podman images --format '{{.Repository}}:{{.Tag}}' | grep -c 'SNAPSHOT'
# 4. Docker with buildx, builds not reproducible
SHIM_IGNORE_EPOCH=1 PATH="$SCRATCH/docker-buildx:$PATH" run .goreleaser/reproduce.sh -n v1.99.0-repro 2>&1 | grep FAIL; echo "exit=${PIPESTATUS[0]}"
cd -
```

Expected:
1. `--skip=publish,announce,sbom,docker`; 17 `ok` lines.
2. `# podman 5.7.0` (the emulation is not taken for Docker with buildx).
3. `exit=0`; `# docker: github.com/docker/buildx v0.30.0 podman-shim`; `ok - two GoReleaser snapshot builds give the same images and archives`; `ok - -version: v1.99.0-repro`; 16 `ok` lines; 2 `buildx prune`; 4 `buildx build` (2 platforms × 2 runs); 0 snapshot images left.
4. `FAIL: two snapshot builds give different images or archives`, non-zero exit.

Then remove anything a failed run may have left: `podman images --format '{{.Repository}}:{{.Tag}}' | grep SNAPSHOT | xargs -r podman rmi`.

- [ ] **Step 6: Fold into the existing commit and clean up**

```bash
rm -rf "$SCRATCH/clone" "$SCRATCH/docker-buildx" "$SCRATCH/podman-docker" "$SCRATCH/docker-run.txt"
git add .goreleaser/reproduce.sh
git commit -q --fixup="$(git log --format=%h -1 --grep='^Add a script to reproduce a release locally')"
GIT_SEQUENCE_EDITOR=: git rebase -q -i --autosquash master
git log --oneline master..HEAD
```

Expected: the same 9 commit subjects, no `fixup!` left; `go test ./...` passes.

---

### Task 9: A throwaway buildx builder per Docker build, instead of pruning

Follow-up decided after the Task 8 review (spec: `reproduce.sh`, step 6, Docker): `docker buildx prune --all --force` wiped the user's whole build cache (possibly shared), and a default Docker install builds with the `docker` driver, unlike CI. Each of the two GoReleaser snapshot builds now gets its own `docker-container` builder, selected with `BUILDX_BUILDER` and removed at exit; no prune.

**Files:**
- Modify: `.goreleaser/reproduce.sh`

**Interfaces:**
- Consumes: the Docker path of Task 8 (snapshot builds, `fingerprint.sh`, smoke test).
- Produces: same command line and checks. New header line after the builds start: `# buildkit <version> (docker-container driver)`. New failure: `FAIL: can't create the buildx builder reproduce-<id>-<run>`, after buildx's own message (`<id>`: the random suffix of the script's temporary directory, lowercase).

- [ ] **Step 1: Extend the `docker-buildx` test shim**

The shim of Task 8 must now handle `buildx create` (with `SHIM_CREATE_FAIL=1` to make it fail), `buildx rm`, `buildx inspect` (reporting a BuildKit version, as buildx before 0.13 with `SHIM_BUILDX_OLD=1`), and log the `BUILDX_BUILDER` each command sees. `$SCRATCH/docker-buildx/docker` becomes:

```bash
#!/bin/bash
# Test shim: a "docker" that reports a real buildx and runs everything with
# Podman underneath, for the Docker path of reproduce.sh on a Docker-less
# machine. Logs its arguments to "log" next to itself.
d=$(dirname "$0")
echo "DOCKER $* [BUILDX_BUILDER=${BUILDX_BUILDER-}]" >> "$d/log"
case "$1" in
info)
	# SHIM_ROOTLESS=1: rootless Docker
	if [ "$2" = --format ]; then
		echo "[name=seccomp,profile=builtin${SHIM_ROOTLESS:+ name=rootless} name=cgroupns]"
		exit 0
	fi
	exec podman info > /dev/null
	;;
buildx)
	case "$2" in
	version) echo "github.com/docker/buildx v0.30.0 podman-shim"; exit 0 ;;
	prune | rm) exit 0 ;;
	# SHIM_CREATE_FAIL=1: the builder cannot be created (e.g. no network to
	# pull moby/buildkit)
	create)
		[ -z "${SHIM_CREATE_FAIL:-}" ] || { echo "ERROR: failed to pull moby/buildkit (shim)" >&2; exit 1; }
		exit 0
		;;
	inspect | ls)
		echo "Driver: docker-container"
		# SHIM_BUILDX_OLD=1: buildx before 0.13
		if [ -n "${SHIM_BUILDX_OLD:-}" ]; then
			echo "Buildkit:         v0.12.5-shim"
		else
			echo "BuildKit version: v0.33.1-shim"
		fi
		exit 0
		;;
	build)
		# GoReleaser's dockers_v2 snapshot build: buildx options translated to
		# Podman's (rewrite-timestamp from --output, epoch from --build-arg)
		shift 2
		args=() epoch='' iid=''
		while (($#)); do
			case "$1" in
			--load | --output=* | --attest=* | --provenance=*) ;;
			--iidfile=*) iid=${1#--iidfile=} ;;
			--build-arg)
				args+=("$1" "$2")
				[[ $2 == SOURCE_DATE_EPOCH=* ]] && epoch=${2#*=}
				shift
				;;
			--platform | -t | --label | --annotation) args+=("$1" "$2"); shift ;;
			*) args+=("$1") ;;
			esac
			shift
		done
		# SHIM_IGNORE_EPOCH=1: builds dated at build time, to test a mismatch
		[ -z "${SHIM_IGNORE_EPOCH:-}" ] || epoch=''
		id=$(podman build -q --no-cache --layers=false --identity-label=false \
			${epoch:+--source-date-epoch="$epoch" --rewrite-timestamp} "${args[@]}") || exit
		[ -z "$iid" ] || echo "sha256:$id" > "$iid"
		exit 0
		;;
	esac
	;;
image)
	# Docker's .Created is an RFC 3339 string, Podman's a time
	if [ "$2" = inspect ] && [ "$3" = --format ] && [ "$4" = '{{.Created}}' ]; then
		exec podman image inspect --format '{{.Created.Format "2006-01-02T15:04:05Z07:00"}}' "${@:5}"
	fi
	;;
run | create)
	# Rootful Docker: --user=<uid> is that host uid; with rootless Podman, only
	# with keep-id. Rootless Docker (SHIM_ROOTLESS=1) is like plain rootless
	# Podman: the host user is root in the container.
	[ -z "${SHIM_ROOTLESS:-}" ] || exec podman "$@"
	args=("$1")
	shift
	for a; do
		[[ $a == --user=* ]] && args+=(--userns=keep-id)
		args+=("$a")
	done
	exec podman "${args[@]}"
	;;
esac
exec podman "$@"
```

`mise exec shellcheck@latest -- shellcheck "$SCRATCH/docker-buildx/docker"` → no output.

- [ ] **Step 2: Prepare a throwaway clone**

```bash
rm -rf "$SCRATCH/clone" && git clone -q . "$SCRATCH/clone"
git -C "$SCRATCH/clone" remote set-url origin https://github.com/dolmen-go/openapi-preprocessor.git
git -C "$SCRATCH/clone" tag -a v1.99.0-repro -m test
```

- [ ] **Step 3: Run the current script (RED)**

```bash
cd "$SCRATCH/clone"
: > "$SCRATCH/docker-buildx/log"
PATH="$SCRATCH/docker-buildx:$PATH" mise exec go@1.27.1 goreleaser@2.18.2 -- .goreleaser/reproduce.sh -n v1.99.0-repro > /dev/null 2>&1; echo "exit=$?"
grep -c 'buildx prune' "$SCRATCH/docker-buildx/log"; grep -c 'buildx create' "$SCRATCH/docker-buildx/log"
grep 'buildx build' "$SCRATCH/docker-buildx/log" | grep -o 'BUILDX_BUILDER=[^]]*' | sort | uniq -c
cd -
```

Expected: `exit=0`, 2 prunes, 0 creates, and 4 builds with an empty `BUILDX_BUILDER=` (the user's current builder).

- [ ] **Step 4: Write the new `reproduce.sh`**

Replace `.goreleaser/reproduce.sh` with:

```sh
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
```

Lint: `mise exec shellcheck@latest -- shellcheck .goreleaser/reproduce.sh` → no output.

- [ ] **Step 5: Run the new script (GREEN)**

```bash
cp .goreleaser/reproduce.sh "$SCRATCH/clone/.goreleaser/"
cd "$SCRATCH/clone"
run() { mise exec go@1.27.1 goreleaser@2.18.2 -- "$@"; }
# Docker with buildx (Podman underneath)
: > "$SCRATCH/docker-buildx/log"
before=$(podman images -aq --no-trunc | sort)
PATH="$SCRATCH/docker-buildx:$PATH" run .goreleaser/reproduce.sh -n v1.99.0-repro > "$SCRATCH/run.txt" 2>&1; echo "exit=$?"
grep -E '^# (docker|buildkit)|snapshot|-version|FAIL' "$SCRATCH/run.txt"; grep -c '^ok' "$SCRATCH/run.txt"
grep -c 'buildx prune' "$SCRATCH/docker-buildx/log"
grep 'buildx create' "$SCRATCH/docker-buildx/log" | sed 's/reproduce-[a-z0-9]*-/reproduce-<id>-/'
grep 'buildx build' "$SCRATCH/docker-buildx/log" | grep -o 'BUILDX_BUILDER=[^]]*' | sed 's/reproduce-[a-z0-9]*-/reproduce-<id>-/' | sort | uniq -c
grep 'buildx rm' "$SCRATCH/docker-buildx/log" | sed 's/reproduce-[a-z0-9]*-/reproduce-<id>-/'
echo "images left: $(comm -13 <(echo "$before") <(podman images -aq --no-trunc | sort) | wc -l)"
# The builder cannot be created
: > "$SCRATCH/docker-buildx/log"
SHIM_CREATE_FAIL=1 PATH="$SCRATCH/docker-buildx:$PATH" run .goreleaser/reproduce.sh -n v1.99.0-repro 2>&1 | tail -2; echo "exit=${PIPESTATUS[0]}"
grep -c 'buildx build' "$SCRATCH/docker-buildx/log"
# Podman path unchanged
run .goreleaser/reproduce.sh -n v1.99.0-repro | grep -c '^ok'
cd -
```

Expected:
- Docker path: `exit=0`; `# docker: github.com/docker/buildx v0.30.0 podman-shim`, `# buildkit v0.33.1-shim (docker-container driver)`, `ok - two GoReleaser snapshot builds give the same images and archives`, `ok - -version: v1.99.0-repro`; 16 `ok` lines; 0 prunes; 2 `buildx create --name reproduce-<id>-1|2 --driver docker-container --bootstrap`; 4 builds, 2 with `BUILDX_BUILDER=reproduce-<id>-1` and 2 with `reproduce-<id>-2`; 2 `buildx rm reproduce-<id>-1|2`; 0 images left.
- Builder creation failing: `ERROR: failed to pull moby/buildkit (shim)` then `FAIL: can't create the buildx builder reproduce-<id>-1`, non-zero exit; 0 builds.
- Podman path: 17 `ok` lines.

- [ ] **Step 6: Fold into the existing commit and clean up**

```bash
rm -rf "$SCRATCH/clone" "$SCRATCH/run.txt"
git add .goreleaser/reproduce.sh
git commit -q --fixup="$(git log --format=%h -1 --grep='^Add a script to reproduce a release locally')"
GIT_SEQUENCE_EDITOR=: git rebase -q -i --autosquash master
git log --oneline master..HEAD
```

Expected: the same 10 commit subjects, no `fixup!` left; `go test ./...` passes.

---

### Task 10: CI run and first reproducible release — with the user

Every step is outward-facing: **the user runs it or explicitly approves it.**

- [ ] **Step 1: CI dry run**

Push the branch (with the user's approval) and check the `release-dry-run` job: "Check the build is reproducible" prints the fingerprint and no diff, then 4 `ok - …` archive lines (one per `.tar.gz`); the smoke test prints 10 `ok - …` lines.
If a GoReleaser build fails on the `--output` flag: **stop and report to the user** (no fallback).

- [ ] **Step 2: Release**

After merge, the user tags the next pre-release (e.g. `v1.0.0-rc.3`). The `Release` workflow passes, including "Check the published image" with `ok - image created at SOURCE_DATE_EPOCH` and `ok - image files dated SOURCE_DATE_EPOCH` for both platforms.

- [ ] **Step 3: Reproduce it on this machine**

Read the Go and GoReleaser versions in the release job's logs (`actions/setup-go` and `goreleaser/goreleaser-action` steps), then:

```bash
mise exec go@<go version> goreleaser@<goreleaser version> -- .goreleaser/reproduce.sh v1.0.0-rc.3
```

Expected: `ok - 6 archives identical to the GitHub Release`, then all other checks `ok`, exit 0.

---

## Final review fixes

Made after the whole-branch review, each verified RED then GREEN (the scripts above are the final versions):

- `check-archives.sh`: a missing, unreadable or empty archive now fails (it passed: the pipeline's status was awk's).
- `reproduce.sh`: builds in a private repository holding only the tag (`git init` + `git fetch --no-tags <repo> refs/tags/<tag>:refs/tags/<tag>`), not a `git worktree`: another tag on the same commit (rc and final) changed the version Go stamps and the tag GoReleaser picks. Sets `GORELEASER_CURRENT_TAG` and checks `dist/metadata.json`'s tag; ignores the user's git settings (`core.autocrlf`, attributes, `log.showSignature`).
- `reproduce.sh`: Go isolated from the user's settings: `GOENV=off` (`go env -w`), `GOTOOLCHAIN=local`, `GOFIPS140` unset too; the version header is printed after that.
- `reproduce.sh`: `podman build --layers=false`: no intermediate images left behind.
- `release.yml`: `GORELEASER_CURRENT_TAG: ${{ github.ref_name }}`, so that a final tag pushed on the commit of a pre-release tag is not released as the pre-release.

### Task 7 review fixes

Made after a review of Task 7, each verified RED then GREEN (the Task 7 scripts above are the final versions):

- `reproduce.sh`: the published checksums file must list exactly the local archives and their SBOMs (`<archive>.sbom.json`); any other published file fails ("published files above were not built locally"). RED: a published file with an extra `linux_riscv64.tar.gz` line, served by a `curl` shim, passed. GREEN: it fails; the archives plus SBOMs pass; a missing archive still fails; the real `v1.0.0-rc.2` file is accepted up to the expected "archives differ".
- `fingerprint.sh`: no argument; the images are those of type `Docker Image` in `dist/artifacts.json` (one per platform in a snapshot build), not a hard-coded `amd64 arm64` list. RED: with `linux/riscv64` added to `dockers_v2` (snapshot through a logging `docker` shim that writes the image ID file, images built with Podman), the third image was not fingerprinted. GREEN: 3 image lines; an image missing from the engine fails; no image recorded fails.
- `test.yml`: `fingerprint.sh` called without argument; the dry-run smoke test takes the `linux/amd64` image from `dist/artifacts.json` (`select(.type == "Docker Image" and .extra.Platforms == ["linux/amd64"]) | .name`).

### Task 8 fix during execution

- `reproduce.sh` (Docker path): when the two snapshot builds differ, the second takes the image names of the first, whose images were left untagged (removal was by name). Each run's image IDs (from its fingerprint) are now also removed, one image at a time. RED: 2 untagged images left after the `SHIM_IGNORE_EPOCH=1` run; GREEN: 0, and 0 after the Docker and Podman runs too.

### Task 8 review fixes

Made after a review of Task 8, each verified RED then GREEN (the Task 8 blocks above are the final versions; the `docker-buildx` shim gained `SHIM_ROOTLESS=1`, imitating rootless Docker):

- `reproduce.sh` and `test.yml`: for a final tag (not a pre-release), a snapshot records one `Docker Image` artifact per image tag (`X.Y.Z-SNAPSHOT-…`, `X.Y`, `X`, `latest`), so the `linux/amd64` selector returned 4 names, passed as one reference. Both now take `first(...)`. RED: tag `v1.98.0` through the buildx shim, exit 125 (`invalid reference format`). GREEN: `v1.98.0` and `v1.99.0-repro` both pass (16 `ok`); the `test.yml` selector gives a single name on a final-tag snapshot (`GORELEASER_CURRENT_TAG=v1.98.0`).
- `smoke-test.sh`: on rootless Docker, `--user=<uid>` maps to a subordinate UID that cannot read the owner-only test files; it now runs as `--user=0:0` there (the current user is root in the container), detected by `rootless` in `docker info --format '{{.SecurityOptions}}'`. RED: with `SHIM_ROOTLESS=1`, `FAIL: 10-ref-ext: exit status 1`. GREEN: rootless (shim) passes with `--user=0:0`; rootful (shim) and Podman unchanged.
- `reproduce.sh` cleanup (asked by the user after the review): `rmi -f` by ID also removed a pre-existing image with the same ID (a byte-identical image the user had built and tagged). The images present at start (IDs and names) are now never removed; the script's names are removed first, then its IDs, one by one and without `-f`, so that an image with another name stays. RED: a user-tagged image identical to the script's `linux/amd64` image (same ID `24e183a8…`) was removed. GREEN: it stays with its tag; the Podman path, and the Docker path (shim) on a final tag, an rc tag and with non-reproducible builds, leave no image behind.

### Task 9 fix during execution

- `reproduce.sh`: a builder is recorded for cleanup before `docker buildx create`, not after: with `--bootstrap`, a builder whose bootstrap fails (e.g. the pull of `moby/buildkit`) may still be registered. RED: with `SHIM_CREATE_FAIL=1`, no `buildx rm`. GREEN: `buildx rm reproduce-<pid>-1`; a normal run still creates and removes 2 builders (16 `ok`).

### Task 9 review fixes

Made after a review of Task 9, each verified RED then GREEN through the `docker-buildx` shim (the Task 9 blocks above are the final versions; the shim gained `SHIM_BUILDX_OLD=1`):

- `reproduce.sh`: dash runs no EXIT trap when a signal kills it, so Ctrl-C left both builders behind: with real Docker, running `buildkitd` containers (restart policy `unless-stopped`) and their cache volumes. `HUP`, `INT` and `TERM` now `exit` 129, 130 and 143, which runs the cleanup. RED: SIGINT to the process group during the second GoReleaser build (SIGINT restored with `env --default-signal=INT`, as in a terminal): no `buildx rm`, the temporary directory and run 1's two images left. GREEN: with SIGINT, SIGTERM and SIGHUP, exit 130, 143 and 129, both builders removed, nothing left.
- `reproduce.sh`: builders are named `reproduce-<id>-<run>`, `<id>` being the random suffix of the temporary directory, lowercase as buildx stores names, not the PID: the BuildKit container is named after the builder, so a run in another PID namespace on the same daemon (devcontainer, CI job with the host's socket) could share it. Checked: the names in the shim log (e.g. `reproduce-68chlakn02-1`), 16 `ok`.
- `reproduce.sh`: the BuildKit version in the header also reads buildx's `Buildkit:` line (before 0.13), and says `version unknown` when there is none. RED: with `SHIM_BUILDX_OLD=1`, `# buildkit  (docker-container driver)`. GREEN: `# buildkit v0.12.5-shim (docker-container driver)`, 16 `ok`; current buildx unchanged; Podman path 17 `ok`.
