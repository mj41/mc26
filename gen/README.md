# gen — the `mc26` command, the generators and the library sources

The Go side of the repository (module `github.com/mj41/mc26`, rooted one level up, so every
command runs as `go run ./gen/cmd/<name>` from the repository root). Its only inputs are a data
directory (an extraction or an `mc26-data` checkout) and the sources in `src/`; it never needs
Java itself.

```
gen/
├── cmd/mc26/            extract · build · smoke · pipeline · release · commit · latest · tag · import-src · import-examples
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
├── versions/<version>/  files an older version needs different from src/ (same relative paths), applied by build
└── src/                 the hand-written library packages; module github.com/mj41/go-mc26, no generated files
```

`src/` is written for the newest Minecraft version. When a packet gained a field in a later
version, the older version's copy of the file that uses it lives under `versions/<version>/`
(26.1: `server/login.go` without the session id, `bot/basic/info.go` without the online-mode
flag). Check the packet with `schemacov -show <flow/name> <version>` before writing an overlay: a
struct literal that omits a field still compiles, so the compiler only reports fields that are
*read*.

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
| component | `components.json` + `component_schema.json` (+ overrides) | `level/component/components.go`, `*_gen.go` |
| blockentities | `block_entities.json` | `level/block/blockentity.go`, `blockentities.go` |
| registryid | `registries.json` | `data/registryid/*.go` |
| biome | `biomes.json` | `level/biome/list.go` |
| lang | `lang/*.json` | `data/lang/<locale>/<locale>.go` |
| packets | `packet_schema.json` + `packets.json` + `packet_phases.json` | `protocol/<state>/{clientbound,serverbound}_gen.go` (+ round-trip tests), `protocol/types/{enums,structs}_gen.go` |

Everything else in the library is copied from `src/` as is. A file the generators write is never
in `src/`; `import-src` recognises them by their `Code generated … DO NOT EDIT` header.

### Packet structs

Every fully typed packet of `packet_schema.json` (a typed codec tree read from the jar's bytecode:
`struct` with named fields, `prim`, `string`, `list`, `optional`, `map`, `enum`, `registry`,
`holder`, `holderset`, `resourcekey`, `nbt`, `unit`) becomes a struct with `PacketID()`, `ReadFrom`
and `WriteTo` under `protocol/<state>/`. A packet named the same in both flows gets a
`Clientbound`/`Serverbound` prefix. Enums and the shared wire structures land in `protocol/types`;
their primitives (`List`, `Map`, `Holder`, `ItemStack`, `Text` = `chat.Message`, `NBT`, …) are
hand-written in `src/protocol/types/types.go`. The header of every generated file lists the packets
that were skipped and why (`opaque`, `dispatch`, a branch-guarded reader); those few are
hand-written in `src/protocol/*/hand.go`, and `src/protocol/handshake` is hand-written because the
handshake state has no packetid constants. `schemacov <version>` prints the coverage and the holes.

```go
var sh play.SetHealth
if err := p.Scan(&sh); err != nil { … }          // p is the pk.Packet a handler received

use := play.UseItem{Hand: types.InteractionHandMainHand}
err := conn.WritePacket(pk.Marshal(use.PacketID(), use))
```

`bot/`, `server/` and the examples use these structs only; after a version bump the compiler
points at every field a packet gained or lost.

## A new Minecraft version

```bash
go run ./gen/cmd/mcmeta diff 26.2 26.3          # 0. preview: registries, block states, item components (no Java)
go run ./gen/cmd/mc26 extract --version 26.3    # 1. temp/data/26.3
go run ./gen/cmd/packetdiff 26.2 26.3           # 2. wire changes the hand-written code must follow
go run ./gen/cmd/schemacov 26.3                 # 3. packets the generator cannot type yet → hand rule or hand.go
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
