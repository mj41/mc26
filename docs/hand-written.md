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
| `net` (conn, framing, CFB8) | 620 | the connection: length-prefixed frames, zlib compression once negotiated, AES/CFB8 encryption after login | the `frame` entry of `nodes.json`: prose in `wire`/`fields`, and since 2026-09-08 the same as data in `frame.data` (length, body, compression with its trigger packet and limits, encryption with its trigger packets and cipher, the states and every transition with the packet that causes it); the recording proxy of `crosscheck` follows the connection from `frame.data` alone |
| `nbt`, `nbt/dynbt` | 5,053 | the NBT codec (struct tags, dynamic values, SNBT) | the tag table of `prims.json`'s `NBT` (every id, its payload) — ~150 lines in decode.py; SNBT and reflection-based struct mapping are a convenience, not a wire need |
| `registry` | 710 | the registry container the configuration phase fills, lookups by id and name, the NBT bridge types (`Holder`, `HolderSet`, `Either`, `Color`) | `registries.json` for the built-in ids, the generated `Registries` struct for the synchronised ones; the container itself is a map |
| `protocol/types` (types.go) | 142 | the aliases from `wire` and the bridges to chat, level and components | nothing: naming |
| `level/component/types.go` | 328 | the item stack bridges (`SlotData`, `Typed`, `Patch`, delimited forms) | described by `prims.json` (`ITEM_STACK`, `COMPONENT_PATCH`, `DELIMITED_COMPONENT_PATCH`, `TYPED_DATA_COMPONENT`); Go keeps hand types for the API |
| `chat` (message, nbtmessage, jsonmessage, decoration, events) | 1,321 | the text component: its NBT and JSON forms, translation, formatting; the style, click and hover events are generated (`style_gen.go`) | described since 2026-09-08: `nbt_schema.json`'s types carry `ComponentSerialization.CODEC` in full — a recursive `Component` that is a string, a non-empty list of components, or a compound of the contents (a legacy dispatch on `type`: text, translatable, keybind, score, selector, nbt, object), `extra` and the style; the Go `Message` stays by hand for its behaviour, another binding can generate its struct from the schema |
| `chat/sign` | 242 | the signature cache, the session and its verification, the unpacking of a signed body against the cache; the wire types are generated | logic on top of generated types |
| `level` (chunk, palette, bitstorage, chunkstatus) | 1,100 | palettes and bit storage: get, set, palette growth; the chunk's wire form is generated (`section_gen.go`, from `prims.json`'s `CHUNK_SECTIONS` / `PALETTED_*`) and converted from and to | the container logic is a binding's own; the wire form is in the JSON (`rest`, `packed`) |
| `level/block` (block.go, properties.go) | 194 | block-state helpers on the generated tables | `blocks.json` |

A binding that writes Tier A from `nodes.json` + `prims.json` can then generate everything
else, which is what the Go tree does. The frame is in `nodes.json` as data (`frame.data`) as
well as prose; the cipher it names (AES-128/CFB8, key and iv the login's shared secret) is a
standard one every language has.

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
- Nothing in Tier A needs Java to write in another language any more: the frame is data in
  `nodes.json` (`frame.data`, since 2026-09-08) and the text component's codec is in
  `nbt_schema.json` (same day).
