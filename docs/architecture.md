# Architecture

## One sentence

A Minecraft server jar goes in; a Go module that speaks that version's protocol and knows
that version's data comes out; every Minecraft version is a branch, every build a tag.

## The chain

```
 Mojang's manifest ──► server jar ──► data-gen/java (JDK 25 container) ──► JSON ──► mc26-data  (branch mc-<v>, tag v0.<YYN>.<n>)
                                                                                │
                                        gen/src (hand-written)  ──┐             ▼
                                        gen/internal/generate   ──┼─► build ──► go-mc26   (branch mc-<v>, tag v0.<YYN>.<patch>)
                                        gen/hand-crafted, templates┘             │
                                                                                 ▼
                                                                      go-mc26-examples (branch mc-<v>)
```

1. **Extract** (`mc26 extract`). The host downloads the jar and the language files from Mojang;
   a JDK 25 container runs the game's own data generator (`--all` reports) and the extractors of
   `data-gen/java`, which read the unobfuscated jar with reflection and `java.lang.classfile` and
   write the wire schemas of packets and data components. `_meta.json` records the jar's checksum
   and the extractor commit. Nothing in the output is code.
2. **Build** (`mc26 build`). The hand-written packages of `gen/src` are copied, the twelve
   generators write the generated packages next to them, templates render the README and the CI
   workflow, `go mod tidy`, `gofmt`, `go build`, `go vet` and `go test` run inside the result.
   The result is a complete module; it is never edited by hand.
3. **Test** (`mc26 smoke`, `mc26 e2e`). A vanilla server of the same version runs in the same
   JDK container; the library's smoke test and the example bots join it. See [testing.md](testing.md).
4. **Release** (`mc26 release`). The JSON goes onto the `mc-<version>` branch of `mc26-data` and
   gets a tag; the built module goes onto the `mc-<version>` branch of `go-mc26` and gets a tag.
   `--push` publishes; without it everything stays local. See [release.md](release.md).

The same command runs on a laptop and in GitHub Actions; the workflows are thin wrappers.

## Repositories

| repository | content | branches and tags |
|---|---|---|
| `mc26` | this: extractors, generators, hand-written library sources, templates, the `mc26` command, workflows, docs | ordinary `main` |
| `mc26-data` | JSON of every Minecraft release | `main` = README; `mc-<version>`; `v0.<YYN>.<n>` (`n` counts re-extractions) |
| `mc26-data-pre` | the same for snapshots and pre-releases | `mc-26.3-pre-2`; `v0.263.0-pre2.<n>` |
| `go-mc26` | the generated library, module `github.com/mj41/go-mc26` | `main` = README; `mc-<version>`; `v0.<YYN>.<patch>` |
| `go-mc26-examples` | bots and tools on the library | `main` = README; `mc-<version>` pinned to a library tag |

Minecraft version ids are `YY.N` from 26.1 on; there is no 26.0. `YYN` in a tag is the version
without the dot (26.2 → `v0.262.x`). Major version 0 states the API promise: none between
Minecraft versions. The three newest versions receive fixes; older branches are frozen.

## Why the data is a repository of its own

Its history is exactly the sequence of Minecraft versions; an extractor refactor never touches
it. Anything — a Java or Rust generator, a test suite, a script — can consume it by tag or raw
URL without Go or Java. A diff between two branches is the change between two Minecraft
versions, readable without running anything. It also holds the two files nobody else publishes:
`packet_schema.json` (typed wire layout of every packet) and `component_schema.json`.

## What is hand-written, what is generated

See [generated-vs-hand-written.md](generated-vs-hand-written.md) for the numbers. In short:
the network framing, NBT, world files, the bot and the server framework are hand-written and
mostly still the code of Tnze/go-mc (recorded in `COPIED`); packets, registries, items, blocks,
components, entities, biomes and translations are generated. The hand-written code refers only
to generated identifiers, so after a version bump the compiler reports every packet field that
came or went.

## Several Minecraft versions from one source tree

`gen/src` is written for the newest version. When a later version changed a packet, the older
version keeps its own copy of the affected file under `gen/src/_versions/<version>/`, applied on
top at build time (26.1 needs two such files). A file in `_versions` should be the exception;
if a difference is data-shaped, generate it instead.

## What runs where

- Host: Go (the `mc26` command, the build, the tests), git.
- Container `eclipse-temurin:25-jdk`: the extraction, the vanilla test server. Docker or podman.
- GitHub Actions: `pipeline.yml` on pull requests, `release.yml` on demand (the only place tags
  are created).
