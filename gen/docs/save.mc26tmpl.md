# Save formats

<!-- This file is a template: gen/docs/save.mc26tmpl.md, rendered by `mc26 docs`. -->

What a world of Minecraft <!-- mc26 value: id --> keeps on disk, as `save_schema.json` describes
it. These formats have no codec: Mojang reads them key by key and writes them the same way, so
the description is read from the reader and the writer themselves, every key with the tag type
the accessor implies, whether it may be absent, and the default used when it is. A part of a
format that does have a codec (the blending data of a chunk, its tick lists, its palettes) is
described through that codec, in full.

The shapes read as on the registries page: a compound is a list of keys and what each holds, `?`
marks a key that may be absent, `id in <registry>` is a namespaced id as a string, `either` is
whichever side the tag fits, `list` a TAG_List. Where a list's elements would not all have the
same tag type, NBT writes every element as a compound and wraps a bare value under the empty
key, which is how a block-state palette holds a bare block id beside a state with properties.

## The container

A dimension's chunks are in region files, `r.<x>.<z>.mca` under its `region/` directory, one per
32×32 chunks; `entities/` and `poi/` use the same container. `nodes.json` carries it as data
(`region`): the file is a whole number of 4 KiB sectors; the first holds 1024 four-byte locations
indexed `x + z * 32` (the high three bytes the chunk's first sector, 0 when the chunk is absent,
the low byte how many sectors it spans), the second 1024 four-byte timestamps; a chunk is a
four-byte big-endian length counting what follows, a compression byte (1 gzip, 2 zlib, 3 none, 4
lz4; bit 0x80 set means the data is in `c.<x>.<z>.mcc` next to the file), then the NBT: one
named root compound with an empty name, unlike the network form, which has no name.

## Formats

`chunk` is a saved chunk of the `region/` files, `entities` the chunk of an `entities/` region
file, whose `Entities` list holds one compound per entity of the type its `id` names —
described by `entity/<id>`, one format per entity type, from the save chain of the type's
class: `Entity.load` and `Entity.save` read the keys every entity has, then each class of the
hierarchy adds its own, and a helper the tag is handed to is followed, an interface's default
method included (a villager's `Inventory`). `entity` is that common part alone. `player` is a
file under `players/data/`, the server-side player read like an entity and written by
`saveWithoutId`, the writer `PlayerDataStorage` calls: the file carries no `id`. `level` is
`level.dat`: the tag `LevelStorageAccess.saveDataTag` builds, whose `Data` is what
`PrimaryLevelData.createTag` writes — a compound a helper builds and returns is followed like
one it is handed, a list it fills is typed by what it adds to it, and a map codec stored
without a key puts its keys at that level (the data packs and the enabled features). Its
reader takes the `Dynamic` API and reads nothing the writer does not write. The world options
and the dimensions are not in `level.dat` since 26.1: they are a saved-data file of their own
(`WorldGenSettings`, below). A scalar's tag is the one the writer writes: a reader's `getIntOr`
accepts any numeric tag, and `Air` is a short on disk.

<!-- mc26 include: save-formats -->

## The records of a world that have a codec

The world options and the dimensions with their generators (`data/minecraft/world_gen_settings.dat`
since 26.1), the respawn point and the data-pack lists of `level.dat` are codec-built records;
they are described on the registries page under shared types and generated into the same Go
package as the formats above, which refer to them.

<!-- mc26 version: >= 26.1 -->
The saved chunk and the light-only sections: a chunk on disk carries one section below the
world and one above it that hold light and nothing else — no `block_states`, no `biomes` — and
Mojang's reader skips whatever falls outside the level's height. The block-state palette of a
section changed in 26.3: an entry with no properties is written as the bare block id, and only a
state with properties as the compound of `id` and `properties`; until 26.2 every entry was the
compound, under the keys `Name` and `Properties`.
<!-- mc26 end -->
