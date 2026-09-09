# What is written by hand, and why

Everything that mirrors Mojang's data is generated: every packet, data component, registry
element, entity metadata field, block, item, biome, translation, the management API and the
handshake come from the JSON, and the only hand-crafted inputs of the generators are
`nodes.json` (the node kinds and the frame) and `prims.json` (which Java members the named
primitives stand for). What remains by hand is behaviour: a runtime kernel that does not mirror
any data, a client and a server built on the generated types, and the tests the harness runs.
The kernel is part of the library (`gen/src`, module `go-mc26`); the client, the server and the
account flows are the kit (the `go-mc26-kit` repository), one source that builds against every
supported version of the library, which this repository tests against but does not own.

`go run ./gen/cmd/mc26 report --version <version>` measures it. On 26.3-pre-3: 408 generated
files against 150 hand-written ones of 21,465 lines (tests included), of which 10,865 lines are
byte-identical to Tnze/go-mc, 6,223 derived from it and 4,377 new; 98.3 % of all lines are
generated, 71.1 % without the translations. The line counts below leave the tests out.

## Tier A — the wire base: what every binding has

Code that a client or a server of the protocol cannot do without, in any language. The JSON
describes most of it exactly; a binding implements the rest from `nodes.json` and the schema's
`prims` section, which is what `gen/crosslang` demonstrates: a reader in a Go module of its own
that imports nothing from the library or the generators, and reads and re-encodes every
recorded packet byte for byte.

