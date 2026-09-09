# Generated versus hand-written

`go run ./gen/cmd/mc26 report --version 26.2` measures a built library; this page explains the
categories and keeps a snapshot. Re-run the command rather than trusting the numbers here.

## Snapshot: go-mc26 for Minecraft 26.2 (2026-09-08)

| | files | lines |
|---|---:|---:|
| **generated** | 392 | 1,273,104 |
| translations (`data/lang`, 143 languages) | 143 | 1,224,573 |
| generated without translations | 249 | 48,531 |
| **hand-written** | 150 | 20,940 |
| byte-identical to Tnze/go-mc | 86 | 10,864 |
| modified since | 45 | 6,441 |
| new in this project | 19 | 3,635 |

Generated share of lines: 98 % overall, 70 % without the translations.

| generator | writes | files | lines |
|---|---|---:|---:|
| item | `data/item` | 1 | 10,781 |
| packets | `protocol/{status,login,configuration,play}` structs and round-trip tests, `protocol/types` enums and shared structs, the `wire` records | 23 | 10,744 |
| blocks | `level/block` (+ `block_states.nbt`) | 2 | 7,774 |
| registryid | `data/registryid`, one file per registry | 95 | 7,549 |
| entity data | `protocol/types/entitydata_gen.go`, `data/entitydata/entitydata_gen.go` | 2 | 3,235 |
| component types | `level/component/*_gen.go` | 113 | 2,443 |
| soundid | `data/soundid` | 1 | 1,985 |
| entity | `data/entity` | 1 | 1,604 |
| nbt | `registry/elements_gen.go`, `registry/registries_gen.go` (+ decode test), `chat/style_gen.go` | 4 | 714 |
| blockentities | `level/block` | 2 | 617 |
| packetid | `data/packetid` (ids and `String()`) | 1 | 537 |
| component | `level/component/components.go` | 1 | 238 |
| constants | `data/constants` | 1 | 163 |
| biome | `level/biome` | 1 | 120 |
| version | `data/version` | 1 | 27 |
| rpc | `management/{types,methods}_gen.go` — the server management API (JSON-RPC over a WebSocket): its types, 68 typed calls, 21 typed notifications | 2 | — |

| package | generated lines | hand-written lines | what is hand-written |
|---|---:|---:|---|
| `nbt` | 0 | 4,960 | NBT codec, SNBT, dynamic values — upstream |
| `bot` | 0 | 3,665 | login, configuration, the event model, chunks, chat, tab list, inventories — modified for 26.x and the generated packets; and the smoke tests (traffic, entity data, equipment, commands, the capture check) that the test harness runs against a vanilla server |
| `net` | 0 | 2,796 | connection, packet framing and field types, RCON — upstream (one test rewritten) |
| `server` | 0 | 2,193 | list ping, login, configuration, command graph, player list — modified |
| `level` | 12,132 | 1,708 | palette and section codecs, block state helpers, the item stack bridges |
| `chat` | 101 | 1,321 | text components, translations, signed chat, the wire form of the chat type — modified |
| `save` | 0 | 1,166 | level.dat, player data, region files — upstream, keys followed 26.1 |
| `microsoft` | 0 | 992 | Microsoft login — new |
| `registry` | 613 | 658 | the registry container and lookups, the NBT bridge types (`Holder`, `HolderSet`, `Color`) with their tests |
| `yggdrasil` | 0 | 654 | Mojang session server — upstream (one key parser added) |
| `wire` | 133 | 560 | the wire generics (`List`, `Map`, `Holder`, `Either`, `EnumSet`, `LenPrefixed`, `Counted`, …), packed positions, NBT bridges — new |
| `protocol` | 9,789 | 220 | the type aliases (the handshake packet is generated since 2026-09-08) |
| `data` | 1,250,336 | 17 | one hand-written helper (`data/lang/en-us`) |
| `offline` | 0 | 30 | offline uuids — upstream |
| `management` | — | — | the WebSocket and JSON-RPC transport under the generated management API, and its smoke test — new (2026-09-08, after this snapshot) |

The machinery that produces the library: 10,458 lines of Go in `gen/` (commands, generators,
build, test harness, the recording proxy), 4,926 lines of Java in `data-gen/java`, the
two hand-crafted files that make the JSON a description rather than a hint (`prims.json`, 50
primitives, and `nodes.json`, 33 node kinds and the frame; the naming overrides, hand lists and
phase file of earlier snapshots are gone), one overlay of one file for 26.1 (`server/login.go`),
and `gen/crosslang/decode.py`, the decoder in another language that checks all of it.

