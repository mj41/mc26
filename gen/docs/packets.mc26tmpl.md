# Packets

<!-- This file is a template: gen/docs/packets.mc26tmpl.md, rendered by `mc26 docs`. -->

Every packet of Minecraft <!-- mc26 value: id --> (protocol <!-- mc26 value: protocol -->), by
connection state and direction, with its id in that state's table and its fields as
`packet_schema.json` describes them. The node kinds and the primitives are explained in
[protocol.md](protocol.md); the frame around a packet, and what changes the state, are there too.

A field's line is its name and what it is on the wire; a field that is only sometimes present
says when. A `struct` is its fields in order; a `dispatch` is its key, then the payload of the
case the key selects; `list of` is a var int count then that many elements; `optional` a
boolean then the value when the boolean is set. Ids are indexes into a dense table per state
and direction, so a packet added in a later version moves every id after it.

<!-- mc26 include: packets -->