| package | lines | what it is | what another language takes from the JSON |
|---|---:|---|---|
| `net/packet` | 1,226 | the field types (`VarInt`, `String`, `UUID`, …), `Packet`, `Tuple`, `Option`, `Array` | the 18 natives the primitive definitions bottom out in (`native` entries: bool, i8…f64be, varint, varlong, string, rest/fixed bytes, fixed bit set, optional var int, lp_vec3, nbt): about 200 lines in `gen/crosslang` |
| `wire` | 671 | the generics behind the node kinds: `List`, `Map`, `Holder`, `HolderSet`, `Either`, `EnumSet`, `LenPrefixed`, `Counted`, `Box`, the packed positions, the NBT bridges | the node kinds of `nodes.json`, one case per kind; `gen/crosslang`'s codec is the reference |
| `net` (conn, framing, CFB8) | 618 | the connection: length-prefixed frames, zlib compression once negotiated, AES/CFB8 encryption after login | the `frame` entry of `nodes.json`: prose, and the same as data in `frame.data` (length, body, compression with its trigger packet and limits, encryption with its trigger packets and cipher, the states and every transition with the packet that causes it); the recording proxy of `crosscheck` follows a connection from `frame.data` alone. The cipher (AES-128/CFB8, key and iv both the login's shared secret) is standard in every language |
| `nbt`, `nbt/dynbt` | 3,328 | the NBT codec: struct tags, dynamic values, SNBT | the tag table of the `NBT` primitive's definition (every tag id and its payload): about 150 lines in `gen/crosslang`. SNBT and the reflection-based struct mapping are a convenience, not a wire need |
| `registry` | 612 | the registry container the configuration phase fills, lookups by id and name, the NBT bridge types (`Holder`, `HolderSet`, `Either`, `Color`) | `registries.json` for the built-in ids, the generated `Registries` struct for the synchronised ones; the container itself is a map |
| `protocol/types` (types.go) | 144 | the aliases from `wire` and the bridges to chat, level and components | nothing: Go naming |
| `level/component/types.go` | 277 | the item stack bridges (`SlotData`, `Typed`, `Patch`, the delimited forms) | the primitive definitions `ITEM_STACK`, `COMPONENT_PATCH`, `DELIMITED_COMPONENT_PATCH`, `TYPED_DATA_COMPONENT`, read from the jar; Go keeps hand types for the API |
| `chat` (message, nbtmessage, jsonmessage, decoration, events) | 712 | the text component: its NBT and JSON forms, translation, formatting; the style, click and hover events are generated (`style_gen.go`) | `nbt_schema.json` carries `ComponentSerialization.CODEC` in full: a recursive `Component` that is a string, a non-empty list of components, or a compound of the contents (a dispatch on `type`: text, translatable, keybind, score, selector, nbt, object), `extra` and the style. The Go `Message` stays by hand for its behaviour; a binding can generate the struct |
| `chat/sign` | 178 | the signature cache, the session and its verification, the unpacking of a signed body against the cache; the wire types are generated | logic on top of generated types |
| `yggdrasil/user` | 300 | a player's profile properties and public key as they travel in packets, and the validator with Mojang's session key | the wire form is in the schema (`GAME_PROFILE_PROPERTIES`, the chat session's public key); the validator is a binding's own |
| `save`, `save/region` | 797 | level.dat, player data, region files | the world formats are part of the library's promise; NBT-shaped, read with the NBT codec |
| `level` (chunk, palette, bitstorage, chunkstatus) | 996 | palettes and bit storage: get, set, palette growth; the chunk's wire form is generated (`section_gen.go`, from `CHUNK_SECTIONS` and `PALETTED_*`, the definitions still written by hand in `prims.json`) and converted from and to | the container logic is a binding's own; the wire form is in the JSON (`rest`, `packed`) |
| `level/block` (block.go, properties.go) | 163 | block-state helpers on the generated tables | `blocks.json` |

A binding that writes Tier A from `nodes.json` and the schema's `prims` section can generate
everything else, which is what the Go tree does. Nothing in it needs the Java sources: the
frame is data, the text component's codec is in `nbt_schema.json`, and the primitives are read
from the jar by the extractor.

## Tier B — application: a client, a server, accounts, examples

Not the protocol: what a program built on it does. This is the kit, module
`github.com/mj41/go-mc26-kit` in its own repository, with the examples as `examples/<name>`.

| package | lines | what it is |
|---|---:|---|
| `bot`, `bot/basic`, `bot/world`, `bot/msg`, `bot/playerlist`, `bot/screen` | 2,475 (+1,190 tests) | a client: login, configuration, the event model, chunks, chat, tab list, inventories |
| `server`, `server/auth`, `server/command`, `server/internal/bvh` | 1,801 | a server framework: list ping, login, configuration, command graph, player list |
| `microsoft`, `yggdrasil`, `offline` | 927 | Microsoft login, Mojang session server, offline uuids |
| `examples/*` | 1,292 | nine small programs: a bot with a console, an auto-fisher, a server-list ping, a region-file dumper, a player-data converter, a pressure test, a Microsoft login, a minimal bot |

Two pieces that read like application code stay in the library because the library needs
them: `management/client.go` (491 lines, the WebSocket and JSON-RPC transport in the same
package as the generated management API) and RCON with the packet queue (`net`, `net/queue`,
84 lines).

The kit has no per-version copies of anything. Where a Minecraft version differs in what the
kit does (the login finished packet gained a session id in 26.2), the code follows
`version.ProtocolVersion` of the library it is built with and writes the packet field by
field, so one source builds against every supported library version; the kit's workflow
checks that with a matrix.

## Tier C — the tests the harness runs

`bot/*_smoke_test.go` and `bot/capture_check_test.go` in the kit and `management/smoke_test.go`
in the library (about 1,100 lines) are not library code. They exist for `mc26 smoke` and
`mc26 crosscheck`, and they are
the reason the generated code is trusted: the smoke test reads every synchronised registry with
its generated type and fails on a tag it does not know, and the capture check decodes every
recorded packet into its generated type and requires the body to be consumed exactly.

## Why these stay by hand

- The runtime kernel (Tier A) does not mirror Mojang data; generating it would mean
  transpiling `NbtIo`, `FriendlyByteBuf`, `RegionFile` and `PalettedContainer` into Go, a
  bigger project than the library and worse Go, for code that has not changed across 26.1 to
  26.3.
- The client and the server (Tier B) are this library's own design: the event model, the world
  view, the command graph. The tables they walk are generated; the state changes they follow
  are data in `nodes.json`.
- The hand-written count does not fall far with each generation step because typed surface
  is added at the same time (the bridges, the management client, the level conversions) while
  data-shaped code goes; the tiers, not the line count, say what a binding has to write.
