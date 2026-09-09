# Architecture

## One sentence

A Minecraft server jar goes in; a Go module that speaks that version's protocol and knows
that version's data comes out; every Minecraft version is a branch, every build a tag.

## The chain

```
 Mojang's manifest ──► server jar ──► data-gen/java (JDK 25 container) ──► JSON ──► mc26-data  (branch mc-<v>, tag v0.<YYN>.<n>)
                                                                                │
                                        gen/src (hand-written)  ──┐             ▼
                                        gen/internal/generate   ──┼─► build ──► go-mc26     (branch mc-<v>, tag v0.<YYN>.<patch>)
                                        gen/hand-crafted, templates┘             │
                                                                                 ▼
                                        ../go-mc26-kit (its own repository) ──► tested against every build (its bot is the test client)
```

1. **Extract** (`mc26 extract`). The host downloads the jar and the language files from Mojang;
   a JDK 25 container runs the game's own data generator (`--all` reports) and the extractors of
   `data-gen/java`, which read the unobfuscated jar with reflection and `java.lang.classfile` and
   write the wire schemas of packets and data components. `_meta.json` records the jar's checksum
   and the extractor commit. Nothing in the output is code.
2. **Build** (`mc26 build`). The hand-written packages of `gen/src` are copied, the twelve
   generators write the generated packages next to them, templates render the README and the CI
   workflow, `go mod tidy`, `gofmt`, `go build`, `go vet` and `go test` run inside the result.
   The result is a complete module; it is never edited by hand. Then the kit (a checkout of
   `go-mc26-kit` next to this repository, `--kit-src` to point elsewhere: the bot, the server
   framework, the account flows, the examples) is assembled against that result under
   `temp/kit/<version>` through a workspace, and built, vetted and tested there.
3. **Test** (`mc26 smoke`, `mc26 e2e`). A vanilla server of the same version runs in the same
   JDK container; the kit's smoke test, the library's management test and the example bots join
   it. See [testing.md](testing.md).
4. **Release** (`mc26 release`). The JSON goes onto the `mc-<version>` branch of `mc26-data` and
   gets a tag; the built module goes onto the `mc-<version>` branch of `go-mc26` and gets a tag.
   `--push` publishes; without it everything stays local. See [release.md](release.md). The kit
   is not released by this repository: it has its own history, commits and tags.

The same command runs on a laptop and in GitHub Actions; the workflows are thin wrappers.

## Repositories

| repository | content | branches and tags |
|---|---|---|
| `mc26` | this: extractors, generators, the hand-written library sources (`gen/src`), templates, the `mc26` command, workflows, docs | ordinary `main` |
| `mc26-data` | JSON of every Minecraft release | `main` = README; `mc-<version>`; `v0.<YYN>.<n>` (`n` counts re-extractions) |
| `mc26-data-pre` | the same for snapshots and pre-releases | `mc-26.3-pre-2`; `v0.263.0-pre2.<n>` |
| `go-mc26` | the generated library, module `github.com/mj41/go-mc26` | `main` = README; `mc-<version>`; `v0.<YYN>.<patch>` |
| `go-mc26-kit` | the bot, the server framework, the account flows and the examples, module `github.com/mj41/go-mc26-kit`; hand-written source with its own history, which mc26 tests against but does not own | one branch, `main`; tags of its own (`v0.1.<n>`), not Minecraft versions; its `go.mod` requires the oldest supported library tag and its workflow builds it against every supported one |

Minecraft version ids are `YY.N` from 26.1 on; there is no 26.0 (the manifest goes from
1.21.11 to the 26.1 snapshots). `YYN` in a tag is the version without the dot (26.2 →
`v0.262.x`), because a Go module cannot carry the Minecraft version as its semver major. Major
version 0 states the API promise: none between Minecraft versions, and none with Tnze/go-mc,
whose code is carried as attribution (`LICENSE`, `COPIED`), not as a sync mechanism. The three
newest versions receive fixes; older branches are frozen with their tags.

