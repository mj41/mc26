# Zero hand-written lines in go-mc26

Status: **open**, started 2026-09-09; the analysis is done, the generating is under way. The
question: what of the library's 14,230 hand-written lines (`report --version 26.3-pre-3`: 10,614
in 84 files plus 3,616 of tests) can still be generated, and what "0" would take.

The answer, in one line: about 780 lines have data behind them and should be generated (300 are
done, the save formats are the rest); about 1,900 could be generated but that only moves them
into the generator and makes them worse; the remaining 7,500 are runtime with no Mojang source,
and "0" for them means a module of their own, not a generator.

Measured on the way: two rows of the table below turned out to be mostly behaviour once read
(`yggdrasil/user`, `chat/message.go`), and one hand-written file was simply wrong (the chunk
statuses). Generating a thing is also how you find out what it is.

## Where the lines are

Non-test lines by file, from `gen/src` on 2026-09-09:

| package | lines | files |
|---|---:|---|
| `nbt` | 2,762 | decode 690, snbt_decode 568, snbt_scanner 432, encode 415, snbt 247, typeinfo 242, rawmsg 85, nbt 71, interface 12 |
| `net/packet` | 1,226 | types 680, util 294, packet 232, builder 20 |
| `level` | 996 | chunk 386, palette 376, bitstorage 215, chunkstatus 19 |
| `chat` | 712 | message 286, decoration 182, nbtmessage 123, jsonmessage 73, clickevent 29, hoverevent 19 |
| `wire` | 671 | wire 671 |
| `registry` | 612 | nbttypes 225, network 149, registry 124, codec 114 |
| `nbt/dynbt` | 566 | types 231, decode 194, encode 94, update 47 |
| `management` | 491 | client 491 |
| `net` | 481 | conn 249, rcon 216, interface 16 |
| `save` | 480 | dimension 163, level 118, chunk 117, playerdata 82 |
| `save/region` | 317 | mca 317 |
| `yggdrasil/user` | 300 | validator 92, user 84, pubkey 77, property 47 |
| `level/component` | 277 | types 277 |
| `chat/sign` | 178 | session 94, cache 42, sign 42 |
| `level/block` | 163 | block 91, properties 58, utilfuncs 14 |
| `protocol/types` | 144 | types 144 |
| `net/CFB8` | 137 | cfb8 137 |
| `net/queue` | 84 | queue 84 |
| `data/registryid/bootstrap` | 17 | builtinregistries 17 |

## Two kinds of line

Corrected on 2026-09-09, after the first two generations and a closer look at the rest: the
"about 3,100 lines" below splits again, and only about 1,200 of it is a reduction. The runtime
rows (the natives of `net/packet`, the framing, the node-kind generics of `wire`, the binary tag
codec of `nbt`) have no *data* to generate from: their source of truth is prose in `nodes.json`
and the primitive notes, so a generator for them is one template per kind, and the template *is*
the implementation. Those lines would move from `gen/src` into `gen/internal/generate` — the
report would say 0, the project would have the same number of hand-written lines, and the Go
would be worse to read (a template renders one shape; the hand-written generic is read by
people). Rows marked **move** below are that; rows marked **generate** have data behind them.

**A. Could come out of a generator.** About 3,100 lines, of which about 1,200 are a reduction (**generate**) and about 1,900 a move into the generator (**move**).

| what | lines | the source of truth | cost |
|---|---:|---|---|
| ~~`level/chunkstatus.go`~~ | 19 | **done 2026-09-09**: the `chunk_status` registry. The hand file listed 13 statuses and no version has those: 26.3 has 10 (`terrain` and `initialize_light` are new, `noise`, `surface`, `carvers`, `liquid_carvers` and `heightmaps` are gone), 26.1 has 12 | done |
| ~~`data/registryid/bootstrap`~~ | 17 | **done 2026-09-09**: a template over the generated block table, with the version's block count asserted | done |
| `protocol/types/types.go` | 144 | **move**: the alias table is `primTypes` in `gen_packets.go` plus the wire generics, both tables in the generator already; emitting the aliases from them moves ~60 lines and leaves the `EntityData` reader and the item stack wrappers, which are behaviour | half a day, little gained |
| ~~`level/component/types.go`~~ | 277 → 13 | **done 2026-09-09**: the five primitive definitions rendered into `level/component/wire_gen.go`; the file keeps the two aliases the generated components use. The generator learned a dispatch whose cases are another section (`casesFrom`) and names taken from the primitive. The fields now carry Mojang's names (`Item`, `Positive`, `Negative`), which the kit followed | done |
| `yggdrasil/user` structs | 208 → about 30 | **measured 2026-09-09, do not**: only `Property` is schema-shaped, and its hand form is the better API (plain strings, the optional signature folded in) where the generated one would be `pk.String` and `pk.Option`. `PublicKey` holds a parsed RSA key and an expiry time, which is parsing, not schema; `user.go` fetches a key pair over HTTP; the validator verifies signatures. B |
| `chat/message.go` struct | 200 → about 50 | **measured 2026-09-09, do not**: the `Message` struct is 50 lines of the file's 286 and the schema does describe it (`ComponentSerialization.CODEC`), but the other 236 are its methods — append, colour, translate, the two string renderings — and they would sit beside a generated struct instead of with it. B |
| `save/*` | 480 | `WorldOptions`, `RespawnData` and `WorldDimensions` are in `nbt_schema.json` already (`dimension.go` could go today). `level.dat`'s other keys, the chunk (`SerializableChunkData.parse`) and the player data are read in Java with keyed accessors (`getInt("xPos")`, `ValueInput.getIntOr`), not codecs: a new extractor rule that walks such a reader and records key, tag type and default gives the same schema shape as a codec. `chunk_status` is a registry, the ticks have codecs | two days, most of it the extractor rule |
| `net/packet/types.go` natives | 450 | **move**: the 18 natives are prose in `prims.json`, so the generator would carry one template per native | a day, nothing gained |
| `net/packet/packet.go`, `net/conn.go` framing | 270 | **move**: `frame.data` of `nodes.json` says what the frame is, and the recording proxy follows it, but the compression and cipher code is not in that data | a day, nothing gained |
| `wire/wire.go` | 600 | **move**: one generic per node kind, each the implementation of that kind; `gen/crosslang` is the same thing as one `case` per kind, and both are read by people | two days, nothing gained |
| `nbt` binary tag codec | 250 | **move**: the tag table of the `NBT` definition gives the payload of each id, but the reader around it is the implementation | a day, nothing gained |
| `registry/nbttypes.go`, `registry/network.go` | 300 | **move**: the NBT forms of `holder`, `holderset`, `either` and `color` are node kinds; the code is their implementation | a day, nothing gained |

