# Data components

<!-- This file is a template: gen/docs/components.mc26tmpl.md, rendered by `mc26 docs`. -->

The data components an item stack can carry in Minecraft <!-- mc26 value: id -->, with their wire
form as `packet_schema.json` describes it. An item stack on the wire is `ITEM_STACK`
([protocol.md](protocol.md), primitives): a count, then the item id, then a component patch —
counts of added and removed components, then each added component as its type's registry id
followed by the form below, then each removed component as an id. The order of a patch is the
encoder's; a reader must not assume it.

The component's registry id is `data_component_type` in `registries.json`; the table's names are
its entries.

<!-- mc26 include: components -->
