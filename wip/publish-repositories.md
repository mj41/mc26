# Publish the repositories

Status: **decision** — the owner's. Nothing here is done by a command until the owner says so.

## What exists (2026-09-09)

Five local repositories under one parent directory, all committed, nothing pushed, and none of
them exists on GitHub yet (`gh repo view mj41/<name>` fails for all five):

| local | branches | tags | remote (set, unreachable) |
|---|---|---|---|
| `mc26` (this repo) | `main` | – | `git@github.com:mj41/mc26.git` |
| `mc26-data` | `main`, `mc-26.1`, `mc-26.2` | `v0.261.2`, `v0.262.2` (with `docs/`, 2026-09-09) | `mj41/mc26-data` |
| `mc26-data-pre` | `main`, `mc-26.3-pre-2`, `mc-26.3-pre-3` | `v0.263.0-pre2.0`, `v0.263.0-pre3.0` | `mj41/mc26-data-pre` |
| `go-mc26` | `main`, `mc-26.1`, `mc-26.2` | `v0.261.0`, `v0.262.0` | `mj41/go-mc26` |
| `go-mc26-examples` | `main` | – | `mj41/go-mc26-examples` |

The library tags `v0.261.0` and `v0.262.0` were made on 2026-09-05 and predate everything the
generators learned since (they hold the first build: hand-written packets, raw registries). The
data tags are older extractions too.

## Steps

1. Re-release before anything is public, so the first published tags are the current state:
   delete the local tags and branches `mc-26.1`/`mc-26.2` in `mc26-data` and `go-mc26` (they
   were never pushed, so this is allowed once), then `mc26 update --version 26.2` and
   `mc26 update --version 26.1` (each extracts, checks, builds, verifies every version, commits
   and tags). Keep `mc26-data-pre` as it is.
2. Create the GitHub repositories (public): `mc26`, `mc26-data`, `mc26-data-pre`, `go-mc26`,
   `go-mc26-examples`. The local remotes already point at those names.
3. Push `mc26` `main`; push the data and library repositories with
   `mc26 release --version 26.2 --skip-extract --no-smoke --push` and the same for 26.1
   (`release` pushes the branch and the tag of each repository), or push by hand:
   `main` first (the README index), then `mc-<version>` and its tag.
4. `go-mc26-examples`: with the library tag public, drop the `go.work`, `go mod tidy` against
   `go-mc26@v0.262.0`, commit, push `main`.
5. Check `go get github.com/mj41/go-mc26@v0.262.0` from an empty module and
   `https://pkg.go.dev/github.com/mj41/go-mc26@v0.262.0` (the first fetch makes the proxy and
   the checksum database record the tag; from then on the tag is immutable — never move or
   delete a published tag, publish a patch instead).
6. For the workflows (`.github/workflows/release.yml`): the `MC26_PUSH_TOKEN` secret (a
   fine-grained token with contents:write on the four generated repositories) in `mc26`.

## Rules that hold

- A published tag never moves. A wrong one is followed by the next patch and a `retract` line.
- Nothing private in any of the five: no private repositories, hosts, infrastructure or the
  name of the owner's deployments, also not in branch names and commit messages. `git log
  --all | grep -i` for them before the first push.
