# Annotated release tags — design

## Goal

Release tags (`v` followed by a semantic version, e.g. `v1.0.0`) are
pushed as annotated tags (`git tag -a`), never as
lightweight tags. Pre-release tags may carry just a one-line message (no
enforcement); public versions carry a more verbose message that includes the
changelog, enforced as a message of at least 3 lines.

The three tags pushed so far (`v1.0.0-rc.1`, `-rc.2`, `-rc.3`) are
lightweight. They stay as they are: published tags are not rewritten.

## Scope

- A git `pre-push` hook, versioned in the repository, that blocks a push of a
  lightweight release tag, or of a public-version tag whose message has fewer
  than 3 lines.
- Enabled once per clone with `git config core.hooksPath .githooks`,
  documented with the release instructions.

Out of scope:

- Any check on GitHub's side ([`release.yml`][release.yml], rulesets). The hook is a safety
  net only: it works in the clones where it is enabled, and
  `git push --no-verify` skips it.
- Writing or managing the changelog, and checking the content of tag
  messages beyond their number of lines.
- Other properties of annotated tags (validity of a signature, target type,
  tag name in the tag object).

[release.yml]: ../../../.github/workflows/release.yml

## Files

- [`.githooks/pre-push`][pre-push] (new, executable): POSIX `sh`, clean
  under shellcheck, like the scripts in [`.goreleaser/`][goreleaser]. Its
  header comment points to this spec
  ([`docs/superpowers/specs/2026-10-15-annotated-release-tags-design.md`][spec]).
- [`.github/workflows/release.yml`][release.yml]: its header comment (the
  release instructions) follows the rules of this spec and gains the
  one-time setup `git config core.hooksPath .githooks`. Its current first
  line, `# To release: git tag -a vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z`,
  gives a public version a 1-line message, which the hook rejects. That line
  becomes:

  ```yaml
  # To release, tag with an annotated tag and push the tag:
  #   git tag -a -m 'Release candidate vX.Y.Z-rc.N' vX.Y.Z-rc.N  (pre-release)
  #   git tag -a vX.Y.Z  (public version: a message of 3 lines or more, with the changelog)
  #   git push origin <tag>
  # Enable the check of release tags once per clone with
  #   git config core.hooksPath .githooks
  ```

  The rest of the header comment is unchanged.

`core.hooksPath` replaces `.git/hooks`: hooks there stop running in a clone
where it is set (this clone has none, only samples). Future hooks of the
project go in [`.githooks/`][githooks].

[pre-push]: ../../../.githooks/pre-push
[goreleaser]: ../../../.goreleaser/
[spec]: 2026-10-15-annotated-release-tags-design.md
[release.yml]: ../../../.github/workflows/release.yml
[githooks]: ../../../.githooks/

## Behaviour

### Release tags and their kinds

The tag name `<tag>` is the remote ref without `refs/tags/`. A **release
tag** is a `<tag>` made of `v` and a version as defined by
[Semantic Versioning 2.0.0][semver]:

- `MAJOR.MINOR.PATCH`, three numbers without leading zeros;
- optionally a pre-release: `-` and dot-separated identifiers of letters,
  digits and `-`, the numeric ones without leading zeros;
- optionally build metadata: `+` and dot-separated identifiers of letters,
  digits and `-`.

As a POSIX extended regular expression (`grep -E`), adapted from the one
of semver.org:

```
^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$
```

Any other tag, even starting with `v` (`vvvv`, `v1.2`, `v01.0.0`,
`v1.0.0-01`), is not a release tag: the hook does not check it.

The kind of a release tag is decided on its version without the build
metadata:

- **Release candidate**: a pre-release whose first identifier is `rc`
  (e.g. `v1.0.0-rc.4`, `v1.0.0-rc.4+build.1`).
- **Other pre-release**: any other pre-release (e.g. `v1.0.0-beta.1`,
  `v1.0.0-rc4`).
- **Public version**: no pre-release. A tag with only build metadata
  (`v1.0.0+build.1`, `v1.0.0+build-1`) is a public version.

