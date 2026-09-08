# What is still written by hand, and why

`go run ./gen/cmd/mc26 report --version 26.3-pre-2` (2026-09-08): 407 generated files, 150
hand-written ones — 21,481 lines, of which 10,864 are byte-identical to Tnze/go-mc, 6,444
derived from it and 4,173 new. The generators are done in the sense that every packet, data
component, registry element, entity metadata field, block, item, biome, translation, the
management API and the handshake come from the JSON; `gen/hand-crafted` holds only
`nodes.json` and `prims.json`. What remains by hand falls into three tiers, and the tier is the
question that decides what a base package is and what another language would have to write.

## Tier A — the wire base: what every binding has to have

Code that a client or a server of the protocol cannot do without, in any language. The JSON
describes most of it exactly; a binding implements the rest from `nodes.json` and
`prims.json`, which is what `gen/crosslang/decode.py` (Python, one file, no Go) demonstrates.

| package | lines | what it is | for another language |
|---|---:|---|---|
| `net/packet` | 1,751 | the field types (`VarInt`, `String`, `UUID`, …), `Packet`, `Tuple`, `Option`, `Array` | the 18 natives `prims.json` names (`native` entries: bool, i8…f64be, varint, varlong, string, rest/fixed bytes, fixed bit set, optional var int, lp_vec3, nbt) — ~200 lines in decode.py |
| `wire` | 560 | the generics behind the node kinds: `List`, `Map`, `Holder`, `HolderSet`, `Either`, `EnumSet`, `LenPrefixed`, `Counted`, `Box`, the packed positions, the NBT bridges | the 33 node kinds of `nodes.json`; decode.py's `d_*`/`e_*` methods are the reference (one per kind) |
| `net` (conn, framing, CFB8) | 620 | the connection: length-prefixed frames, zlib compression once negotiated, AES/CFB8 encryption after login | the `frame` entry of `nodes.json` (prose, not data) plus AES/CFB8 from the login's shared secret — the one part written from Java knowledge rather than the JSON |
| `nbt`, `nbt/dynbt` | 5,053 | the NBT codec (struct tags, dynamic values, SNBT) | the tag table of `prims.json`'s `NBT` (every id, its payload) — ~150 lines in decode.py; SNBT and reflection-based struct mapping are a convenience, not a wire need |
| `registry` | 658 | the registry container the configuration phase fills, lookups by id and name, the NBT bridge types (`Holder`, `HolderSet`, `Color`) | `registries.json` for the built-in ids, the generated `Registries` struct for the synchronised ones; the container itself is a map |
| `protocol/types` (types.go) | 142 | the aliases from `wire` and the bridges to chat, level and components | nothing: naming |
| `level/component/types.go` | 328 | the item stack bridges (`SlotData`, `Typed`, `Patch`, delimited forms) | described by `prims.json` (`ITEM_STACK`, `COMPONENT_PATCH`, `DELIMITED_COMPONENT_PATCH`, `TYPED_DATA_COMPONENT`); Go keeps hand types for the API |
| `chat` (message, nbtmessage, jsonmessage, decoration, events) | 1,321 | the text component: its NBT and JSON forms, translation, formatting; the style, click and hover events are generated (`style_gen.go`) | **not in the JSON**: the component codec (`ComponentSerialization`) is not extracted, so a binding writes it from the Java — the largest gap left (item 7 of [generated-vs-hand-written.md](generated-vs-hand-written.md)) |
| `chat/sign` | 382 | the signed-chat wire types (`PackedMessageBody`, `PackedSignature`, `FilterMask`) and the signature cache and session | the wire types are in `packet_schema.json` (26.3 fully; 26.1/26.2 leave `FilterMask` conditional); the cache and the signing are logic |
| `level` (chunk, palette, bitstorage, chunkstatus) | 1,184 | chunk sections and palettes: read, write, get, set | the wire form is `prims.json`'s `CHUNK_SECTIONS` / `PALETTED_*` (`rest`, `packed`); decode.py reads chunks from it alone; the container logic (get/set, palette growth) is a binding's own |
| `level/block` (block.go, properties.go) | 194 | block-state helpers on the generated tables | `blocks.json` |

A binding that writes Tier A from `nodes.json` + `prims.json` + the Java-derived pieces (the
frame's crypto, the text component) can then generate everything else, which is what the Go
tree does; the two gaps to close for that to be true without Java are the text component's
codec and the frame as data rather than prose.

## Tier B — application: a client, a server, world files, accounts

Not the protocol. Kept in this tree today because Tnze/go-mc kept them and the examples use
them; where they live later is the base-package decision.

| package | lines | what it is |
|---|---:|---|
| `bot`, `bot/basic`, `bot/world`, `bot/msg`, `bot/playerlist`, `bot/screen` | 2,554 (+1,111 tests) | a client: login, configuration, the event model, chunks, chat, tab list, inventories |
| `server`, `server/auth`, `server/command`, `server/internal/bvh` | 2,194 | a server framework: list ping, login, configuration, command graph, player list |
| `save`, `save/region` | 1,166 | level.dat, player data, region files |
| `microsoft`, `yggdrasil`, `offline` | 1,676 | Microsoft login, Mojang session server, offline uuids |
| `management/client.go` | 320 | the WebSocket + JSON-RPC transport under the generated management API |
| `net/rcon`, `net/queue` | 200 | RCON, a packet queue |

## Tier C — the tests the harness runs

`bot/*_smoke_test.go`, `bot/capture_check_test.go`, `management/smoke_test.go` (1,400 lines):
not library code; they exist for `mc26 smoke` and `mc26 crosscheck` and are the reason the
generated code is trusted. They stay wherever the harness can run them.

## Notes for the split (decided later, after the generators)

- A base package is Tier A plus everything generated. Its hand-written part is ~12,000 lines,
  three quarters of it `nbt` and `net/packet`, both byte-identical to upstream.
- Tier B is where API taste lives (the event model, the world view, the server framework);
  it changes for reasons that have nothing to do with the protocol and would be the second
  module or repository.
- The `_versions/26.1` overlay (one file, `server/login.go`) is Tier B: only the server
  framework differs between versions today.
- Two Tier A items still need Java to write in another language: the text component codec
  and the frame's encryption. Extracting `ComponentSerialization` into `nbt_schema.json` would
  close the first; the second is one paragraph of prose in `nodes.json` and a standard cipher.
