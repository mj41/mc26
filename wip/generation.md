# Everything that mirrors Mojang's data is generated

Status: **done** 2026-09-09. Analysis of 2026-09-05, seven steps, and the rounds after them.

## The question, and the honest target

Measured on 2026-09-05 on the 26.2 build (`mc26 report`): generated 1.26 M lines (39 k without
translations), hand-written 21.0 k lines in 161 files. Three kinds of hand-written code, with
different answers:

| kind | lines then | what it is | generate? |
|---|---:|---|---|
| runtime kernel | ≈ 11.5 k | NBT, framing, VarInt, compression, encryption, RCON, region files, palette bit-packing, text rendering, the login flows | no: a library runtime, not a mirror of Mojang data; version-independent |
| data-shaped | ≈ 5.5 k | structs and tables that mirror a codec, a constant or a registry in the jar | yes: every piece has a machine-readable source of truth in the jar |
| framework glue | ≈ 4 k | the bot and server design: state machines, event dispatch, handlers | mostly no; ≈ 1 k of it copied packet fields and could be deleted |

Target: everything data-shaped generated; a small version-independent runtime and a small
framework stay by hand.

## What was done

Every step of the list is done; the details are in `docs/generated-vs-hand-written.md` (the
snapshot and the closed items), `docs/hand-written.md` (the inventory in three tiers), the
extractor headers (`data-gen/java/*.java`) and this repository's log, one commit per rule.

| when | what | measure |
|---|---|---|
| 09-05 | components from `DataComponents` codec chains (103 of 111), the reflection extractor and 58 overrides gone, the `wire` package | hand 21.0 k → 20.5 k |
| 09-06 | `GenNbtSchema` → registry elements, the `Registries` struct, chat style and events; `Holder`/`HolderSet`/`Color` bridges; `--only` extraction | 29 of 29 registries typed |
| 09-06 | hand packets gone (guards, enum dispatch, rest bytes, payloads), shared wire structs, entity metadata and particles, the bot keeps the packets instead of copying them, the constants extractor; the smoke test fails on a registry key the types lack | 26.2 packets 192 → 217 of 232 typed |
| 09-06 → 07 | some forty walker rules for packets (loops, bit fields, id maps, inherited statics, getters, enums by name…); the `javap` dev command; crosscheck: a recording proxy and a decoder written from the JSON alone (Python then, a Go module of its own since 09-09), round-trips every recorded packet; `prims.json` and `nodes.json` say what every primitive and node kind is on the wire; every component generated | every packet and component of 26.1, 26.2, 26.3-pre-2 fully described |
| 09-08 | chunk sections described down to palettes and packed longs (`rest`, `packed`), `level/section_gen.go`; the management API client from the server's OpenRPC document; every naming override, hand list and the phase file gone (names from the bytecode's variables); the handshake generated; the text component in the schema; registry dispatch cases from bootstrap classes; memory limits on everything the harness starts | hand-crafted inputs: `nodes.json` and `prims.json` only |
| 09-08 → 09 | the frame as data in `nodes.json` (the recording proxy follows it); every synchronized registry and save-format shared type described (dialog actions by reflection over the static map, inherited factories, helper groups, self-referencing eithers, `StateHolder`, bootstrap methods by method reference, 26.1's `MutableObject` recursion); Go types for `either`, `ref`, `recursive`, xor-of-fields structs, generic records (`Weighted<T>`) | 32 of 32 registries and 11 of 11 shared types in 26.3-pre-2; no opaque node, no caseless dispatch, every ref resolves |

On 2026-09-09 (26.3-pre-2): generated 98.3 % of lines, 71.1 % without translations (52.9 k
generated against 21.5 k hand-written in 150 files: 86 upstream, 45 modified, 19 new). The
hand-written count did not fall far below the baseline because typed surface was added (the
bridges, the management client, the level conversions) while data-shaped code went.

## What stays raw, and why

Maps whose value type depends on the key (game rules, environment attributes, enchantment
effect components), the custom dialog payloads, two union fields whose type differs per case,
the chat `Message` behaviour, the save formats read with keyed accessors (no codec to read them
from; `LevelSettings` has none in any version).

## Why not further

- The runtime kernel: generating it means transpiling Mojang's Java (`NbtIo`, `FriendlyByteBuf`,
  `RegionFile`, `PalettedContainer`) into Go — a bigger project than the library, worse Go, and
  the kernel did not change across 26.1–26.3.
- The framework: our design and the reason the library is usable; the tables it walks are
  generated already, the state changes are data in `nodes.json`.
- A DSL for handlers only moves the hand-written lines into the DSL file.

## Risks, as they turned out

- DataFixerUpper codec chains were as varied as feared; every hole was closed by a rule read
  from the bytecode, never by a hand table; the schema check in every build keeps it so.
- Mojang refactors codecs between versions (26.3 moved the registry codec classes and renamed
  a lookup; 26.1 still had `MutableObject` recursion): one or two rules per release, found by
  the pipeline, is the observed rate.
- Optional NBT fields with defaults: `omitempty` plus the default in a comment; the smoke test's
  registry check has caught every wrong tag so far.