This matches GoReleaser (`prerelease: auto` in
[`.goreleaser.yaml`][goreleaser.yaml]), which parses the tag as a semantic
version: a tag with a pre-release suffix makes a GitHub pre-release, and its
image is not tagged `latest`.

[semver]: https://semver.org/spec/v2.0.0.html
[goreleaser.yaml]: ../../../.goreleaser.yaml

### Checks

Git runs the hook with one line per ref being pushed on its standard input:
`<local ref> <local sha> <remote ref> <remote sha>`. The local ref is written
as given on the command line, so it may contain spaces
(`HEAD@{1 day ago}`); the other three fields never do. Refs that are already
up to date on the remote are expected not to be listed (checked by the
`--tags` test below). For each line:

1. The remote ref is not `refs/tags/` followed by a release tag: ignored
   (branches, other tags).
2. The local sha is all zeros: a deletion, ignored.
3. `git cat-file -t <local sha>` is not `tag`: the tag is lightweight,
   reported.
4. Annotated pre-release (release candidate or other): accepted, whatever
   its message.
5. Annotated public version: the lines of its message are counted; fewer
   than 3 is reported.

Counting the lines of a message:

- The message is the content of the tag object after its headers (and the
  blank line that ends them), as stored.
- Every line counts, blank lines included; a last line without a final
  newline counts too.
- The signature of a signed tag, which git stores at the end of the message,
  is not counted: otherwise a signed one-line message would pass. It starts
  at the first line that is one of the 4 markers git recognizes,
  `-----BEGIN PGP SIGNATURE-----`, `-----BEGIN PGP MESSAGE-----`,
  `-----BEGIN SSH SIGNATURE-----` or `-----BEGIN SIGNED MESSAGE-----`, and
  goes to the end.

By default (`-m` or the editor), git cleans up the message: it removes
trailing blank lines and merges consecutive blank lines. The shortest
message that passes is then a subject, a blank line and one line of body.

### Report

The hook reports every rejected tag of the push, one block of two or three
lines each on standard error, then exits 1, which makes git abort the whole
push (branches included). With no rejected tag, it exits 0 and prints
nothing.

Lightweight tag, first line:
`pre-push: <tag> is a lightweight tag; release tags (v*) must be annotated.`

Public version with a short message, first line (`1 line`, else
`<n> lines`):
`pre-push: <tag>: the tag message has <n> lines; public versions need at least 3, including the changelog.`

Second line, where `<target>` is what was pushed (see below):

| Kind | Second line of the block |
|---|---|
| Release candidate (lightweight) | `  To fix it: git tag -a -f -m 'Release candidate <tag>' <tag> <target>` |
| Other pre-release (lightweight) | `  To fix it: git tag -a -f -m 'Pre-release <tag>' <tag> <target>` |
| Public version (lightweight, or short message) | `  Replace it with an annotated tag on the same commit: git tag -a -f <tag> '<target>^{}'` |

The suggested command creates, or replaces in place (`-f`), the local tag
`<tag>` with an annotated tag on the same commit. `<target>` depends on the
local ref of the push:

- **The local tag of the same name** (local ref `refs/tags/<tag>`, as with
  `git push origin <tag>`): `<target>` is `<tag>`, and the block has two
  lines. The push can then be run again.
- **Anything else** (`git push origin HEAD:refs/tags/<tag>`, or a local tag
  with another name): `<target>` is the local sha (40 or 64 hexadecimal
  digits), since there may be no local tag `<tag>`, or one on another
  commit. Running the push again would push the same object, so a third
  line gives the push of the new tag:
  `  Then push it: git push <remote> <tag>`, where `<remote>` is the first
  argument git gives the hook (the name of the remote, or its URL when the
  push names no remote).

The commands are meant to be copied and pasted into a shell, so their
arguments are quoted where a shell could change them:

- `<tag>` and `<target>` are written as they are: a release tag is made
  only of letters, digits, `.`, `-` and `+`, and a sha only of hexadecimal
  digits.
