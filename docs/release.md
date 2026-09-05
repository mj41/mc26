# Releasing

## What a release is

For one Minecraft version:

1. the extracted JSON, committed on branch `mc-<version>` of `mc26-data` (or `mc26-data-pre` for
   an id with a suffix), tagged `v0.<YYN>.<n>` — `n` counts extractions of the same version with a
   newer extractor;
2. the library built from exactly that data, committed on branch `mc-<version>` of `go-mc26`,
   tagged `v0.<YYN>.<patch>` — `patch` counts builds of the same version with a newer generator or
   fixed sources.

Both commits record their inputs: `_meta.json` names the jar checksum and the `mc26` commit that
extracted; `data/version/version.go` (`DataSource`, `Generator`) and the commit message name the
data tag and the `mc26` commit that built. A build is reproducible from those two commits.

## Locally

```bash
go run ./gen/cmd/mc26 release --version 26.2            # commits and tags in ../mc26-data and ../go-mc26
go run ./gen/cmd/mc26 release --version 26.2 --push     # and pushes branches and tags
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
(default on). It clones the three target repositories with the secret `MC26_PUSH_TOKEN` — a
fine-grained token with *contents: write* on `mc26-data`, `mc26-data-pre` and `go-mc26` — and
runs the local command with `--e2e --push`. This is the only place tags are created for
published lines; a laptop release with `--push` is for bootstrapping.

`pipeline.yml` never tags and never pushes.

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
