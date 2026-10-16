# Annotated Release Tags Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A versioned git `pre-push` hook that blocks the push of a lightweight release tag (`v` and a semantic version), or of a public-version tag whose message has fewer than 3 lines, and says how to fix it.

**Architecture:** One POSIX `sh` script, [`.githooks/pre-push`][pre-push], enabled once per clone with `git config core.hooksPath .githooks`. It reads the refs git is about to push on its standard input, keeps the tags whose name matches the spec's semver regular expression (`grep -E`), decides their kind on the version without build metadata, checks them with `git cat-file`, prints one report block per rejected tag on standard error and exits 1. The fix commands of the report use the local tag or the pushed object, depending on the local ref, and are quoted for a shell. The tests are a bash script kept outside the repository: they push to local bare repositories from a clone of the local repository (no network).

**Tech Stack:** POSIX `sh`, git (2.53 here), `grep -E`, `awk`, `sed`, shellcheck (via `mise exec shellcheck@latest --`), dash for a portability run, bash for the tests, `ssh-keygen` for the signed-tag test, actionlint for the workflow.

**Spec:** [`docs/superpowers/specs/2026-10-15-annotated-release-tags-design.md`][spec]

[pre-push]: ../../../.githooks/pre-push
[spec]: ../specs/2026-10-15-annotated-release-tags-design.md

## Global Constraints

- [`.githooks/pre-push`][pre-push]: POSIX `sh`, clean under shellcheck, executable (git mode `100755`); its header comment points to the spec.
- Release tag: the remote ref is `refs/tags/<tag>`, where `<tag>` matches, with `grep -E`:
  `^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`
  Any other ref is not checked. Deletions (local sha all zeros, 40 or 64) are ignored.
- Kinds, on the version without its build metadata (from the first `+`): release candidate when the pre-release's first identifier is `rc`; other pre-release for any other pre-release; public version without pre-release.
- Lightweight release tag: rejected. Annotated pre-release: accepted whatever its message. Annotated public version: rejected if its message has fewer than 3 lines (as stored, blank lines included, a last line without a final newline counted, the signature not counted: from the first line `-----BEGIN PGP SIGNATURE-----`, `-----BEGIN PGP MESSAGE-----`, `-----BEGIN SSH SIGNATURE-----` or `-----BEGIN SIGNED MESSAGE-----` to the end).
- The local ref of a hook input line may contain spaces; the other three fields never do.
- Report, on standard error, one block of two or three lines per rejected tag, then exit 1; nothing printed and exit 0 otherwise. Lines, verbatim:
  - `pre-push: <tag> is a lightweight tag; release tags (v*) must be annotated.`
  - `pre-push: <tag>: the tag message has <n> lines; public versions need at least 3, including the changelog.` (`1 line` for one line)
  - `  To fix it: git tag -a -f -m 'Release candidate <tag>' <tag> <target>` (release candidate)
  - `  To fix it: git tag -a -f -m 'Pre-release <tag>' <tag> <target>` (other pre-release)
  - `  Replace it with an annotated tag on the same commit: git tag -a -f <tag> '<target>^{}'` (public version)
  - `  Then push it: git push <remote> <tag>` (third line, only when `<target>` is the local sha)
- `<target>` is `<tag>` when the local ref is `refs/tags/<tag>`, else the local sha. `<remote>` is the hook's first argument, written as it is when made only of letters, digits and `.`, `_`, `-`, `/`, `:`, `@`, `+`, `,`, `%`, else single-quoted with each `'` written `'\''`.
- [`.github/workflows/release.yml`][release.yml]: the first line of its header comment, `# To release: git tag -a vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z`, is replaced by the 6 lines given in the spec (Files section); the rest of the header comment is unchanged.
- No check on GitHub's side; the changelog is written by hand.
- Tests are not committed. They run git without the user's or system configuration (`GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1`), on a clone of the local repository, never reaching GitHub.

[pre-push]: ../../../.githooks/pre-push
[release.yml]: ../../../.github/workflows/release.yml

## Review Focus

