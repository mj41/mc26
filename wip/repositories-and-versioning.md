# The repositories, the versions, the workflows

Status: **done** — decided 2026-09-05, built the same day; kept as the record of why. What is
current lives in `docs/architecture.md`, `docs/release.md` and `README.md`.

## The repositories

```
 Mojang jar ──► mc26 (this repo) ──┬─► mc26-data      (releases)       ──► go-mc26 ──► go-mc26-examples
                data-gen/ Java    │   (JSON, branch per MC version)     (generated    (examples and
                gen/      Go      └─► mc26-data-pre  (snapshots and      library)      bots, one main)
                workflows              pre-releases, same scheme)
```

- **`mc26`** — the project: the Java extractors (`data-gen/java`, run in a JDK 25 container),
  the Go generators, templates and framework sources (`gen/`), the `mc26` command and the
  workflows. A monorepo rather than an umbrella of submodules or a Java repo and a Go repo: for
  one or two maintainers a cross-language change is one commit, and the tested set *is* the
  commit. (The umbrella shape — a repo of submodule pins, pipeline and release in one job —
  earns its keep only with separate contributors per component.)
- **`mc26-data`** — extracted JSON of release versions, nothing else: `main` is a README, one
  branch per Minecraft release, tags `v0.<YY><N>.<n>` where `n` counts re-extractions with a
  newer extractor. No code, no module: consumable by tag or raw URL from any language; a
  version's diff *is* the Minecraft change. `_meta.json` records the version, protocol, data
  version, jar hash and extractor commit.
- **`mc26-data-pre`** — the same for snapshots and pre-releases (`v0.263.0-pre3.0`), so the
  weekly churn stays out of the release repo's history.
- **`go-mc26`** — the library, module `github.com/mj41/go-mc26`: generated, templated or copied
  only; every commit is a `build` result reproducible from a generator commit and a data
  commit, both in `version.go` and the commit message. Consumers import it as a plain
  dependency, no `replace`.
- **`go-mc26-examples`** — examples and bots on the library, one `main` branch pinned to the
  newest tag (the examples were byte-identical across versions); the `e2e` step builds them.

All five stay free of private names: no private repositories, hosts or infrastructure of the
owner's deployments, also not in branch names and commit messages.

## No backward compatibility (2026-09-05)

`go-mc26` does not promise API compatibility with Tnze/go-mc, and nothing before Minecraft 26.1
is supported: package layout and type names may change whenever generation makes a better shape
possible; the upstream copies are attribution (`LICENSE`, the `COPIED` manifest), not a sync
mechanism; the last three Minecraft versions receive fixes, older branches freeze with their
tags. There is no Minecraft 26.0 (the manifest goes from 1.21.11 to the 26.1 snapshots), so
`mc-26.1` / `v0.261.0` is the first line everywhere.

## Versions

- Go modules cannot carry the Minecraft version as the semver major, so tags are
  `v0.<YY><N>.<patch>`: `v0.262.0` is 26.2, `v0.262.1` a fix on it, `v0.263.0-pre3.0` a
  pre-release's data. Major 0 also states the API promise honestly.
- Branch model: in `go-mc26` and the data repos, `main` carries only a README listing the
  branches; one branch per Minecraft version holds that version's tree; tags pin releases.
  `go get github.com/mj41/go-mc26@v0.262.0`; `@mc-26.3` for the bleeding edge.
- Older versions share `gen/src` (written for the newest version); a file an older version
  needs different lives under `gen/src/_versions/<version>/` and replaces it at build time
  (26.1: one file). Check the packet in both versions with `schemacov -show` before writing an
  overlay: a struct literal that omits a field compiles.

## Publishing a Go module

There is no registry: push the repository whose `go.mod` names the module path, tag semver
versions, and the first `go get` makes `proxy.golang.org` fetch the tag and `sum.golang.org`
record it — from then on the version is immutable; a wrong one is withdrawn with a `retract`
line in a later `go.mod`, never by moving the tag. `LICENSE` must be present for pkg.go.dev.
For local work against an unpublished tree use a `go.work`, not a `replace` in a committed
`go.mod`.

## Workflows

`mc26/.github/workflows/pipeline.yml` (pull requests and `workflow_dispatch(version)`: extract,
build, unit tests, smoke, e2e, artifacts) and `release.yml` (`workflow_dispatch(version, push)`:
clones the target repositories with `MC26_PUSH_TOKEN`, runs `mc26 release --version V --push`).
One job each, no dispatch chain between repositories; the generated repositories carry only a
`ci.yml` (library: build, vet, test) and a `verify.yml` (data: JSON parses, `_meta.json`
present), written by `build`/`release` from `gen/templates`. A tag is created only by the
release workflow or by the owner's explicit `release --push`, never by hand. The same commands
run locally without pushing (`docs/testing.md`, `docs/release.md`).

## Cross-version evidence (2026-09-05)

Extraction with the pipeline: 26.1, 26.2 and 26.3-pre-2 after one fix (26.3 renamed
`VanillaRegistries.createLookup()` to `createWorldLookup()`; both names resolve now, and
`ExtractAll` fails the run on an extractor error instead of warning). `packetdiff`: 26.1 → 26.2
changed 3 layouts (login `+BOOL`, login_finished `+UUID`, team parameters); 26.2 → 26.3-pre-2
changed about 40 (teleport with position and rotation, `VecDelta` entity moves, chat session
public key, the chunk packet, spawn info); 26.3-pre-2 → 26.3-pre-3 none. The data changes per
release are large and were fully generated already; the wire changes were the manual work,
which the packet generator ended.