Three pieces the generator needs before the component bridges can be rendered, all found on
2026-09-09 by trying:

1. **A dispatch whose cases come from elsewhere.** `TYPED_DATA_COMPONENT` is `{type, value:
   dispatch(key = the type field, casesFrom = packet_schema.json#components)}`; the union
   renderer only knows a dispatch that carries its cases inline. The rendering wanted is the
   hand-written `Typed`: read the id, ask the generated `NewComponent` for the value. The
   delimited form is the same under a `lenprefixed`, where an unknown component keeps its bytes.
2. **Names taken from the primitive, not the Java class.** `COMPONENT_PATCH` and
   `DELIMITED_COMPONENT_PATCH` are both Java `DataComponentPatch` with different fields, and
   `ITEM_STACK` and `UNTRUSTED_ITEM_STACK` are both `ItemStack`: the struct registry would
   collide, so a primitive's definition has to be named after the primitive.
3. **A holder with no direct form.** `ItemStack`'s item is `holder:item` with no `direct` node,
   which both `wire.Holder` and the JSON reader encode as a plain var int id — the same bytes
   the hand-written `ItemID` writes, so this one is a type change and not a wire change
   (checked, not assumed).

**B. No source of truth in Mojang's data: runtime.** About 7,500 lines: SNBT (1,250; Mojang
has `SnbtGrammar` in 26.x, a real grammar class, but a parser generated from it is more code
than it replaces), the reflection-based NBT struct mapping and `dynbt` (1,700), the palettes
and bit storage and the chunk conversions (980), the chat formatting and translation (450),
RCON, the packet queue and CFB8 (440), the region file reader (317), the management transport
(491), the session-key validator, the signed-chat session and cache, the block-state helpers,
the registry container and lookups (600). Version independent by observation: none of it
changed between 26.1 and 26.3.

**Tests** (3,616) follow their code: generated packages get generated round-trip tests, the
runtime keeps its own.

## What "0" takes

1. **Generate what has data behind it: about 780 lines, of which 300 are done.** Done
   2026-09-09: the chunk status and the bootstrap (36 lines, and the status list was wrong in
   every version), and the item stack and component patch wire types (264). What is left is the
   save formats (480), whose extractor rule — walk a Java reader that uses keyed accessors and
   record key, tag type and default — is the one real piece of work and the most valuable,
   since nothing describes `level.dat` or a saved chunk today. Measured and dropped along the
   way: the profile structs and the text component's struct, where the schema-shaped part is
   30 and 50 lines and the generated form would be the worse API.
2. **Decide about the rest, which is a move and not a reduction: about 1,900 lines.** The
   natives, the framing, the node-kind generics and the tag codec would go from `gen/src` into
   `gen/internal/generate` as one template each. The report would say 0; the project would not
   have one line fewer, and the templates read worse than the Go they replace. Recommendation:
   do not.
3. **Move the runtime into a module of its own** (about 7,500 lines: SNBT, the NBT struct
   mapping, palettes and bit storage, chat formatting, RCON, CFB8, region files, the management
   transport, the validators), version independent, in its own repository with its own history
   and a build matrix like the kit's (`go-mc26-base`, say; the name is the owner's). The
   generated library imports it; the kit imports both. Then `report` shows 0 hand-written lines
   in `go-mc26`, and every hand-written line in the project lives in one of two repositories
   that change on their own clock.

Step 3 is a relocation too, but an honest one: it makes the promise "go-mc26 is generated, never
edited" literal, it does not make any code worse, and it is the base package the tiers of
`docs/hand-written.md` describe. Whether to take it is a decision; step 1 stands on its own.

## Open

- Does step 2 happen at all, and the module's name.
- Whether the SNBT parser should follow `SnbtGrammar` (a Mojang source exists; the payoff is a
  parser that tracks Mojang's syntax changes, the cost a grammar-driven generator).
- The API names of the component bridges after generation (`Typed`, `Patch`, `SlotData` today;
  the definitions say `TypedDataComponent`, `DataComponentPatch`, `ItemStack`).
