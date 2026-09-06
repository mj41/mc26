# Generated versus hand-written

`go run ./gen/cmd/mc26 report --version 26.2` measures a built library; this page explains the
categories and keeps a snapshot. Re-run the command rather than trusting the numbers here.

## Snapshot: go-mc26 for Minecraft 26.2 (2026-09-06)

| | files | lines |
|---|---:|---:|
| **generated** | 384 | 1,270,178 |
| translations (`data/lang`, 143 languages) | 143 | 1,224,573 |
| generated without translations | 241 | 45,605 |
| **hand-written** | 151 | 20,118 |
| byte-identical to Tnze/go-mc | 88 | 11,200 |
| modified since | 44 | 6,143 |
| new in this project | 19 | 2,775 |

Generated share of lines: 98 % overall, 69 % without the translations.

| generator | writes | files | lines |
|---|---|---:|---:|
| item | `data/item` | 1 | 10,781 |
| blocks | `level/block` (+ `block_states.nbt`) | 2 | 7,774 |
| registryid | `data/registryid`, one file per registry | 95 | 7,549 |
| packets | `protocol/{status,login,configuration,play}` structs and round-trip tests, `protocol/types` enums and shared structs, the `wire` records | 23 | 8,036 |
| component types | `level/component/*_gen.go` | 105 | 2,226 |
| soundid | `data/soundid` | 1 | 1,985 |
| entity | `data/entity` | 1 | 1,604 |
| entity data | `protocol/types/entitydata_gen.go`, `data/entitydata/entitydata_gen.go` | 2 | 3,234 |
| nbt | `registry/elements_gen.go`, `registry/registries_gen.go` (+ decode test), `chat/style_gen.go` | 4 | 714 |
| blockentities | `level/block` | 2 | 617 |
| packetid | `data/packetid` (ids and `String()`) | 1 | 537 |
| component | `level/component/components.go` | 1 | 238 |
| biome | `level/biome` | 1 | 120 |
| constants | `data/constants` | 1 | 163 |
| version | `data/version` | 1 | 27 |

| package | generated lines | hand-written lines | what is hand-written |
|---|---:|---:|---|
| `nbt` | 0 | 4,960 | NBT codec, SNBT, dynamic values — upstream |
| `net` | 0 | 2,779 | connection, packet framing and field types, RCON — upstream (one test rewritten) |
| `bot` | 0 | 2,676 | login, configuration, the event model, chunks, chat, tab list, inventories — modified for 26.x and the generated packets |
| `level` | 11,564 | 2,045 | palette and section codecs, block state helpers, 8 component types the schema cannot describe and three bridges |
| `server` | 0 | 2,193 | list ping, login, configuration, command graph, player list — modified |
| `chat` | 101 | 1,308 | text components, translations, signed chat, the wire form of the chat type — modified |
| `save` | 0 | 1,166 | level.dat, player data, region files — upstream, keys followed 26.1 |
| `microsoft` | 0 | 992 | Microsoft login — new |
| `yggdrasil` | 0 | 654 | Mojang session server — upstream (one key parser added) |
| `registry` | 613 | 658 | the registry container and lookups, the NBT bridge types (`Holder`, `HolderSet`, `Color`) with their tests |
| `wire` | 133 | 441 | the wire generics (`List`, `Map`, `Holder`, `Either`, `EnumSet`, …), packed positions, NBT bridges — new |
| `protocol` | 7,431 | 199 | the handshake, the type aliases |
| `data` | 1,250,336 | 17 | one hand-written helper (`data/lang/en-us`) |
| `offline` | 0 | 30 | offline uuids — upstream |

The machinery that produces the library: 8,796 lines of Go in `gen/` (commands, generators,
build, test harness), 3,422 lines of Java in `data-gen/java`, four hand-crafted input files
(hand components 8, hand packets 0, naming 12, packet phases 4), and one overlay of one file
for 26.1 (`server/login.go`).

