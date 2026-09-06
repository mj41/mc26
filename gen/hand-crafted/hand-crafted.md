# Hand-crafted generator inputs

Files in this directory are maintained by hand and read by the generators. Each one covers
something no Mojang output provides: a Go naming choice, or a decision about what stays
hand-written. They are small on purpose; whenever a jar-derived source of truth appears, the
entry moves out of here.

### naming_overrides.json

- `component_names`: Go type names for data components whose derived name would be wrong
  (`map_id` → `MapID`, not `MapId`; `tnt` → `Tnt`).
- `block_trim_prefix_types`: block-state property enums whose Go constants drop the type-name
  prefix (`Direction` → `Down`, `Up`).
- `field_names`: the field names of a packet or shared structure whose reader gives the walker
  nothing to name the values by (`ClientboundCustomQueryPacket` reads three values into a
  two-argument constructor; `BlockHitResult` folds three floats into a vector before its
  constructor sees them), keyed by the Java short class name, in wire order; applied only when
  the count matches.
- `type_names`: Go type names for packet-side structures named after the class whose codec
  built them rather than after what they carry (`ParticleTypes` → `ParticleOptions`).
- `nbt_type_names`: Go type names for the NBT-shaped structures of `nbt_schema.json` whose Java
  short name would read badly (`Style$Serializer` → `Style`, `ChatTypeDecoration` →
  `Decoration`); every other name is derived from the Java class.

### packet_phases.json

The protocol states in generation order with the prefix their packet-id constants get
(`Config` for `configuration`, none for `play`, …) and a comment for the generated file.

### hand_packets.json

Packets implemented by hand in `src/protocol/<state>/hand.go`, keyed
`<state>/<flow>/<packet name>` with the reason. Empty today: the last hand-written packets
(custom payloads and queries, the chunk, the signed chat message, the player info update) are
generated since the walker learned rest-of-packet payloads, length-prefixed buffers, entries
guarded by an action bit set and leaf types kept by hand elsewhere (`externalHandTypes` in
`gen_packets.go`: the signed-chat structures of `chat/sign`, the block entity of `level`). The
generator still honours entries here — a packet listed is skipped regardless of the schema's
coverage, and reported when the schema types it fully — so a packet whose shape changes faster
than the walker can be parked here with a hand-written struct.

### hand_components.json

The data components implemented by hand in `src/level/component` (today: codecs the schema
cannot type — a dispatch on an enum or a recursive codec), keyed by registry name with the
reason. The components generator skips them regardless of coverage and says so when a newer
schema types one fully. Every other component is generated from the `components` section of
`packet_schema.json` (the stream codec of its `DataComponents` registration).

## Adding or retiring an entry

A new hand-written packet or component: implement it in `hand.go` or a file of
`level/component`, add the key here with the reason, build. Retiring one: delete both. What
is currently skipped and why is listed in the generated files themselves (`skipped_gen.go`
for components, the header comment of each `*_gen.go` packet file).