## Why one repository for the extractors, the generators and the sources

Java extractors, Go generators, the library sources and the kit sources are one repository,
not an umbrella of submodules or a Java repository and a Go repository. For one or two
maintainers a cross-language change is one commit, and the tested set *is* the commit. The
umbrella shape (a repository of submodule pins with the pipeline and release in one job) earns
its keep only with separate contributors per component.

## Why the kit is a repository of its own, and why this one still tests it

The library's promise is "the protocol and the data as Go types"; the bot and the server are
opinions on top and change for reasons that have nothing to do with a Minecraft version, so
they are versioned on their own, in a repository of their own, with their own history: they
are hand-written source, not a build product, and a change to the bot is a pull request there,
not here. The cut follows the dependency graph and is the only one that is free: `server`
imports `bot`, both import the account packages, and the library's `level` needs `save` and
its `chat/sign` needs `yggdrasil/user`, which is why those two stay in the library. Splitting
the kit further (the server on its own, say) is a mechanical step if a consumer ever wants one
part without the others; merging modules back would not be.

This repository consumes the kit without owning it, because the kit's bot is the pipeline's
test client: the smoke test joins a vanilla server with it, the recorded session of
`crosscheck` is a session it plays, and `e2e` runs its examples. So every build here assembles
the kit checkout against the library it produced and runs those tests; a Minecraft version
that changes what the bot reads fails `verify` here, and the fix is a commit in the kit. Should
that coupling ever hurt, the alternative is a minimal test client inside this repository
(login, configuration, keepalive, chunks), at the price of duplicating that much of `bot`.

One build of the library is one Minecraft version: packet ids, registries and packet layouts
are package-level constants and generated types. A program that must speak two versions during
a migration window builds the kit twice, against two library tags, which is two builds of the
same source. A binary that speaks several versions at once is not a goal.

## Why the data is a repository of its own

Its history is exactly the sequence of Minecraft versions; an extractor refactor never touches
it. Anything — a Java or Rust generator, a test suite, a script — can consume it by tag or raw
URL without Go or Java. A diff between two branches is the change between two Minecraft
versions, readable without running anything. It also holds the files nobody else publishes:
`packet_schema.json` (the typed wire layout of every packet, shared structure and data
component), `nbt_schema.json` (the registry elements and chat structures as NBT) and
`entity_data.json` (the entity metadata layout).

## What is hand-written, what is generated

See [generated-vs-hand-written.md](generated-vs-hand-written.md) for the numbers. In short:
the network framing, NBT and the world files (in the library) and the bot and the server
framework (in the kit) are hand-written and mostly still the code of Tnze/go-mc (recorded in
each module's `COPIED`); packets, registries, items, blocks, components, entities, biomes and
translations are generated. The hand-written code refers only to generated identifiers, so
after a version bump the compiler reports every packet field that came or went.
[hand-written.md](hand-written.md) sorts it into tiers.

## Several Minecraft versions from one source tree

`gen/src` is written for the newest version. When a later version changes a packet an older
version's library needs differently, the older version keeps its own copy of the affected file
under `gen/src/_versions/<version>/`, applied on top at build time. No version needs one at
present; a file in `_versions` should be the exception, and if a difference is data-shaped,
generate it instead.

The kit has no overlays at all: one source builds against every supported library version.
Where a version differs in something the kit does (the login finished packet gained a session
id in 26.2), the kit follows `version.ProtocolVersion` of the library it is built with and
writes that packet field by field, so the generated struct's shape does not matter to it.

## What runs where

- Host: Go (the `mc26` command, the build, the tests), git.
- Container `eclipse-temurin:25-jdk`: the extraction, the vanilla test server. Docker or podman.
- GitHub Actions: `pipeline.yml` on pull requests, `release.yml` on demand (the only place tags
  are created).