The registry step (2026-09-06) added typed elements for all 29 synchronized registries where 3
were typed before; it removed about 150 hand-written lines of structs and added about 270 of
bridge types and tests, so the hand-written total moved little while the typed surface grew.
The packet step the same day retired the last hand-written packets (285 lines) and the chunk's
own packet codec (about 120 lines); 204 of the 232 packets of 26.2 are generated. The wire
step then moved the shared records (`Vec3`, `GlobalPos`, `GameProfile`, `BlockHitResult`) from
hand-written `wire` code to generated ones, leaving `wire` with the generics, the packed
positions and the NBT bridges. The entity data step added a capability rather than removing
code: the metadata layout of every entity type and the particle options, both generated. The
bot step then made the bot keep the generated packets as its state (`basic.Player.Login`,
`Player.Spawn`, `types.ItemStack` slots) instead of copying their fields, which also retired
the 26.1 overlay of `bot/basic/info.go`. The constants step replaced the hand-written menu
table with the registry and named the inventory slot layout from Mojang's constants. Two further
rounds typed what the walker had given up on: dispatches on a registry whose elements carry
their own codec (particles, recipe and slot displays, number formats) and fields a reader only
reads under a condition on earlier fields. 242 of the 255 packets of 26.2 are generated; what
is left is four registry dispatches without a bootstrap rule, four readers with loops, an
either and two odd readers, each named in the header of the file that would have held it.

## What the categories mean

- **generated** — the first line says `Code generated by gen/<generator> … DO NOT EDIT`. Never
  edited; a change goes into the generator or its inputs and shows up on the next build.
- **hand-written** — copied from `gen/src` at build time. `COPIED` in the library (and in
  `gen/src`) lists every such file with its origin:
  - *upstream*: byte-identical to Tnze/go-mc at the commit the sources were taken from (import
    paths rewritten). MIT, Copyright (c) 2019 Tnze.
  - *modified*: derived from an upstream file and changed here — for 26.x, for the generated
    packet structs, or to fix something.
  - *new*: written for this project.
  When you edit an *upstream* file, change its `COPIED` line to *modified*; the report shows a
  file that is in the tree but not in `COPIED` as *unlisted*.
- **overlay** — `gen/src/_versions/<version>/`: a hand-written file that an older Minecraft
  version needs different. Counted as hand-written in that version's build.

## What still could be generated

The remaining hand-written code that is *data-shaped* rather than *logic-shaped*, in the order
of payoff:

1. ~~the 58 component schema overrides and the 17 hand-written component types~~ — done
   2026-09-05: 103 of 111 components are generated from their codec chains; 8 with a dispatch
   or a recursive codec stay by hand (`hand_components.json`), with three bridge types;
2. ~~the NBT field names of the registry codecs and the chat decoration~~ — done 2026-09-06,
   and guarded by the smoke test, which fails when a live server sends a registry key the
   generated element types do not have:
   the registries sent in the configuration phase (`registry/elements_gen.go`, the `Registries`
   struct) and the chat style, click and hover events and decoration (`chat/style_gen.go`) are
   generated from the DataFixerUpper codecs (`nbt_schema.json`); what the walker cannot type (an
   `IntProvider`, a dispatch on a registry) stays a raw field, listed in the file header;
3. the save formats (`level.dat`, chunks, player data in `save/`) — their shapes are read with
   keyed accessors rather than codecs; `nbt_schema.json` already carries the codec-built parts
   (`LevelData$RespawnData`, `WorldDataConfiguration`, `WorldOptions`);
4. ~~entity metadata serializers~~ — done 2026-09-06, and guarded by a smoke test that summons
   entities and compares what the server sends with the generated table: `GenEntityData` + `data/entitydata` and
   `types.EntityData`; `set_entity_data` and `level_particles` are generated, the particle
   options as a union over the particle type registry (two particle types with a further
   dispatch inside are rejected when decoded);
5. ~~the hand-written packets in `protocol/*/hand.go`~~ — done 2026-09-06: the walker types
   rest-of-packet payloads, length-prefixed buffers and entries guarded by an action bit set,
   and treats the signed-chat types and the block entity as leaves; `hand.go` is gone. The
   packets still skipped are dispatches on a registry (particles, recipes, debug values) and a
   few readers with loops (`set_equipment`, `commands`, the advancements).

The logic — NBT, framing, world files, the bot's event model, the server framework — stays
hand-written by design.
