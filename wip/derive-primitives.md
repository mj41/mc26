# Derive the composed primitives; leave only the natives by hand

Status: **done** 2026-09-09 (this commit). Four definitions remain by hand, said so below.

## What was typed by hand, and is not any more

`gen/hand-crafted/prims.json` held 50 named primitives as node trees written *from the
bytecode* — each entry naming the Java member it was read from, none of them read by the
walker, which stopped at those names because three tables in `GenPacketSchema.java` told it to
(`BUF_READS`, `BYTEBUF_CODECS`, `KNOWN_CODEC_FIELDS`, some ninety lines). Nothing was wrong in
them, but they were the last place a Minecraft version could change something and the pipeline
would not notice: a `readBlockPos` that packs its bits differently would have been described as
before until a smoke test failed.

## What is done

- `prims.json` now says only *which Java members stand for each name* (`FriendlyByteBuf.readUUID`,
  `ItemStack.OPTIONAL_STREAM_CODEC`, …; the first the version has is used, so `readInstant`
  leaving `FriendlyByteBuf` in 26.3 is just the next member). The three tables are gone; the
  extractor loads the file (`MC_PRIMS_JSON`, mounted read-only into the container) and stops at a
  member's name exactly where the file names it.
- Every composed primitive is derived: the extractor walks the member with the name's own stop
  rule switched off, so `UUID`, `BLOCK_POS`, `ITEM_STACK`, `COMPONENT_PATCH`, `BYTE_ARRAY`,
  `OPTIONAL_ITEM_STACK`, `GAME_PROFILE_PROPERTIES`, … are read from the jar like a packet is, and
  written into a `prims` section of `packet_schema.json` (name, definition, the member, the
  members that stand for the name). 26 of 28 in 26.1/26.2, 27 of 28 in 26.3 (`VAR_INT_LIST` has
  no member there, `BYTE_BIT_SET` and `MESSAGE_SIGNATURE` none in 26.1/26.2: a primitive a
  version has no member for is left out, and the build checks nothing names it).
- Compared with the hand definitions of the same day: 19 identical, the rest equivalent in a
  form the walker prefers (a `string` node with its cap instead of the `STRING` primitive, the
  `holder` for the item id, the two counts of a component patch guarded by "either count
  non-zero", which reads nothing extra). Packets, structs and components came out identical.
- Walker rules the derivation needed, all general: an anonymous codec's `decode` with its
  constructor arguments bound to the fields; `(int) l`, `l >> 32`, `l & mask` on a long as
  unnamed bit ranges, named by the constructor they go into (`new ChunkPos((int) l, (int) (l >>
  32))`); the static helper rule reads through `l2i`; a component type's `streamCodec()` as a
  dispatch whose key is the type field, and a copy made by `decode` keeps that pending name;
  `if (a == 0 && b == 0) return EMPTY;` negated as one region of two alternatives (it was two
  regions, i.e. a conjunction, and the second lost); a collapsed repetition guarded by what was
  open around its loop, not by the next read; `ByteBufCodecs.readCount(buf, 16)` keeps its cap
  on the list it bounds.
- The Go side reads the schema's `prims` section: the build check, the generator (a primitive
  with no Go type is rendered from its definition), `docs` (the table says which member each was
  read from, or "by hand"), `crosslang` (no `--prims` flag), `protodefdiff`, `cmd/prims`.
- The prose on each primitive moved from `prims.json` into `gen/docs/protocol.mc26tmpl.md`
  ("Notes on the primitives"); the JSON is data only, like `nodes.json`.

## What stays by hand

| primitive | why the bytecode of the reader cannot say it |
|---|---|
| `CHUNK_SECTIONS` | the section count comes from the dimension type sent at configuration time, not from the packet; the reader takes bytes and another class reads sections out of them later |
| `PALETTED_BLOCK_STATES`, `PALETTED_BIOMES` | the palette byte selects a storage width through a table (`Strategy.getConfigurationForBitCount`) and the entry count is a constant of the strategy, neither of which is a read |
| `ENTITY_DATA` | a loop terminated by a byte value (0xff), each entry dispatching on a serializer id whose cases are `entity_data.json`; the walker has no `whilelist` rule for a terminator read as part of the entry |

They are marked `hand` in `prims.json` with that reason, and the build reports their number.
`READER_RULES` (4 readers given their node outright: the custom-query payload whose form only the
writer shows, the entity-data unpack, the two anonymous entity-data serializers) and
`FIELD_PRIMS` (the chunk buffer pinned to `CHUNK_SECTIONS`) stay for the same reasons. One of
the four is known to be loose: `EntityDataSerializers$2` (`OPTIONAL_BLOCK_STATE`) writes the
state id itself with 0 for absent, not the `n - 1` of `OPTIONAL_VAR_INT` it is named with; the
note on `OPTIONAL_VAR_INT` in the protocol document says so.

## What is not a task

The rest of the hand-written Go is behaviour (`docs/hand-written.md`): the runtime kernel, the
bot and server, the bridges. The save formats have no codec to read.
