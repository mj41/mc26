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
`ReadFrom`/`WriteTo`. A dispatch on a registry whose elements carry their own codec (the
particle types, registered with it in `ParticleTypes`; `REGISTRY_BOOTSTRAP` in the extractor)
becomes one struct with the id and the fields of every case (`types.ParticleOptions`); a case
the schema cannot type is rejected when decoded, the rest of the union stays usable. The header of every generated file lists the packets that were skipped and
why (`opaque`, `dispatch`, a branch-guarded reader); none is hand-written today (`hand_packets.json`
is empty but still honoured), and `src/protocol/handshake` is hand-written because the handshake
state has no packetid constants. `schemacov <version>` prints the coverage and the holes.

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
