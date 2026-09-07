# gen — the `mc26` command, the generators and the library sources

The Go side of the repository (module `github.com/mj41/mc26`, rooted one level up, so every
command runs as `go run ./gen/cmd/<name>` from the repository root). Its only inputs are a data
directory (an extraction or an `mc26-data` checkout) and the sources in `src/`; it never needs
Java itself.

```
gen/
├── cmd/mc26/            extract · build · smoke · e2e · pipeline · release · report · commit · latest · tag · import-src · import-examples
├── cmd/packetdiff/      wire-layout diff of two versions (packet_schema.json + packets.json)
├── cmd/mcmeta/          registry preview and check against misode/mcmeta, no Java needed
├── cmd/schemacov/       how much of packet_schema.json is fully typed, and why the rest is not
├── internal/extract/    downloads (jar, language files) and the extraction container; _meta.json
├── internal/generate/   the generators (gen_*.go) and their helpers
├── internal/build/      copy src/ + generate + README/CI + go mod tidy, gofmt, build, vet, test
├── internal/smoke/      vanilla server + `go test ./bot -run TestSmoke`
├── internal/gitx/       branch, replace tree, commit, tag, push
├── internal/importsrc/  one-time import of the library sources and examples from a go-mc tree
├── internal/paths/      <root>/temp layout (data/<version>, cache, lib/<version>, smoke/<version>)
├── hand-crafted/        inputs that are not in any Mojang output (see hand-crafted/hand-crafted.md)
├── templates/           version.go, packetid.go, README.md, ci.yml, … (text/template)
├── src/_versions/<v>/   files an older version needs different from src/ (same relative paths), applied by build; the underscore keeps them out of every ./... walk
└── src/                 the hand-written library packages; module github.com/mj41/go-mc26, no generated files
```

