# Hand-crafted generator inputs

Files in this directory are maintained by hand and read by the generators. Each one covers
something no Mojang output provides: a Go naming choice, or a decision about what stays
hand-written. They are small on purpose; whenever a jar-derived source of truth appears, the
entry moves out of here.

## What is not here any more

`naming_overrides.json` — Go names chosen by hand where the derived one read badly
(`ChatTypeDecoration` → `Decoration`, `serversettings` → `ServerSettings`), field names for
readers the walker could not name, block-state constants without their type's prefix
(`Down` for `DirectionDown`) — is gone (2026-09-08). Every name is now derived from the
schema, the registry name or the Java class by rules that hold for every entry (`_id` →
`ID`, `ip` → `IP`), the field names come from the bytecode itself (see `struct` in
nodes.json), and a Go constant always carries its type's name.

`packet_phases.json` — the protocol states with the prefix their packet-id constants got
(`Config` for `configuration`, none for `play`) — is gone (2026-09-08): the states come from
`packets.json`, in the order a connection goes through them, and every one prefixes its
constants with its own name (`ClientboundPlaySetHealth`, `ClientboundConfigurationKeepAlive`,
`ServerboundHandshakeIntention`), which also gave the handshake state its constants and
retired the hand-written `protocol/handshake`.

`hand_packets.json` and `hand_components.json` — the lists of packets and data components
kept by hand because the schema could not type them — are gone (2026-09-08): every packet and
component of 26.1, 26.2 and 26.3-pre-2 is generated, and a schema that cannot type one is
reported in the generated files themselves (`skipped_gen.go` for components, the header
comment of each `*_gen.go` packet file) rather than papered over by a hand-written type. The
leaf types other packages implement with the same wire form (`externalHandTypes` in
`gen_packets.go`: the signed-chat structures of `chat/sign`, the block entity of `level`) are
the last hand-kept pieces of a packet.
