# A new Minecraft version

Mojang ships 26.3. Everything below runs from the `mc26` checkout; the sibling checkouts
`../mc26-data`, `../mc26-data-pre`, `../go-mc26` and `../go-mc26-examples` are where releases land.

## One command

```bash
go run ./gen/cmd/mc26 update                  # the newest release in Mojang's manifest
go run ./gen/cmd/mc26 update --version 26.3
go run ./gen/cmd/mc26 update --pre            # the newest snapshot or pre-release
```

`update` does the whole chain and stops where a person is needed:

1. **extract** the version (jar and language files from Mojang, the extractors in a JDK 25
   container) into `temp/data/26.3`;
2. **check the schemas**: every packet, component, registry element and shared type has to be
   described in full — a hole means the extractors do not understand a new codec shape, and the
   command stops with the list (see "When the extractors need work"; `--allow-holes` goes on
   with the packet left out of the generated files and the registry field raw);
3. **print the wire diff** against the newest version before this one: `packetdiff` (packets
   added and removed, ids that moved, a token diff for every layout that changed) and `nbtdiff`
   (registries added and removed, and for each element the keys that appeared, disappeared or
   changed type);
4. **build** the library strictly: every field a packet gained or lost surfaces as a compile
   error in the hand-written code (`bot/`, `server/`) and the command stops. Fix `gen/src` for
   the new version; if an older, still-maintained version needs the old shape, put its copy of
   the file under `gen/src/_versions/<old version>/`, checking the packet in both versions
   first (`schemacov -show clientbound/minecraft:login 26.2`). A struct literal that omits a
   field compiles, so only *reads* of a vanished field are reported. Then
   `update --version 26.3 --skip-extract` to go on;
5. **verify** every extracted version — build, smoke, cross-check, e2e — since the sources
   changed for all of them (one table, logs under `temp/verify/`);
6. **commit** the data into `../mc26-data` (branch `mc-26.3`, tag `v0.263.0`) and the library
   into `../go-mc26` (the same branch and tag), locally; it never pushes. `--no-commit` stops
   after verify.

Then the examples (one `main` branch): bump the library version in `go.mod`, build through the
`go.work`, commit. Publishing is a separate, deliberate step:
`mc26 release --version 26.3 --skip-extract --no-smoke --push`, or the release workflow.

## The same, by hand

```bash
go run ./gen/cmd/mcmeta diff 26.2 26.3                # before extracting: registry changes, no Java
go run ./gen/cmd/mc26 extract --version 26.3
go run ./gen/cmd/mc26 report --version 26.3           # the schema check without a build
go run ./gen/cmd/packetdiff 26.2 26.3
go run ./gen/cmd/nbtdiff 26.2 26.3                    # the registry elements' keys
go run ./gen/cmd/schemacov 26.3                       # what is typed and why the rest is not
go run ./gen/cmd/mc26 build --data 26.3               # [--allow-holes]
go run ./gen/cmd/mc26 verify --versions 26.3,26.2,26.1
go run ./gen/cmd/mc26 release --version 26.3 --skip-extract
```

`mcmeta diff` reads misode/mcmeta, which publishes the data-generator reports the day a version
ships: it tells the size of the data change before any extraction, and nothing about packets.
`pipeline --version 26.3 --skip-extract --smoke --e2e` is one version's build and tests without
the cross-check, the form the pull-request workflow runs.

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
