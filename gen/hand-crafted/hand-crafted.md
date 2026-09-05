# Hand-Crafted Generator Inputs

This directory contains manually maintained data files that serve as inputs
to the Go code generators in `tools/`. These files describe information that
cannot be derived from Minecraft's built-in data generator output.

## Why Hand-Crafted?

Mojang's data generator (`--all`) produces structured JSON for registries,
blocks, packets, etc. — our generators consume those directly from
`temp/jsons/<version>/`. However, some data has no machine-readable source:

- **Component wire formats** — field types, serialization order, and NBT
  wrapping are not in any Mojang output
- **Go naming overrides** — abbreviation choices (`MapID` not `MapId`)
  are language-specific conventions
- **Protocol phase naming** — Go name abbreviations (`Config` not
  `Configuration`) are style choices

These files bridge the gap between Mojang data and Go code generation.

---

## Files

### component_schema.json

Wire format schema for all data component types. Each entry describes how
a component is serialized on the wire, using one of these patterns:

| Pattern | Description | Generated Code |
|---------|-------------|----------------|
| `embed` | Struct embeds a single type | `type X struct { T }` — inherits R/W |
| `embed_nbt` | Embeds `dynbt.Value`, R/W via `pk.NBT` | Explicit R/W with `pk.NBT(&x.Value)` |
| `empty` | Unit type, no wire data | `struct{}`, no-op R/W |
| `delegate` | Single named field, R/W delegates | Field + forwarding R/W methods |
| `named_int` | Named `int32` type | Cast-based R/W via `(*pk.VarInt)` |
| `array` | Single array field | `[]T`, R/W via `pk.Array` |
| `tuple` | Multiple fields | `pk.Tuple{&f1, &f2, ...}` |
| `custom` | Complex, skip generation | Keep hand-written file |

**Delegate `serMethod` values:**
- `direct` — delegates to field's own `ReadFrom`/`WriteTo`
- `nbt` — wraps with `pk.NBT(&field)`
- `nbtfield` — wraps with `pk.NBTField{V: &field, AllowUnknownFields: true}`

**Tuple field type wrappers:**
- `pk.Array[T]` — field is `[]T`, tuple arg is `pk.Array(&field)`
- `pk.Option[T]` — field is `pk.Option[T, *T]`, tuple arg is `&field`
- `pk.NBTField[T]` — field is `T`, tuple arg is `pk.NBTField{V: &field, AllowUnknownFields: true}`

### naming_overrides.json

Go naming convention overrides that cannot be derived from MC data:

- **`component_names`** — maps snake_case component names (without `minecraft:`
  prefix) to Go type names when `snakeToCamel()` produces the wrong result.
  Example: `map_id` → `MapID` (Go treats "ID" as an initialism).
- **`block_trim_prefix_types`** — block property enum types whose Go constants
  omit the type-name prefix. Example: `Direction` → `Down`, `Up` (not
  `DirectionDown`, `DirectionUp`). Style preference only.

Used by: `gen_component.go`, `gen_component_types.go`, `gen_blocks.go`

### packet_phases.json

Protocol phase definitions in generation order. Each entry has:

- `name` — MC JSON key (e.g. `"configuration"`)
- `go_prefix` — Go constant name prefix (e.g. `"Config"`, or `""` for play)
- `comment` — Human-readable name for section comments

Used by: `gen_packetid.go`

---

## Maintenance

When adding support for a new Minecraft version:

1. **New components**: Add entries to `component_schema.json`. Run
   `go run ./tools/` — it will report any components in `components.json`
   that lack a schema entry.
2. **Naming issues**: If `snakeToCamel()` produces wrong Go names,
   add overrides to `naming_overrides.json`.
3. **New protocol phases**: Add entries to `packet_phases.json` (extremely
   rare — hasn't changed since configuration phase was added in 1.20.2).

## Data Source Reference

| File | Source | Maintained by |
|------|--------|---------------|
| `component_schema.json` | MC wiki / decompiled source | Hand-written |
| `naming_overrides.json` | Go style preferences | Hand-written |
| `packet_phases.json` | Protocol structure | Hand-written |

### hand_packets.json

The packets implemented by hand in `src/protocol/<state>/hand.go`, keyed
`<state>/<flow>/<packet name>` with the reason. The packets generator skips them
regardless of the schema's coverage; when a new Minecraft version's schema types one of
them fully, the generator says so, and the hand-written version can be retired on purpose
(remove it from `hand.go` and from this file) rather than colliding with a generated one.