The registry step (2026-09-06) added typed elements for all 29 synchronized registries where 3
were typed before; it removed about 150 hand-written lines of structs and added about 270 of
bridge types and tests, so the hand-written total moved little while the typed surface grew.
The packet step the same day retired the last hand-written packets (285 lines) and the chunk's
own packet codec (about 120 lines). The wire
step then moved the shared records (`Vec3`, `GlobalPos`, `GameProfile`, `BlockHitResult`) from
hand-written `wire` code to generated ones, leaving `wire` with the generics, the packed
positions and the NBT bridges. The entity data step added a capability rather than removing
code: the metadata layout of every entity type and the particle options, both generated. The
bot step then made the bot keep the generated packets as its state (`basic.Player.Login`,
`Player.Spawn`, `types.ItemStack` slots) instead of copying their fields, which also retired
the 26.1 overlay of `bot/basic/info.go`. The constants step replaced the hand-written menu
table with the registry and named the inventory slot layout from Mojang's constants. Further
rounds typed what the walker had given up on: dispatches on a registry whose elements carry
their own codec (particles, recipe and slot displays, number formats, the stat types), fields a
reader only reads under a condition on earlier fields, integers that carry several values in
their bits, enums that do not travel as their ordinal. On 2026-09-07 and 08 the last eight
hand-written component types went (all 122 components of 26.3-pre-2 are generated from their
codec chains), the chunk sections were described down to the palettes and packed longs, and
the cross-language check became part of the pipeline. Every packet and component of 26.1, 26.2
and 26.3-pre-2 is generated, and no packet or component of the three is partial: the last,
`player_chat`'s `FilterMask` in 26.1 and 26.2, is read as the dispatch it is since the walker
follows a switch on an enum.

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
   2026-09-05 for 103 of 111, and 2026-09-07 for the rest: every component is generated from its
   codec chain (`hand_components.json` is gone), with the item stack bridges kept by hand;
2. ~~the NBT field names of the registry codecs and the chat decoration~~ — done 2026-09-06,
   and guarded by the smoke test, which fails when a live server sends a registry key the
   generated element types do not have:
   the registries sent in the configuration phase (`registry/elements_gen.go`, the `Registries`
   struct) and the chat style, click and hover events and decoration (`chat/style_gen.go`) are
   generated from the DataFixerUpper codecs (`nbt_schema.json`); since 2026-09-08 a dispatch on
   a registry gets its cases from the registry's bootstrap class (dialogs, int providers), a
   `forEach` over a static map registers what the map holds (the dialog actions), an inherited
   static factory is read from the class that declares it, a helper that returns a group of
   fields is read like a codec factory, a codec that contains itself through an `either` is a
   named `recursive` node, and `StateHolder.codec` is the id-and-properties struct it
   dispatches to; every registry of the three versions is described (no `opaque`, no caseless
   dispatch), and on 2026-09-09 the last node kinds got Go types too: an `either` is
   `registry.Either[L, R]` (the first side that decodes without an unknown key), an xor of
   keyed fields (`VerticalAnchor`) a struct with each key optional, a `ref` a pointer to the
   enclosing type it names (by name and kind), a `recursive` codec its body's type or a
   wrapper with the body embedded (`BlockStateProviderHolder`), a registry whose element is a
   list (block transformers) a slice, and a Java record whose uses give a field different
   types (`Weighted<T>`, `ModelAndTexture<T>`) a generic Go struct with that field as its type
   parameter; the raw fields left are the maps whose value type
   depends on the key (game rules, environment attributes, enchantment effect components),
   the custom payloads, and two union fields whose type differs between cases;
3. the save formats (`level.dat`, chunks, player data in `save/`) — their shapes are read with
   keyed accessors rather than codecs (`LevelSettings` has no codec at all: `parse(Dynamic)`);
   `nbt_schema.json` carries the codec-built parts in full since 2026-09-08
   (`LevelData$RespawnData`, `WorldDataConfiguration`, `DataPackConfig`, `WorldOptions`,
   `WorldDimensions` down to the density functions, surface rules and material rules);
4. ~~entity metadata serializers~~ — done 2026-09-06, and guarded by a smoke test that summons
   entities and compares what the server sends with the generated table: `GenEntityData` + `data/entitydata` and
   `types.EntityData`; `set_entity_data` and `level_particles` are generated, the particle
   options as a union over the particle type registry (two particle types with a further
   dispatch inside are rejected when decoded);
5. ~~the hand-written packets in `protocol/*/hand.go`~~ — done 2026-09-06: the walker types
   rest-of-packet payloads, length-prefixed buffers and entries guarded by an action bit set,
   and treats the signed-chat types and the block entity as leaves; `hand.go` is gone. The
   packets still skipped are dispatches on a registry (particles, recipes, debug values) and a
   few readers with loops (`set_equipment`, `commands`, the advancements) — all of which have
   since been typed: no packet is skipped in 26.1, 26.2 or 26.3-pre-2;
6. ~~the chunk section codec~~ — done 2026-09-08: its wire form is described in `prims.json`
   (`CHUNK_SECTIONS`, `PALETTED_BLOCK_STATES`, `PALETTED_BIOMES`, with the `rest` and `packed`
   node kinds), `decode.py` reads chunks from that description alone, and the Go side does too:
   `level/section_gen.go` is generated from those definitions (the section, the two paletted
   containers, the width functions), and `level` converts between it and its own palettes and
   bit storage, which is the runtime logic that stays by hand;
7. the text component itself (`chat/message.go`, `chat/nbtmessage.go`, about 400 lines): the
   style, click and hover events are generated from `nbt_schema.json`, and since 2026-09-08 the
   schema describes the whole component (`ComponentSerialization.CODEC`, recursive, with its
   legacy dispatch on `type`); the Go `Message` struct and its NBT/JSON readers are still by
   hand, since their behaviour (translation, formatting, the string and list forms) is most of
   the code.

The logic — NBT, framing, world files, the bot's event model, the server framework — stays
hand-written by design.