- A pre-release whose first identifier only starts with `rc` (`v9.9.9-rc-1`): another pre-release, not a release candidate — Task 1 test "lightweight pre-release rc-1 (not a release candidate)".
- An uppercase `V9.9.9`: not a release tag, pushed unchecked — Task 1 test "lightweight V9.9.9 (not a release tag)".
- A push spelled `<tag>:<tag>` (`git push origin v9.7.7:v9.7.7`): git gives the local ref as `refs/tags/v9.7.7`, so the block has two lines and `<target>` is the tag — Task 1 test "lightweight tag pushed as <tag>:<tag>: the local tag is fixed".
- A branch named like a release tag (`refs/heads/v9.7.6`): not checked — Task 1 test "branch named like a release tag".
- `git push --follow-tags`: the annotated tags git adds to the push are checked — Task 1 test "--follow-tags: annotated public version, 1-line message".

---

### Task 1: Hook rejecting lightweight release tags and short public-version messages

**Files:**
- Create: [`.githooks/pre-push`][pre-push]
- Modify: [`.github/workflows/release.yml`][release.yml] (header comment, line 3)
- Test: `$SCRATCH/test-pre-push.sh` (not committed)

`$SCRATCH` is a scratch directory outside the repository (the session's scratchpad). Commands run from the repository's top directory.

**Interfaces:**
- Consumes: nothing.
- Produces: [`.githooks/pre-push`][pre-push], enabled by Task 2.

- [ ] **Step 1: Write the test script**

`$SCRATCH/test-pre-push.sh`:

```bash
#!/bin/bash
# Tests of the pre-push hook of .githooks/ (spec:
# docs/superpowers/specs/2026-10-15-annotated-release-tags-design.md).
# Usage: test-pre-push.sh <repo> <hooks dir>
#   <repo>       the local repository, cloned (no network)
#   <hooks dir>  core.hooksPath of the clone: <repo>/.githooks, or an empty
#                directory for the RED run
set -u
repo=$(cd "$1" && pwd)
hooks=$(cd "$2" && pwd)
# Hermetic git: no user or system configuration (signing, hooks, aliases)
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=Test GIT_AUTHOR_EMAIL=test@example.invalid
export GIT_COMMITTER_NAME=Test GIT_COMMITTER_EMAIL=test@example.invalid
t=$(mktemp -d)
trap 'rm -rf "$t"' EXIT

# The editor for the public-version command: a 3-line message
cat > "$t/editor" <<'EOF'
#!/bin/sh
printf 'Release\n\n- Changelog line\n' > "$1"
EOF
chmod +x "$t/editor"
export GIT_EDITOR=$t/editor

pass=0 fail=0
ok() {
	echo "ok - $*"
	pass=$((pass + 1))
}
not_ok() {
	echo "not ok - $*"
	fail=$((fail + 1))
}

# Expected report lines. <target> defaults to <tag> (pushed from the local
# tag of the same name)
lightweight() { echo "pre-push: $1 is a lightweight tag; release tags (v*) must be annotated."; }
short() { echo "pre-push: $1: the tag message has $2; public versions need at least 3, including the changelog."; }
fix_rc() { echo "  To fix it: git tag -a -f -m 'Release candidate $1' $1 ${2:-$1}"; }
fix_pre() { echo "  To fix it: git tag -a -f -m 'Pre-release $1' $1 ${2:-$1}"; }
fix_public() { echo "  Replace it with an annotated tag on the same commit: git tag -a -f $1 '${2:-$1}^{}'"; }
then_push() { echo "  Then push it: git push $1 $2"; }

# The remote of push_ok and push_rejected
remote=origin

# push_ok <description> <push args>...: the push succeeds
push_ok() {
	local desc=$1
	shift
	if git push -q "$remote" "$@" > "$t/out" 2>&1; then
		ok "$desc"
	else
		not_ok "$desc: push failed"
		sed 's/^/#   /' "$t/out"
	fi
}

# push_rejected <description> <expected report> <push args>...: the push
# fails with exactly the expected report, and leaves the remote unchanged
push_rejected() {
	local desc=$1 want=$2 before
	shift 2
	before=$(git ls-remote "$remote")
	if git push -q "$remote" "$@" > "$t/out" 2>&1; then
		not_ok "$desc: push succeeded"
	elif [ "$(grep -E '^(pre-push: |  )' "$t/out")" != "$want" ]; then
		not_ok "$desc: unexpected report"
		sed 's/^/#   /' "$t/out"
	elif [ "$(git ls-remote "$remote")" != "$before" ]; then
		not_ok "$desc: remote changed"
	else
		ok "$desc"
	fi
}

# fix_and_push <tag>: run the commands suggested by the last rejected push
# (with the scripted editor): the fix, after which <tag> must be an
# annotated tag of a commit, then the suggested push (if none, the push of
# refs/tags/<tag> to $remote)
fix_and_push() {
	local cmd push
	cmd=$(sed -n -e 's/^  To fix it: //p' \
		-e 's/^  Replace it with an annotated tag on the same commit: //p' "$t/out")
	push=$(sed -n 's/^  Then push it: //p' "$t/out")
	if [ -z "$cmd" ]; then
		not_ok "$1: no command suggested"
	elif ! eval "$cmd" > /dev/null 2>&1; then
		not_ok "$1: suggested command failed: $cmd"
	elif [ "$(git cat-file -t "$1")" != tag ] ||
		[ "$(git cat-file -p "$1" | sed -n 's/^type //p')" != commit ]; then
		not_ok "$1: not an annotated tag of a commit after: $cmd"
	elif [ -z "$push" ]; then
		push_ok "$1: pushed after: $cmd" "refs/tags/$1"
	elif eval "$push" > "$t/out" 2>&1; then
		ok "$1: pushed after: $cmd; $push"
	else
		not_ok "$1: suggested push failed: $push"
		sed 's/^/#   /' "$t/out"
	fi
}

git init -q --bare "$t/remote.git"
git clone -q "$repo" "$t/clone"
cd "$t/clone" || exit 1
git remote set-url origin "$t/remote.git"
git config core.hooksPath "$hooks"
# The remote as on GitHub: master and the published tags (v1.0.0-rc.1 to
# -rc.3 are lightweight)
git push -q --no-verify origin HEAD:refs/heads/master --tags
head=$(git rev-parse HEAD)

# Tags already on the remote are not checked again
git tag -a -m 'Release candidate v9.9.5-rc.1' v9.9.5-rc.1
push_ok "--tags: only the new annotated tag is pushed" --tags

# Lightweight tags, by kind
git tag v9.9.9-rc.1
push_rejected "lightweight release candidate" \
	"$(lightweight v9.9.9-rc.1; fix_rc v9.9.9-rc.1)" refs/tags/v9.9.9-rc.1
fix_and_push v9.9.9-rc.1
git tag v9.9.9-beta.1
push_rejected "lightweight pre-release" \
	"$(lightweight v9.9.9-beta.1; fix_pre v9.9.9-beta.1)" refs/tags/v9.9.9-beta.1
fix_and_push v9.9.9-beta.1
git tag v9.9.9-rc4
push_rejected "lightweight pre-release rc4 (not a release candidate)" \
	"$(lightweight v9.9.9-rc4; fix_pre v9.9.9-rc4)" refs/tags/v9.9.9-rc4
git tag v9.9.9-rc-1
push_rejected "lightweight pre-release rc-1 (not a release candidate)" \
	"$(lightweight v9.9.9-rc-1; fix_pre v9.9.9-rc-1)" refs/tags/v9.9.9-rc-1
git tag v9.9.9
push_rejected "lightweight public version" \
	"$(lightweight v9.9.9; fix_public v9.9.9)" refs/tags/v9.9.9
fix_and_push v9.9.9

# Kinds are decided without the build metadata
git tag v9.9.9+build.1
push_rejected "lightweight public version with build metadata" \
	"$(lightweight v9.9.9+build.1; fix_public v9.9.9+build.1)" refs/tags/v9.9.9+build.1
git tag v9.9.9+001
push_rejected "lightweight public version, build metadata with leading zeros" \
	"$(lightweight v9.9.9+001; fix_public v9.9.9+001)" refs/tags/v9.9.9+001
git tag v9.9.9-rc.1+build.1
push_rejected "lightweight release candidate with build metadata" \
	"$(lightweight v9.9.9-rc.1+build.1; fix_rc v9.9.9-rc.1+build.1)" refs/tags/v9.9.9-rc.1+build.1
git tag -a -m 'Release' v9.9.9+build-1
push_rejected "public version with a hyphen in the build metadata, 1-line message" \
	"$(short v9.9.9+build-1 '1 line'; fix_public v9.9.9+build-1)" refs/tags/v9.9.9+build-1

# Not pushed from the local tag of the same name: the commands use the
# local sha, and the new tag is pushed
git tag lw-local
push_rejected "lightweight tag pushed as v9.9.4" \
	"$(lightweight v9.9.4; fix_public v9.9.4 "$head"; then_push origin v9.9.4)" \
	lw-local:refs/tags/v9.9.4
fix_and_push v9.9.4
push_rejected "lightweight release candidate pushed from HEAD" \
	"$(lightweight v9.8.6-rc.1; fix_rc v9.8.6-rc.1 "$head"; then_push origin v9.8.6-rc.1)" \
	HEAD:refs/tags/v9.8.6-rc.1
fix_and_push v9.8.6-rc.1
push_rejected "lightweight public version pushed from HEAD" \
	"$(lightweight v9.8.6; fix_public v9.8.6 "$head"; then_push origin v9.8.6)" \
	HEAD:refs/tags/v9.8.6
fix_and_push v9.8.6
git tag -a -m 'Release' ann-local
push_rejected "annotated tag, 1-line message, pushed as v9.8.5" \
	"$(short v9.8.5 '1 line'; fix_public v9.8.5 "$(git rev-parse ann-local)"; then_push origin v9.8.5)" \
	ann-local:refs/tags/v9.8.5
fix_and_push v9.8.5
# The local ref is given to the hook as written, spaces included
push_rejected "lightweight tag pushed from a source with spaces" \
	"$(lightweight v9.8.7; fix_public v9.8.7 "$head"; then_push origin v9.8.7)" \
	'HEAD@{0 seconds ago}:refs/tags/v9.8.7'
# A remote given by a path with a space and a ': single-quoted
remote="$t/re mote'.git"
git init -q --bare "$remote"
push_rejected "lightweight tag pushed to a path with a space and a quote" \
	"$(lightweight v9.8.4; fix_public v9.8.4 "$head"; then_push "'$t/re mote'\\''.git'" v9.8.4)" \
	HEAD:refs/tags/v9.8.4
fix_and_push v9.8.4
remote=origin

# Not release tags: not checked
for tag in foo vvvv v9.9 v09.9.9 v9.9.9-01 V9.9.9; do
	git tag "$tag"
	push_ok "lightweight $tag (not a release tag)" "refs/tags/$tag"
done
push_ok "branch named like a release tag" HEAD:refs/heads/v9.7.6

# Accepted
git tag -a -m 'Release' -m '- Changelog line' v9.9.8
push_ok "annotated public version" refs/tags/v9.9.8
git tag v9.7.7
push_rejected "lightweight tag pushed as <tag>:<tag>: the local tag is fixed" \
	"$(lightweight v9.7.7; fix_public v9.7.7)" v9.7.7:v9.7.7
git tag v8.8.8
git push -q --no-verify origin refs/tags/v8.8.8
push_ok "deletion of a lightweight release tag" --delete refs/tags/v8.8.8

# The whole push is rejected, and each rejected tag is reported
git switch -q -c topic
git commit -q --allow-empty -m topic
git tag v9.9.7
push_rejected "branch and lightweight tag together" \
	"$(lightweight v9.9.7; fix_public v9.9.7)" refs/heads/topic refs/tags/v9.9.7
git tag v9.9.6
git tag v9.9.6-rc.1
push_rejected "two lightweight tags: two reports" \
	"$(lightweight v9.9.6; fix_public v9.9.6; lightweight v9.9.6-rc.1; fix_rc v9.9.6-rc.1)" \
	refs/tags/v9.9.6 refs/tags/v9.9.6-rc.1

# Messages of public versions: at least 3 lines
git tag -a -m 'Release' v9.9.3
push_rejected "public version, 1-line message" \
	"$(short v9.9.3 '1 line'; fix_public v9.9.3)" refs/tags/v9.9.3
fix_and_push v9.9.3
printf 'Release\n- Changelog line\n' > "$t/msg"
git tag -a -F "$t/msg" v9.9.2
push_rejected "public version, 2-line message" \
	"$(short v9.9.2 '2 lines'; fix_public v9.9.2)" refs/tags/v9.9.2
git tag -a -m 'Release' -m '- Changelog line' v9.9.1
push_ok "public version, 3-line message" refs/tags/v9.9.1
printf 'Release\n\n- Changelog line' > "$t/msg"
git tag -a --cleanup=verbatim -F "$t/msg" v9.9.0
push_ok "public version, 3 lines without a final newline" refs/tags/v9.9.0
ssh-keygen -q -t ed25519 -N '' -C test -f "$t/key"
git -c gpg.format=ssh -c user.signingkey="$t/key.pub" tag -s -m 'Release' v9.8.9
push_rejected "signed public version, 1-line message" \
	"$(short v9.8.9 '1 line'; fix_public v9.8.9)" refs/tags/v9.8.9
printf 'object %s\ntype commit\ntag v9.7.9\ntagger Test <test@example.invalid> 0 +0000\n\nRelease\n-----BEGIN PGP MESSAGE-----\n\nAAAA\n-----END PGP MESSAGE-----\n' \
	"$(git rev-parse HEAD)" > "$t/tag"
git update-ref refs/tags/v9.7.9 "$(git mktag < "$t/tag")"
push_rejected "public version with a PGP MESSAGE signature, 1-line message" \
	"$(short v9.7.9 '1 line'; fix_public v9.7.9)" refs/tags/v9.7.9
git tag -a -m 'Release candidate v9.8.8-rc.2' v9.8.8-rc.2
push_ok "pre-release, 1-line message" refs/tags/v9.8.8-rc.2

# SHA-256 repository: deletions have 64 zeros
git init -q --bare --object-format=sha256 "$t/remote256.git"
git init -q --object-format=sha256 "$t/r256"
cd "$t/r256" || exit 1
git config core.hooksPath "$hooks"
git remote add origin "$t/remote256.git"
git commit -q --allow-empty -m init
git tag v1.0.0
git push -q --no-verify origin HEAD:refs/heads/master refs/tags/v1.0.0
git tag v1.0.1
push_rejected "SHA-256: lightweight tag" \
	"$(lightweight v1.0.1; fix_public v1.0.1)" refs/tags/v1.0.1
push_ok "SHA-256: deletion" --delete refs/tags/v1.0.0
# --follow-tags pushes the annotated tags of the pushed commits
git tag -a -m 'Release' v1.0.2
push_rejected "--follow-tags: annotated public version, 1-line message" \
	"$(short v1.0.2 '1 line'; fix_public v1.0.2)" --follow-tags HEAD:refs/heads/master

echo "# $pass passed, $fail failed"
[ "$fail" = 0 ]
```

- [ ] **Step 2: Run it without the hook (RED)**

```bash
mkdir -p "$SCRATCH/no-hooks"
bash "$SCRATCH/test-pre-push.sh" . "$SCRATCH/no-hooks"; echo "exit=$?"
```

Expected: last lines `# 14 passed, 33 failed` and `exit=1`.
- `ok` for the 14 cases that must pass anyway: `--tags: only the new annotated tag is pushed`, the 6 `lightweight … (not a release tag)` (`foo`, `vvvv`, `v9.9`, `v09.9.9`, `v9.9.9-01`, `V9.9.9`), `branch named like a release tag`, `annotated public version`, `deletion of a lightweight release tag`, `public version, 3-line message`, `public version, 3 lines without a final newline`, `pre-release, 1-line message`, `SHA-256: deletion`;
- `not ok … push succeeded` for the 24 rejection cases;
- `not ok …: no command suggested` for the 9 `fix_and_push` (`v9.9.9-rc.1`, `v9.9.9-beta.1`, `v9.9.9`, `v9.9.4`, `v9.8.6-rc.1`, `v9.8.6`, `v9.8.5`, `v9.8.4`, `v9.9.3`).

- [ ] **Step 3: Write the hook**

[`.githooks/pre-push`][pre-push]:

```sh
#!/bin/sh
# Git pre-push hook: release tags (v and a semantic version, e.g. v1.0.0 or
# v1.0.0-rc.1) must be annotated (git tag -a), and the message of a public
# version (no pre-release) must have at least 3 lines, to include the
# changelog.
# Spec: docs/superpowers/specs/2026-10-15-annotated-release-tags-design.md
#
# Enable it once per clone: git config core.hooksPath .githooks
#
# Git gives the name of the remote (or its URL) and its URL as arguments,
# and one line per ref to update on the remote (refs already up to date are
# not listed):
#   <local ref> <local sha> <remote ref> <remote sha>
set -eu

# A release tag: v and a semantic version (https://semver.org/spec/v2.0.0.html)
release_tag='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'

# message_lines <sha>: the number of lines of the message of the annotated
# tag <sha>, as stored (blank lines included), without the signature of a
# signed tag
message_lines() {
	git cat-file tag "$1" | awk '
		!body { if ($0 == "") body = 1; next }
		/^-----BEGIN (PGP SIGNATURE|PGP MESSAGE|SSH SIGNATURE|SIGNED MESSAGE)-----$/ { exit }
		{ n++ }
		END { print n + 0 }'
}

# shell_word <string>: <string> as is if it has only characters that no
# shell changes, else single-quoted
shell_word() {
	case "$1" in
	*[!A-Za-z0-9._/:@+,%-]*) printf "'%s'" "$(printf %s "$1" | sed "s/'/'\\\\''/g")" ;;
	*) printf %s "$1" ;;
	esac
}

remote=$(shell_word "$1")
status=0
while read -r line; do
	# The local ref is written as given on the command line, so it may contain
	# spaces (HEAD@{1 day ago}): the other fields are taken from the end
	line=${line% *}
	remote_ref=${line##* }
	line=${line% *}
	local_sha=${line##* }
	local_ref=${line% *}
	case "$remote_ref" in
	refs/tags/*) ;;
	*) continue ;;
	esac
	tag=${remote_ref#refs/tags/}
	printf '%s\n' "$tag" | grep -Eq "$release_tag" || continue
	# Deletion: the local sha is all zeros (40 or 64)
	case "$local_sha" in
	*[!0]*) ;;
	*) continue ;;
	esac

	# The kind of tag, decided without the build metadata
	version=${tag%%+*}
	case "$version" in
	*-*)
		case "${version#*-}" in
		rc | rc.*) kind=rc ;;
		*) kind=pre ;;
		esac
		;;
	*) kind=public ;;
	esac

	if [ "$(git cat-file -t "$local_sha")" != tag ]; then
		echo "pre-push: $tag is a lightweight tag; release tags (v*) must be annotated." >&2
	else
		# Annotated: only the message of a public version is checked
		[ "$kind" = public ] || continue
		lines=$(message_lines "$local_sha")
		[ "$lines" -lt 3 ] || continue
		if [ "$lines" = 1 ]; then
			lines="1 line"
		else
			lines="$lines lines"
		fi
		echo "pre-push: $tag: the tag message has $lines; public versions need at least 3, including the changelog." >&2
	fi

	# Pushed from the local tag of the same name, the fix replaces it and the
	# push can be run again; else the fix uses the pushed object, and the new
	# tag must be pushed
	if [ "$local_ref" = "$remote_ref" ]; then
		target=$tag
	else
		target=$local_sha
	fi
	case "$kind" in
	rc) echo "  To fix it: git tag -a -f -m 'Release candidate $tag' $tag $target" >&2 ;;
	pre) echo "  To fix it: git tag -a -f -m 'Pre-release $tag' $tag $target" >&2 ;;
	public) echo "  Replace it with an annotated tag on the same commit: git tag -a -f $tag '$target^{}'" >&2 ;;
	esac
	[ "$target" = "$tag" ] || echo "  Then push it: git push $remote $tag" >&2
	status=1
done
exit "$status"
```

```bash
chmod +x .githooks/pre-push
```

- [ ] **Step 4: Run the tests with the hook (GREEN), with sh and with dash**

```bash
bash "$SCRATCH/test-pre-push.sh" . .githooks; echo "exit=$?"
mkdir -p "$SCRATCH/dash-hooks"
{ echo '#!/bin/dash'; tail -n +2 .githooks/pre-push; } > "$SCRATCH/dash-hooks/pre-push"
chmod +x "$SCRATCH/dash-hooks/pre-push"
bash "$SCRATCH/test-pre-push.sh" . "$SCRATCH/dash-hooks" | tail -1
```

Expected: 47 `ok` lines, among them `v9.9.9: pushed after: git tag -a -f v9.9.9 'v9.9.9^{}'` and `v9.8.4: pushed after: git tag -a -f v9.8.4 '<sha>^{}'; git push '<tmp>/re mote'\''.git' v9.8.4`; no `not ok`; `# 47 passed, 0 failed` and `exit=0`; the dash run's last line `# 47 passed, 0 failed`.

- [ ] **Step 5: Lint**

```bash
mise exec shellcheck@latest -- shellcheck .githooks/pre-push "$SCRATCH/test-pre-push.sh"
```

Expected: no output. (Run `mise exec` from the repository: it reads the user's `.config/mise.toml`; if it starts installing or upgrading the tools listed there, report it.)

- [ ] **Step 6: Release instructions in [`release.yml`][release.yml]**

As required by [the spec][spec] (Files section): the current instruction (`git tag -a vX.Y.Z -m vX.Y.Z`) gives a public version a 1-line message, which the hook rejects. In [`.github/workflows/release.yml`][release.yml], replace line 3:

```yaml
# To release: git tag -a vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z
```

with:

```yaml
# To release, tag with an annotated tag and push the tag:
#   git tag -a -m 'Release candidate vX.Y.Z-rc.N' vX.Y.Z-rc.N  (pre-release)
#   git tag -a vX.Y.Z  (public version: a message of 3 lines or more, with the changelog)
#   git push origin <tag>
# Enable the check of release tags once per clone with
#   git config core.hooksPath .githooks
```

The rest of the header comment (lines 4 to 7) is unchanged.

Check (actionlint runs shellcheck on the `run:` scripts, so both tools are given to `mise exec`):

```bash
mise exec actionlint@latest shellcheck@latest -- actionlint .github/workflows/release.yml
```

Expected: no output.

- [ ] **Step 7: Commit, and check the executable bit**

```bash
git add .githooks/pre-push .github/workflows/release.yml
git ls-files -s .githooks/pre-push | cut -c1-6
git commit -q -F - <<'EOF'
Git hook: check release tags before pushing them

A pre-push hook, enabled with core.hooksPath, rejects a push of a
lightweight release tag (v and a semantic version), or of a public-version
tag (no pre-release; build metadata does not count) whose message, which
includes the changelog, has fewer than 3 lines (the signature of a signed
tag is not counted). It suggests the commands that fix the tag, quoted for
a shell, and the push of the new tag when it was not pushed from the local
tag of the same name. The release instructions now pass the hook.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
git log --oneline master..HEAD
```

Expected: `100755` (else `git update-index --chmod=+x .githooks/pre-push` before committing); 3 commits: the spec, this plan, this task.

[pre-push]: ../../../.githooks/pre-push
[release.yml]: ../../../.github/workflows/release.yml
[spec]: ../specs/2026-10-15-annotated-release-tags-design.md

---

### Task 2: Enable and ship — with the user

Every step changes the user's clone or is outward-facing: **the user runs it or explicitly approves it.**

- [ ] **Step 1: Enable the hook in this clone**

```bash
git config core.hooksPath .githooks
git config --get core.hooksPath
```

Expected: `.githooks`.

- [ ] **Step 2: Push the branch and merge**

The user pushes the branch (the hook runs; no tag is pushed, so it prints nothing) and merges it. The next release tag is created with the instructions of [`release.yml`][release.yml].

[release.yml]: ../../../.github/workflows/release.yml
