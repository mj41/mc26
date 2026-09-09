# The same protocol, described by hand: PrismarineJS/minecraft-data

Everything in this repository is derived: the packet schema from the bytecode of Mojang's
unobfuscated jars, the names from Mojang's own registry ids and parameter names, the
verification from real sessions and a reader written from the JSON alone. That has been possible
since Minecraft 26.1, when the jars started shipping with their names. Before that, the only
protocol description anyone had was written by hand, packet by packet, version after version,
from decompiled and deobfuscated code, and the largest and longest-lived of those is
[PrismarineJS/minecraft-data](https://github.com/PrismarineJS/minecraft-data): the ProtoDef
definitions (`data/pc/<version>/protocol.json`) that node-minecraft-protocol, mineflayer and
their Python ports read. Years of that work is what made writing a Minecraft client outside Java
possible at all, and this project owes it the same debt every other one does. Thank you.

This page is where that description and ours are compared. It is the only place in this
repository that mentions the other: the project does not carry the history of reverse
engineering with it, since it does not need to, and nothing here depends on it.

## The comparison

```bash
git -C <minecraft-data> fetch origin
git -C <minecraft-data> show origin/master:data/pc/26.1/protocol.json > /tmp/protocol-26.1.json
go run ./gen/cmd/protodefdiff 26.1 /tmp/protocol-26.1.json
```

`protodefdiff` flattens both descriptions of a version to the natives of the schema in wire
order — `varint`, `i32be`, `string`, … with markers for a count, an option, a switch and a loop —
and compares per state, direction and packet: the id tables first (an id one side lacks, or a
different packet at an id, is a real difference), then every packet both sides have. Names are
not compared, since the two vocabularies differ on purpose (below); a switch's cases are
counted, not expanded.

### Minecraft 26.1, on 2026-09-09

Against their master of 2026-09-08. The id tables agree on every one of the 256 packets
(theirs carries one more, id 254 in the handshake, the legacy server-list ping, which is not a
packet of the current protocol). 198 packets flatten the same. Of the 58 that differ, most are
representation: their `switch` over an earlier field is our `when`; their `Slot` is our item
stack with its component patch expanded; a byte we call unsigned they call signed; two ints
where we pack a chunk position into one long; their `award_stats` reads three var ints where the
middle one is, on our side, a dispatch on the stat type — the same bytes.

The rest look like errors in the hand-written definition. Ours is read from the bytecode, and the
packets a session carries are verified byte for byte:

| packet (our name / theirs) | minecraft-data 26.1 | the jar |
|---|---|---|
| `teleport_entity` / `entity_teleport` | id, x y z as doubles, two rotation bytes, on-ground | id, the whole `PositionMoveRotation` (position, delta movement, two rotation floats), the relatives int, on-ground — the layout since 1.21.2 |
| `move_minecart_along_track` / `move_minecart` | nine floats per step | two `Vec3` of doubles, two rotation bytes, one float |
| `test_instance_block_status`, `test_instance_block_action` | three `i32` for the size | three var ints |
| `custom_click_action` (configuration and play) | a boolean, then NBT | a length prefix, then an optional tag with no boolean |
| `show_dialog` in the configuration state | plain NBT | a holder: a var int (id + 1, 0 = inline) then the NBT, as their own play-state definition has it |
| `initialize_border`, `set_border_lerp_size`, `section_blocks_update`, `set_structure_block` | `varint` | `varlong` (the same bytes below 2^31, so nothing breaks today) |
| `set_creative_mode_slot` | a two-case switch on the count | the untrusted item stack, whose component patch is length-delimited — to be checked against a session |

Reporting these upstream, one issue per packet with the bytecode as evidence
(`go run ./gen/cmd/javap 26.1 <class>`), is the useful thing to do with the list.

## Names

The two vocabularies differ, and ours will not move toward theirs. Every name here is derived:
packets are Mojang's registry ids (`minecraft:teleport_entity`, `level_chunk_with_light`), fields
are the parameter and local names Mojang ships, types are the Java classes. The last hand-kept
naming table left this repository on 2026-09-08, and a new Minecraft version renames nothing by
hand. Their names (`spawn_entity`, `map_chunk`, `window_items`, `block_dig`) come from the years
before the jars had names, kept stable for the people who use them; that stability is their
value, and it also let `entity_teleport` keep its name while its layout changed. The Minecraft
wiki and Mojang's own data generator use Mojang's names now.

Where the two meet, an alias table computed by matching packet ids per version (all 256 of
26.1 match) is enough: an export to their format would carry their names, and packets.md in the
data repository can show the alias next to ours. No hand list.

## Exports

Their format, and Kaitai Struct's, are the two schema languages with an ecosystem around them.
An export from our schema to either would give those ecosystems definitions derived from the jar
instead of written by hand, and give this project a further independent consumer of the
description, checked the way the JSON-only reader is. Most of our node kinds map one to one; the
work is in a dispatch on a registry (their switch is over a mapper of the id, which needs the
registry's names inlined), `packed` and `rest` (natives they do not have), and `guard` (their
switch over `../field`). Kaitai would be the read direction only. Whether to do it is in the
backlog (`wip/exports.md`); nothing is visible to anyone until the data repository is public.
