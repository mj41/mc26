# A new Minecraft version

Mojang ships 26.3. Everything below runs from the `mc26` checkout; the sibling checkouts
`../mc26-data`, `../mc26-data-pre`, `../go-mc26` and `../go-mc26-kit` are where releases land.

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
4. **build** the library strictly, then the kit against it: every field a packet gained or
   lost surfaces as a compile error in the hand-written code (the kit's `bot/` and `server/`)
   and the command stops. Fix the kit (a commit in `../go-mc26-kit`) so that it builds against
   the new version *and* the older supported ones: the kit has one source for all of them, so a shape that differs
   between versions is written field by field under a check of `version.ProtocolVersion`
   (see the login finished packet in `server/login.go`), never as two copies. Check the packet
   in both versions first (`schemacov -show clientbound/minecraft:login 26.2`). A struct literal
   that omits a field compiles, so only *reads* of a vanished field are reported. Then
   `update --version 26.3 --skip-extract` to go on;
5. **verify** every extracted version — build, smoke, cross-check, e2e — since the sources
   changed for all of them (one table, logs under `temp/verify/`). The e2e run's last
   scenario cuts the version's fixture world for the save tests into
   `gen/src/save/testdata/<version>/`, so the new version's first build skips those tests
   and every later one runs them; a release's world is committed with the sources, a
   pre-release's stays local (see `testing.md`);
6. **commit** the data into `../mc26-data` (branch `mc-26.3`, tag `v0.263.0`) and the library
   into `../go-mc26` (the same branch and tag), locally; it never pushes. `--no-commit` stops
   after verify.

Publishing is a separate, deliberate step: `mc26 release --version 26.3 --skip-extract
--no-smoke --push`, or the release workflow. Then, in the kit's repository, add the new library
tag to the matrix of its `ci.yml`, commit, and tag the kit if the bump changed its sources.

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

The observed rate is one or two rules per release, found by the pipeline rather than by a
reader of the code: 26.3 moved the registry codec classes and renamed a lookup, 26.1 still had
`MutableObject` recursion in a codec, 26.3-pre-3 needed nothing. Every hole so far was closed
by a rule read from the bytecode, never by a hand table; the schema check in every build keeps
it that way.

- A registry or report renamed: `GenBiomes`/`GenItems` resolve lookups reflectively; follow the
  pattern.
- A new codec shape in packets: extend `invoke` (codec chains) or `readerInvoke` (buffer reads)
  in `GenPacketSchema.java`; `schemacov -show` on the affected packet shows the tree you get.
- A new data component shape: the same interpreter (`DataComponents.<clinit>`); a component the
  schema cannot type is listed in `level/component/skipped_gen.go` until it can.
- A new DataFixerUpper combinator in a registry codec (`invoke` in `GenNbtSchema.java`) or a
  registry codec class that moved (they are matched by short class name, as
  `net/minecraft/resources` became `net/minecraft/core/registries/codec` in 26.3).

## When the robot misbehaves

The kit's robot is written from what the vanilla client does, and every file of it names the
vanilla class and method it follows. When a new version breaks it, read those classes of both
versions as source and compare them:

```bash
go run ./gen/cmd/source 26.2 LocalPlayer      # temp/source/26.2/net/minecraft/client/player/LocalPlayer.java
go run ./gen/cmd/source 26.3 LocalPlayer
diff -u temp/source/26.2/net/minecraft/client/player/LocalPlayer.java \
        temp/source/26.3/net/minecraft/client/player/LocalPlayer.java
```

`source` decompiles the client jar (it holds the shared `net.minecraft.world` classes and the
integrated server too; `-server` reads the dedicated server's jar) with Vineflower in the JDK
container, a class with its nested classes, the rest of the jar on the library path; `-l` lists
the names a suffix matches, `-all` decompiles everything. The output stays under `temp/`: it is
for reading, never committed or copied.
