# Zero hand-written lines in go-mc26

Status: **decision** — what remains is the owner's call, not more generating. Started and
finished on 2026-09-09 for everything that had data behind it. The question: what of the library's 14,230 hand-written lines (`report --version 26.3-pre-3`: 10,614
in 84 files plus 3,616 of tests) can still be generated, and what "0" would take.

The answer, in one line: the ~780 lines that had data behind them are generated (2026-09-09);
about 1,900 more could be, but that only moves them into the generator and makes them worse;
the remaining ~7,500 are runtime with no Mojang source, and "0" for them means a module of
their own, not a generator.

What the work measured, which is not what it set out to measure: generating a shape is a
correctness tool, not a line-count tool. Seven defects came out of it, each one code that had
been wrong against a real server or a real world for as long as it had existed, and the biggest
item — the saved chunk — ended with more hand-written lines than it started, because the format
is not what the hand-written types claimed. Generating a thing is how you find out what it is.

## What generating the save format actually did

The saved chunk was the biggest item of the honest half, and generating it **added**
hand-written lines:

| file | before | after |
|---|---:|---:|
| `save/chunk.go` | 117 | 92 (the compression wrapper, the short names, the entity NBT) |
| `level/chunk.go` | 386 | 411 |
| `level/savepalette.go` | – | 107 |
| `_versions/26.1` and `26.2` overlays | – | 170 |

The type declarations moved into the generator, and the conversion grew, because the format is
not the shape the hand-written types claimed. What generating it found, each a real defect on a
real world, and the first two not even in the code being generated:

- **an NBT list wrote its elements by reflection.** The encoder asks a struct field and a map
  value to marshal themselves and never asked a list element, so a list of eithers came out
  carrying the literal keys `Left` and `Right`. That is every generated NBT list of holders,
  eithers and colours, not only the palette.
- **an either took whichever side decoded first**, and the NBT decoder is lenient enough that a
  compound read into a string leaves it empty rather than failing, so the first side swallowed
  every tag. It now takes the side whose tag type matches, and unwraps the `{"": value}` form an
  NBT list uses when its elements are not all the same shape.
- **the block state's keys were assumed rather than read**: the extractor described every
  version with `id` and `properties` because that codec had a hand-written stand-in. They are
  `Name` and `Properties` until 26.2, so the older versions were described as the newer one.
- **a bare block id means the block's default state**, not the zero value of its Go struct: a
  wall torch faces north by default and nothing faces the zero direction, so a 26.3 chunk failed
  on the first torch. The blocks generator emits the default from the data now.
- from 26.3 an entry with no properties is written as the bare id and one with them as a
  compound, while 26.1 and 26.2 always write the compound — two shapes, hence the overlay;
- a saved chunk carries a light-only section below the world and one above it, which the
  conversion has to skip rather than reject.

So the lesson of the biggest item is not the one the table below predicts. Generating a shape
does not shrink the code that uses it; what it buys is that the shape is this version's, that
the difference between versions is visible instead of silently wrong, and that
`mc26-data` now carries a description of the save format nothing else publishes. The
end-to-end run reads every region file the server wrote and converts every chunk in them, so
the next defect of this kind fails the pipeline rather than waiting for a user — and it has to
be every region: three of a world's four passed while the fourth held the entry that did not
read.

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
| `save/*` | 480 | **done 2026-09-09** for the chunk, and it cost lines rather than saving them (see the section above). `GenSaveSchema` walks the reader and the writer of a format and records every key with its tag type, default and nesting; the saved chunk is described in full for every version (`save_schema.json`), and `gen_save.go` renders it into `save/save_gen.go`. `level.dat` and the player data are still by hand (their readers use a third API) | done |
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
4. ~~**The paletted containers of a saved chunk**~~ — done 2026-09-09. They are read through a
   codec that is an instance field of `PalettedContainerFactory`, built per level rather than
   held in a static field, and applied by a lambda mapped over the compound. Two rules closed
   it: a codec a record component holds is followed to where the record is built (its own
   static factory, interpreted until it constructs the record), and a codec a lambda captures
   describes the compound it is mapped over. Both palettes now come out in full: the block
   states as an either of a block id or an id-and-properties struct, the biomes as a holder of
   the biome registry, each beside its packed long array.
5. **A key read outside the reader.** `DataVersion` is taken off the tag before `parse` (the
   data fixer reads it), so the chunk's schema has no such key although every saved chunk has
   one; a second reader would have to be walked for it.

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

1. **Generate what has data behind it — done 2026-09-09, and it did not reduce the line
   count.** The chunk status and the bootstrap (36 lines, and the status list was wrong in every
   version), the item stack and component patch wire types (264 lines to 13), and the saved
   chunk. Measured and dropped along the way: the profile structs and the text component's
   struct, where the schema-shaped part is 30 and 50 lines and the generated form would be the
   worse API. What is left of the save formats is `level.dat` and the player data, whose readers
   use a third API (`Dynamic` and a chain of `readAdditionalSaveData` methods per class).
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
