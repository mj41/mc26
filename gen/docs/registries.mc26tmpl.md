# Registry elements and shared NBT types

<!-- This file is a template: gen/docs/registries.mc26tmpl.md, rendered by `mc26 docs`. -->

The registries a server of Minecraft <!-- mc26 value: id --> sends its clients in the
configuration state (`registry_data`), with the NBT shape of each element as `nbt_schema.json`
describes it, and the shared types the game reads the same way: the text component, the chat
style and events, and the codec-built records of a world save.

A shape is a compound: each line is a key and what its tag holds. A key marked `?` is optional,
with its default when the codec has one. A `dispatch` is a compound whose named key selects
one of the cases, each adding its own keys next to it. `id in <registry>` is a string, the
element's namespaced id; `id in <registry> or inline` may instead be the element itself as a
compound. `either` is whichever side decodes; `list` a TAG_List; a map a compound with
arbitrary keys. A name followed by `again` is the enclosing type of that name: the codec
contains itself.

## Registries

<!-- mc26 include: registries -->

## Shared types

<!-- mc26 include: types -->
