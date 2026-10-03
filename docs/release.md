# Releasing

## What a release is

For one Minecraft version:

1. the extracted JSON, committed on branch `mc-<version>` of `mc26-data` (or `mc26-data-pre` for
   an id with a suffix), tagged `v0.<YYN>.<n>` — `n` counts extractions of the same version with a
   newer extractor — together with `docs/`, the version's documentation rendered from
   `gen/docs/*.mc26tmpl.md` (`mc26 docs --version V` renders it alone, into `temp/docs/<version>`);
2. the library built from exactly that data, committed on branch `mc-<version>` of `go-mc26`,
   tagged `v0.<YYN>.<patch>` — `patch` counts builds of the same version with a newer generator or
   fixed sources.

Both commits record their inputs: `_meta.json` names the jar checksum and the `mc26` commit that
extracted; `data/version/version.go` (`DataSource`, `Generator`) and the commit message name the
data tag and the `mc26` commit that built. A build is reproducible from those two commits.

After a commit in a data or library repository, `release` rewrites the README of that repository's
`main` from `gen/templates/index-<kind>.md.tmpl`: what the repository is, and the table of its
`mc-*` branches with the protocol, the data version and the latest tag of each, read from the
branches themselves. `main` never holds anything else.

## Locally

```bash
go run ./gen/cmd/mc26 update  --version 26.2            # extract, check, build, verify, then the commits and tags in ../mc26-data and ../go-mc26
go run ./gen/cmd/mc26 release --version 26.2            # the commits and tags alone (extract + build + smoke, no cross-check, no e2e)
go run ./gen/cmd/mc26 release --version 26.2 --push     # and pushes branches and tags
go run ./gen/cmd/mc26 index --repo ../go-mc26 --kind lib  # the README on main alone (release does it after each commit)
```

Steps: extract (unless `--skip-extract` finds `temp/data/26.2`) → stage the data (JSON, README
from a template, LICENSE, `verify.yml`) → checkout `mc-26.2` (created from `main` if new),
replace the tree, commit, tag the next free number → build from that checkout → smoke test
(`--e2e` adds the example bots) → checkout `mc-26.2` in `go-mc26`, replace, commit, tag → push
if asked. Nothing is tagged twice: an unchanged tree keeps its tag and the command says so.

The target checkouts must be clean; the command refuses otherwise.

A pre-release whose data is wanted before the library builds against it (new wire shapes still
to be handled in `gen/src`) is released with `--data-only`: the data branch and tag are made,
the library step is skipped. `26.3-pre-2` was seeded into `mc26-data-pre` that way as
`v0.263.0-pre2.0`.

## In GitHub Actions

`.github/workflows/release.yml`, `workflow_dispatch` with the version and a `push` switch
(default on). It clones the three target repositories over SSH, each with its own deploy key:
a write deploy key on `mc26-data`, `mc26-data-pre` and `go-mc26`, whose private halves are the
secrets `MC26_DATA_DEPLOY_KEY`, `MC26_DATA_PRE_DEPLOY_KEY` and `GO_MC26_DEPLOY_KEY` here; a key
reaches its own repository and no other, and does not expire. Then it runs the local command
with `--e2e --push`. To replace a key: `ssh-keygen -t ed25519 -N "" -f k`, `gh repo deploy-key
add k.pub -R mj41/<repo> --allow-write`, `gh secret set <NAME> -R mj41/mc26 < k`, delete `k`
and the old deploy key. This is the only place tags are created for
published lines; a laptop release with `--push` is for bootstrapping.

`pipeline.yml` never tags and never pushes.

## The kit

`go-mc26-kit` is not released by this repository: it is hand-written source with its own
history, committed and tagged there (`v0.1.<n>`, its own line). What this repository does is
test it: `verify` builds and runs it against every extracted version, so run `verify` before
tagging the kit. When a new library version is published, add its tag to the matrix in the
kit's `.github/workflows/ci.yml` (three lines, maintained by hand) and tag the kit if its
sources changed.

## A fix for an older line

The three newest Minecraft versions receive fixes. Commit the fix in `mc26` (in `gen/src`, or in
`gen/src/_versions/<version>/` when only the old version needs it), then release the old
version again: `release --version 26.1 --skip-extract` reuses the data (its tag stays), builds
with the new generator and produces `v0.261.<patch+1>`.

## Go module consumers

```
go get github.com/mj41/go-mc26@v0.262.0
```

The first fetch after a push makes the Go proxy and checksum database record the tag; from
then on it is immutable. Never move or delete a published tag — publish the next patch instead
(a `retract` directive in a later `go.mod` withdraws a broken one). `@mc-26.3` works for the
bleeding edge as a pseudo-version.