- `<target>^{}` is always single-quoted: `^` is a pattern character in zsh
  with the `extendedglob` option.
- `<remote>` is written as it is when it is made only of letters, digits
  and `.`, `_`, `-`, `/`, `:`, `@`, `+`, `,`, `%`; otherwise (a URL or path
  with a space, a `~`, a `'`…) it is single-quoted, with each `'` in it
  written `'\''`.

For a public version, the command has no `-m`: git opens the editor, and the
message, which includes the changelog, is written by hand. `<target>^{}` is
the commit the pushed object points to: without it, an annotated tag would
get a new tag pointing to the old tag object (a nested tag), and without any
target the new tag would point to `HEAD`, which may no longer be the release
commit.

## Testing

Not committed, as for the scripts of the reproducible release. In the
scratchpad: a bare repository as the remote, and a clone of the local
repository whose `origin` is that bare repository (no network), with
`core.hooksPath` pointing to the hook under test.

- RED: without the hook, a push of a lightweight `v9.9.9` tag succeeds.
- GREEN, with the hook:
  - a lightweight `v9.9.9-rc.1` is rejected with the release-candidate
    command; running that command makes the push pass;
  - a lightweight `v9.9.9-beta.1` is rejected with the pre-release command;
    running that command makes the push pass;
  - a lightweight `v9.9.9` is rejected with the public-version command;
  - a lightweight `v9.9.9-rc.1+build.1` is rejected with the
    release-candidate command, and an annotated `v9.9.9+build-1` with a
    1-line message is rejected as a public version (kinds decided without
    the build metadata);
  - a lightweight `v9.8.6-rc.1` and a lightweight `v9.8.6`, each pushed
    from `HEAD:refs/tags/<tag>`, are rejected with the command on the local
    sha and the line `Then push it: git push origin <tag>`; running both
    commands makes the push pass, and the new tag points to the commit;
  - an annotated tag with a 1-line message, pushed under another name
    (`<local tag>:refs/tags/v9.8.5`), is rejected with the public-version
    command on `<local sha>^{}` and the push line; running both commands
    makes the push pass, and the new tag points to the commit, not to the
    old tag object;
  - a lightweight tag pushed from a local ref with spaces
    (`HEAD@{0 seconds ago}:refs/tags/v9.8.7`) is rejected;
  - a lightweight `v9.8.4` pushed from `HEAD` to a remote given by its path,
    which contains a space and a `'`, is rejected with that path
    single-quoted in the push line; running both commands makes the push
    pass;
  - an annotated `v9.9.9` with a 1-line message (`a`) and with a 2-line
    message (`a⏎b`) is rejected with the line count and the public-version
    command;
  - an annotated `v9.9.9` with a 3-line message (`a⏎⏎b`) is pushed;
  - a signed `v9.9.9` with a 1-line message (signed with an SSH key
    generated in the scratchpad) is rejected with `1 line`;
  - a `v9.9.9` whose 1-line message is followed by a
    `-----BEGIN PGP MESSAGE-----` block (a tag object written with
    `git mktag`) is rejected with `1 line`;
  - running the public-version command, with an editor scripted to write a
    3-line message, makes the push pass, and the new tag points to the
    commit;
  - an annotated `v9.9.9-rc.2` with a 1-line message is pushed;
  - each rejection leaves the remote unchanged;
  - lightweight tags that are not release tags are pushed: `foo`, `vvvv`,
    `v9.9`, `v09.9.9`, `v9.9.9-01`;
  - a lightweight `v9.9.9+001` (leading zeros are allowed in build
    metadata) is rejected;
  - a lightweight `v9.9.9-rc4` is rejected with the pre-release command;
  - deleting a release tag on the remote goes through;
  - a push of a branch and a lightweight release tag together is rejected
    entirely (the branch is not updated on the remote);
  - two rejected release tags in one push give two blocks;
  - `git push --tags` passes when the only lightweight release tags are
    already on the remote (as `v1.0.0-rc.1` to `-rc.3` are on GitHub).
- `shellcheck .githooks/pre-push`: no output.
