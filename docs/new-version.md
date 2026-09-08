# A new Minecraft version

Mojang ships 26.3. Everything below runs from the `mc26` checkout; the sibling checkouts
`../mc26-data`, `../mc26-data-pre`, `../go-mc26` and `../go-mc26-examples` are where releases land.

## 1. Look before extracting (no Java)

```bash
go run ./gen/cmd/mcmeta diff 26.2 26.3
```

misode/mcmeta publishes the data-generator reports the day a version ships. The diff lists
registry changes (new blocks, items, entities, sound events, component types), block-state
property changes and per-item default-component changes. It tells you the size of the data
change; it says nothing about packets.

## 2. Extract

```bash
go run ./gen/cmd/mc26 extract --version 26.3
```

Writes `temp/data/26.3`. The jar and the language files come from Mojang; the container does
the rest. Watch for the extractors failing to compile — the game's API moved (`GenBiomes` and
`GenItems` had to follow a renamed lookup method between 26.2 and 26.3); fix the Java in
`data-gen/java`, re-run.

## 3. Read the wire changes

```bash
go run ./gen/cmd/packetdiff 26.2 26.3
go run ./gen/cmd/schemacov 26.3
```

`packetdiff` prints packets added and removed, ids that moved, and a token diff for every packet
whose layout changed — read the tokens, they name the primitive reads. `schemacov` says how many
packets are fully typed and why the rest are not; a new `opaque` or `dispatch` hole usually
means a new codec combinator the extractor does not understand yet (`GenPacketSchema.java`);
the packet is left out of the generated files, with the reason in their header, until the
extractor types it. The registry
elements have the same kind of report in the header of the generated
`registry/elements_gen.go`: the fields kept as raw NBT, and why (`GenNbtSchema.java`, run alone
with `mc26 extract --version 26.3 --only GenNbtSchema` while extending it).

## 4. Build and let the compiler talk

```bash
go run ./gen/cmd/mc26 build --data 26.3
```

Every field a packet gained or lost surfaces as a compile error in the hand-written code
(`bot/`, `server/`). Fix `gen/src` for the new version. If an older,
still-maintained version needs the old shape, put its copy of the file under
`gen/src/_versions/<old version>/` — and check the packet in both versions first:

```bash
go run ./gen/cmd/schemacov -show clientbound/minecraft:login 26.2
```

A struct literal that omits a field compiles, so only *reads* of a vanished field are reported.

## 5. Test against the real thing

```bash
go run ./gen/cmd/mc26 smoke --version 26.3
go run ./gen/cmd/mc26 e2e   --version 26.3      # ../go-mc26-examples must be on mc-26.3
```

Or all of it at once: `go run ./gen/cmd/mc26 pipeline --version 26.3 --skip-extract --smoke --e2e`.

## 6. Release

```bash
go run ./gen/cmd/mc26 release --version 26.3            # local branches and tags
go run ./gen/cmd/mc26 release --version 26.3 --push     # or run the release workflow
```

Then the examples (one `main` branch): bump the library version in `go.mod`, build through
the `go.work`, commit. (`mc26 import-examples` was only for the first import.)

## Pre-releases and snapshots

Any id in Mojang's manifest works: `mc26 extract --version 26.4-pre-1`. `release` routes ids
with a suffix to `mc26-data-pre` and tags them `v0.264.0-pre1.<n>`. The library can be built from
pre-release data for early adaptation; it is not part of the normal release chain.

## When the extractors need work

- A registry or report renamed: `GenBiomes`/`GenItems` resolve lookups reflectively; follow the
  pattern.
- A new codec shape in packets: extend `invoke` (codec chains) or `readerInvoke` (buffer reads)
  in `GenPacketSchema.java`; `schemacov -show` on the affected packet shows the tree you get.
- A new data component shape: the same interpreter (`DataComponents.<clinit>`); a component the
  schema cannot type is listed in `level/component/skipped_gen.go` until it can.
- A new DataFixerUpper combinator in a registry codec (`invoke` in `GenNbtSchema.java`) or a
  registry codec class that moved (they are matched by short class name, as
  `net/minecraft/resources` became `net/minecraft/core/registries/codec` in 26.3).