`src/` is written for the newest Minecraft version. When a packet gained a field in a later
version, the older version's copy of the file that uses it lives under `src/_versions/<version>/`
(26.1: `server/login.go` without the session id). Check the packet with `schemacov -show
<flow/name> <version>` before writing an overlay: a struct literal that omits a field still
compiles, so the compiler only reports fields that are *read*. Keeping the generated packet
itself as the state (`basic.Player.Login`, `Player.Spawn`, the screen's `types.ItemStack` slots)
rather than copying its fields avoids most overlays.

## Generators

`build` runs them in this order into the library tree:

| generator | input | output |
|---|---|---|
| version | `version.json` | `data/version/version.go` — `Name`, `ProtocolVersion`, `DataVersion`, pack and Java versions, `DataSource`, `Generator` |
| packetid | `packets.json` | `data/packetid/packetid.go` — ids per state and flow, `String()` |
| soundid | `registries.json` | `data/soundid/soundid.go` |
| item | `items.json` + `registries.json` | `data/item/item.go` |
| blocks | `blocks.json` + `block_properties.json` | `level/block/blocks.go`, `block_states.nbt`, `properties_enum.go` |
| entity | `entities.json` | `data/entity/entity.go` |
| component | `components.json` + the `components` section of `packet_schema.json` (the stream codec of every `DataComponents` registration) + `hand_components.json` | `level/component/components.go` (registry), one `<name>_gen.go` per component, `enums_gen.go`, `structs_gen.go`, a round-trip test |
| blockentities | `block_entities.json` | `level/block/blockentity.go`, `blockentities.go` |
| registryid | `registries.json` | `data/registryid/*.go` |
| biome | `biomes.json` | `level/biome/list.go` |
| lang | `lang/*.json` | `data/lang/<locale>/<locale>.go` |
| packets | `packet_schema.json` + `packets.json` + `packet_phases.json` | `protocol/<state>/{clientbound,serverbound}_gen.go` (+ round-trip tests), `protocol/types/{enums,structs}_gen.go` |
| entity data (inside packets) | `entity_data.json` | `protocol/types/entitydata_gen.go` (serializer names, `NewEntityDataValue`), `data/entitydata/entitydata_gen.go` (field index constants per class, the fields of every entity type) |
| constants | `constants.json` | `data/constants/constants_gen.go` — the compile-time constants of a few classes (inventory slot layout, section geometry, level limits, living-entity NBT keys) |
| nbt | `nbt_schema.json` + `naming_overrides.json` | `registry/elements_gen.go` (the registry elements sent in the configuration phase, their enums and records, a decode test), `registry/registries_gen.go` (the `Registries` struct), `chat/style_gen.go` (`Style`, `ClickEvent`, `HoverEvent`, `Decoration`) |

Everything else in the library is copied from `src/` as is. A file the generators write is never
in `src/`; `import-src` recognises them by their `Code generated … DO NOT EDIT` header.

### Packet structs

Every fully typed packet of `packet_schema.json` (a typed codec tree read from the jar's bytecode:
`struct` with named fields, `prim`, `string`, `list`, `optional`, `map`, `enum`, `registry`,
`holder`, `holderset`, `resourcekey`, `nbt`, `unit`) becomes a struct with `PacketID()`, `ReadFrom`
and `WriteTo` under `protocol/<state>/`. A packet named the same in both flows gets a
`Clientbound`/`Serverbound` prefix. Enums and the shared wire structures land in `protocol/types`;
their primitives (`List`, `Map`, `Holder`, `ItemStack`, `Text` = `chat.Message`, `NBT`, …) are
hand-written in `src/protocol/types/types.go`. The plain records every package shares (`Vec3`,
`GlobalPos`, `GameProfile`, `BlockHitResult`, listed in `wireStructs`) are generated once into
package `wire` (`wire/structs_gen.go`, with the enums they use in `wire/enums_gen.go`) from
wherever the schema first shows them, and `protocol/types/wire_gen.go` aliases them for the
packets. A structure another package implements by hand
with the same wire form (the signed-chat types of `chat/sign`, `level.BlockEntity`) is a leaf:
`externalHandTypes` in `gen_packets.go` maps its schema name to the Go type, and nothing inside it
counts as a hole. A packet whose entries carry only the parts an `EnumSet` field selects
(`player_info_update`) gets an entry type with a `fields(guard)` selector and a custom
`ReadFrom`/`WriteTo`. A dispatch on a registry whose elements carry their own codec (particle
types, recipe and slot displays, number formats, position sources, debug subscriptions:
registered with it in a bootstrap class listed in `REGISTRY_BOOTSTRAP` in the extractor — in its
`bootstrap(Registry)` method or its `<clinit>`, through any `register…` helper, either as a codec
factory, a `TYPE` record holding the codec, or an object whose `streamCodec()` returns it; a
one-argument static factory passed to `dispatch` wraps every case, which is how a debug
subscription becomes an optional per case) becomes one struct with the id and the fields of every
case (`types.ParticleOptions`, `types.SlotDisplay`); a case the schema cannot type is rejected
when decoded, the rest of the union stays usable. A
codec that refers to itself (a composite slot display holds slot displays) is a `ref` node in
the schema; the generator renders it as a list of the union or, for a single value, as
`types.Box`. A field read only under a condition on earlier fields — a flag bit, a boolean, an
enum or int compared with a constant, or a small predicate of it (`shouldHaveParameters(method)`,
evaluated over its domain) — carries the condition as `when` in the schema and is read and
written in its own segment of a sequential `ReadFrom`/`WriteTo` (`stop_sound`,
`player_look_at`, `set_objective`, `set_player_team`, the command tree, the advancements). A loop whose counter
starts at zero and is compared with a bound is the repetition it looks like: a constant bound
repeats the body that many times (`sign_update`'s four lines of text), a bound read from the
buffer makes it a length-prefixed list (`section_blocks_update`). An array the reader allocated
stands for the size it was made with, so the repetition lands on the field that array fills
instead of disappearing from the struct.

A loop with no counter that ends on a test of something it read is the list that shape means:
`set_equipment` reads a slot byte whose top bit says another entry follows, so it becomes a
`whilelist` node and a list type that reads until the bit is clear. The generated type takes
the bit off what it read and puts it back on what it writes, so the value holds only the slot;
an empty list has no wire form and refuses to encode, which is also why the packet is left out
of the zero-value round trip.

A hand-written type stands in for a shape the schema does not know, and the generator
substitutes it wherever that name appears. When the schema does know the fields, they are what
the wire carries: Mojang writes a chunk position as one packed long in most packets and as two
var ints in a waypoint, both from `ChunkPos`, and substituting the packed type for the second
read eight bytes where the wire has two. A hand-written type that a primitive maps to is the
definition of that primitive, so a struct of the same name carrying fields is generated instead.

A dispatch can also be written as a call rather than as a codec: the command tree reads an id
in `command_argument_type`, takes that element out of the registry and asks it to read the
rest. An element taken out of a registry by an id just read carries the registry with it, and a
call on it that takes the buffer is a dispatch keyed by that registry, its cases coming from
the registry's bootstrap class — the name each registration was given and the class of the
object registered under it, with that same method interpreted on each class. A case whose
reader reads nothing carries no payload.

A reader called with values read earlier is interpreted with them bound to its parameters, so a
branch on one of them is a condition on the field that carries it. Such a value is not a read of
that reader, only something it was handed, so it becomes no field of its own; and the guards it
carries belong to the caller's struct, where the value is a field, so the reader's fields are
spliced in beside it rather than nested. Some bits of a value compared with a number
(`(flags & 3) == 2`, the node type of the command tree) is a condition like any other. A
predicate the extractor could only evaluate by enumerating a byte's domain is written back as
the bit it is when the values say so. A value read and then shifted (`buf.readVarInt() - 1`)
compared with a number is the value compared with that number shifted back, which is how the
optional 256-byte signature of `delete_chat` and of every entry of a chat packet's last-seen
list is read at all.

A dispatch whose cases all read the same shape is not a union at all. `award_stats` sends stats
as an id in `stat_type` followed by an id in whatever registry that stat type wraps: the codec
of every case is a field each element's constructor builds the same way, so the whole dispatch
is a struct of the key and that one shape. The generator writes the second id as a var int
whose registry the first names.

An enum whose constants each carry their own case — a reader in a field, as `TrackedWaypoint$Type`
holds the subclass that takes the rest of the buffer, or the codec of that case, as
`PositionPath$Type` does — is a dispatch on that enum, and becomes a union the same way a
registry dispatch does, keyed by the enum instead of a registry id and numbered by the ordinal. It is named after what it is, the part the constant selects
(`types.TrackedWaypointPayload`), so the class holding it keeps its own name. A case whose
reader reads nothing is a case with no payload.

An enum read by name rather than by ordinal (`StringRepresentable.fromEnum`, decoded with
`EnumCodec.byName`) becomes a `stringenum` node carrying both the constants and the names they
are written as, and generates a `pk.String` type whose constants are those names — so a name a
later version adds still decodes instead of failing.

A reader that hands the buffer to a void helper reads through it, so the helper is interpreted
and what it read becomes part of the value; a helper that reads nothing is a writer or
bookkeeping and is ignored. Dropping one that does read would produce a struct short of fields
while the packet still counted as fully typed.

The schema's leaves are named primitives — `VAR_INT`, `ITEM_STACK`, `COMPONENT_PATCH` — and the
names are all this generator needs, because the library has a Go type for each. A generator for
another language has nothing, so `hand-crafted/prims.json` says what each name is on the wire: a
node tree in the schema's own vocabulary where one describes it, a bit layout where the value is
fields inside an integer, and `native` for the few a language implements in its runtime (the
var-int framing, the binary NBT format, the integers themselves). Each definition records the
Java member it was read from. `mc26 build` checks that every primitive a version uses is defined
and that the definitions resolve, so a version that introduces a new one stops the build instead
of producing a binding with a hole in it; `prims <version>` prints the same report.

`hand-crafted/nodes.json` does the same for the vocabulary the trees are built from. A node kind
such as `list` or `holder` meant something exact, but that meaning lived only in this extractor
and this generator; it now says what the bytes are, what the node's own keys mean, and which Go
type it becomes. It also carries the frame a packet travels in, which the schema never mentions
and without which a primitive that reads to the end of the packet has no meaning. The build fails
on a node kind nobody has defined, as it does on a primitive.

`crosscheck <version>` is the test of whether all that is true. It starts a vanilla server,
records the packets it sends, and hands the bytes to a decoder in another language that has only
the JSON: `gen/crosslang/decode.py` reads the schema, the primitives and the node kinds, decodes
each captured packet and encodes it again. It passes only when every packet comes back byte for
byte. A description that is complete to a reader who already has this library, and no one else,
fails there. The recording half checks the same packets in Go as it goes: every one must decode
into its generated type and consume the body exactly, which is what `Packet.ScanAll` is for and
what `New<Flow>(id)` in each protocol package makes possible. A packet whose schema is short of a
field decodes without complaint, so reading it is not the test; reading all of it is. Byte-for-byte
equality is left to the other language, because this library writes a chat component back as the
compound that means the same rather than the bare string it arrived as. It passes on 26.1 and 26.2 — every packet a vanilla server sends, decoded and
encoded again by a reader that has never seen Go. It does not pass on 26.3-pre-2, which has two
shapes the extractor still reads wrongly: the movement packets, whose step count chooses between
two branches, and the chunk packet, which changed.

The header of every generated file lists the packets that were skipped and
why (`opaque`, `dispatch`, a branch-guarded reader); as of 26.1, 26.2 and 26.3-pre-2 there are
none — every packet of the protocol is generated — and none is hand-written
(`hand_packets.json` is empty but still honoured), while `src/protocol/handshake` is
hand-written because the handshake state has no packetid constants. `schemacov <version>` prints the coverage and the holes.

```go
var sh play.SetHealth
if err := p.Scan(&sh); err != nil { … }          // p is the pk.Packet a handler received

use := play.UseItem{Hand: types.InteractionHandMainHand}
err := conn.WritePacket(pk.Marshal(use.PacketID(), use))
```

`bot/`, `server/` and the examples use these structs only; after a version bump the compiler
points at every field a packet gained or lost.

### Data components

The same interpreter reads `DataComponents.<clinit>`: every `register("name", builder ->
builder.persistent(CODEC).networkSynchronized(STREAM_CODEC))` yields the component's wire tree
(a component without a stream codec is sent as the NBT of its persistent codec; a chat
component as text). `gen_components.go` renders one type per component into
`level/component` — a struct with the record's fields, the enum itself for enum-valued
components, `struct{ Value T }` for a single value — plus the records and enums they share.
The registry `components.go` maps ids to them. The wire generics come from package `wire`
(shared with `protocol/types`); item stacks inside components are `SlotData`, typed
components `Typed`, patches `Patch` (hand-written bridges in `src/level/component/types.go`).
Components whose codec has a dispatch or recursion the schema cannot type are listed in
`hand_components.json` and kept by hand.

### Entity metadata

`GenEntityData` reads, by reflection after bootstrap, the synched fields of every entity class
(`SynchedEntityData.defineId`: index and serializer) and types the serializers' codecs with the
packet interpreter. The `set_entity_data` packet is generated with a `types.EntityData` list
(index, serializer id, value) whose values come from `types.NewEntityDataValue(serializer)`;
`data/entitydata` names the indices per declaring class (`entitydata.LivingEntityHealth`), which
hold for every subclass, and lists the fields of every entity type. A serializer the schema
cannot type (particles: a dispatch on the particle type registry) is listed in the header of
`entitydata_gen.go`; decoding a value of it fails, since its length is unknown.

### Registry elements and chat structures

`GenNbtSchema` interprets the DataFixerUpper codecs the same way: every
`RecordCodecBuilder.create(instance -> instance.group(Codec.BOOL.fieldOf("has_skylight")…))` of
the registries in `RegistryDataLoader.SYNCHRONIZED_REGISTRIES` becomes a tree of keys, types,
optionality and defaults, and `gen_nbt.go` renders it as a struct with `nbt` (and `json`) tags:
`registry.DimensionType`, `registry.Biome`, `registry.DamageType`, … and `registry.Registries`
listing them, which the bot decodes the `registry_data` packets into. A `MapCodec` used inline
becomes an embedded struct (its keys sit at the parent's level), a `StringRepresentable` enum a
string type with constants, a `RegistryFileCodec` a `Holder[T]` (id or inline element), a
`HolderSet` a tag, an id or a list of ids, and a dispatch whose cases the walker can enumerate
(the click and hover events) one struct with the fields of every case. What it cannot type — an
`IntProvider` (`either`), a dispatch on a registry (`Dialog`) — stays a raw field and is listed
in the header of `elements_gen.go`, so the rest of the element is still typed. The chat side
(`chat.Style`, `chat.ClickEvent`, `chat.HoverEvent`, `chat.Decoration`) comes from the same
schema, with `json` tags for text components.

## A new Minecraft version

```bash
go run ./gen/cmd/mcmeta diff 26.2 26.3          # 0. preview: registries, block states, item components (no Java)
go run ./gen/cmd/mc26 extract --version 26.3    # 1. temp/data/26.3
go run ./gen/cmd/packetdiff 26.2 26.3           # 2. wire changes the hand-written code must follow
go run ./gen/cmd/schemacov 26.3                 # 3. packets the generator cannot type yet → walker rule, or hand_packets.json + hand.go
go run ./gen/cmd/mc26 extract --version 26.3 --only GenNbtSchema   #    iterate on one extractor (15 s a round)
go run ./gen/cmd/mc26 build --data 26.3         # 4. the compiler points at the rest
go run ./gen/cmd/mc26 smoke --version 26.3      # 5. against a vanilla server
go run ./gen/cmd/mc26 release --version 26.3    # 6. branches + tags in ../mc26-data and ../go-mc26 (push separately)
```

## Adding a generator

1. `internal/generate/gen_foo.go` with `func genFoo(jsonDir, outRoot string) error`; read inputs
   with `readJSON`, `readHandCrafted`; write with `writeFile` or `executeTemplate` (gofmt included).
2. Append `{"foo", genFoo}` to `Generators` in `internal/generate/generate.go`.
3. Start the output with `generatedHeader(...)` so `import-src` and reviewers recognise it.

## See also

- [mcsrc.dev](https://mcsrc.dev) — Fabric's in-browser decompiled Minecraft source; quickest way to
  look at one class of a 26.x jar.
- [Vineflower](https://vineflower.org) over the server jar when a grep-able source tree is needed
  (1.12.0 handles the 26.x class files on JDK 25).
- [misode/mcmeta](https://github.com/misode/mcmeta) — processed data-generator reports for every
  release and snapshot (`cmd/mcmeta` reads them).
