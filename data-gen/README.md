# data-gen — Java extractors

Java programs that read Mojang's unobfuscated Minecraft 26.x server jar and write the JSON
published in [mc26-data](https://github.com/mj41/mc26-data). They run inside a JDK 25 container
started by `mc26 extract` (see `../gen`); the host only downloads the jar and the language files.

| file | writes | how |
|---|---|---|
| `ExtractAll.java` | orchestrates: runs the data generator (`--all` reports: `registries.json`, `blocks.json`, `packets.json`, `commands.json`, `datapack.json`), copies `version.json` from the inner jar, compiles and runs the extractors below; any failure is fatal | `java --source 21 ExtractAll.java <version>` |
| `GenEntities.java` | `entities.json` — entity types with dimensions | registry + reflection |
| `GenComponents.java` | `components.json` — data component types, networkable flag | registry |
| `GenBlockEntities.java` | `block_entities.json` — block entity types and their blocks | registry |
| `GenBlockProperties.java` | `block_properties.json` — block state property definitions | reflection |
| `GenBiomes.java` | `biomes.json` — biome ids in network order | runtime registry |
| (Mojang's data generator) | `data/` — the vanilla data pack its `--all` run writes beside the reports (recipes, loot tables, tags, advancements, worldgen, trades), copied as files | the data generator |
| `GenItems.java` | `items.json` — per-item stack size and name, and every default component as `wire`: a DataComponentPatch in its network encoding, base64 (a component naming a data-pack registry left out and listed in `wire_skipped`) | runtime registry, the components' own stream codecs |
| `GenPacketSchema.java` | `packet_schema.json` — typed wire layout of every packet, shared structure and data component | `java.lang.classfile` over the stream codecs |
| `GenEntityData.java` | `entity_data.json` — the entity metadata serializers with their wire form and the synched fields (index, serializer) of every entity type | reflection after bootstrap + `GenPacketSchema`'s interpreter over `EntityDataSerializers` |
| `GenConstants.java` | `constants.json` — the compile-time constants of a few classes (inventory slot layout, section geometry, level limits, NBT keys, the input and movement packet flags) | reflection |
| `GenBlockBehaviour.java` | `block_behaviour.json` — per state the collision and outline shapes (a shared table of distinct shapes), destroy speed, the right-tool flag, replaceable, light, the sturdy faces, the map colour, the fluid (with its falling flag); per block destroy time, explosion resistance, friction, speed and jump factors, dynamic and offset shapes | reflection after bootstrap: the game's own `getCollisionShape`, `getShape`, … with an empty getter at the origin |
| `GenComponentHashes.java` | `component_hashes.json` — sample stacks as a give command writes them (every kind of component codec: values, records, lists, texts, holders, nested stacks), parsed by the game's `ItemParser`; per component its network encoding (base64) and the hash a vanilla client sends for it in a container click; the names of the data-driven registries the holders were written with | the game's own stream codecs and `HashOps.CRC32C_INSTANCE` over a registry access of the vanilla registries |
| (Mojang's data generator) | `json-rpc-api-schema.json` — the OpenRPC document of the server management protocol, copied from the `--all` reports | Mojang's own `JsonRpcApiSchema` provider |
| `GenNbtSchema.java` | `nbt_schema.json` — NBT shape of the registries sent in the configuration phase, of the chat style, events and decoration, of the codec-built save-format records (world options, dimensions down to the density functions and surface rules) and of the text component itself (`ComponentSerialization.CODEC`, walked in full: recursive, with its legacy dispatch on `type`); every registry and shared type is described in 26.1, 26.2 and 26.3-pre-2, with no opaque node and no caseless dispatch | `java.lang.classfile` over the DataFixerUpper codecs (shares the class access and JSON helpers of `GenPacketSchema`); registry dispatch cases from the bootstrap classes, a static map's entries by reflection |
| `GenSaveSchema.java` | `save_schema.json` — NBT shape of the save formats, which have no codec: a saved chunk's keys with their tag types, defaults and nesting, read from `SerializableChunkData.parse` and `write` together, down to the palettes of a section; the entity region file; every entity type's NBT and a player's file and `level.dat`, from `Entity.load` and `Entity.save` walked with the type's own class resolving the virtual calls, so each class of the hierarchy adds its keys | `java.lang.classfile` over the reader's keyed accessors (`getIntOr`, `getListOrEmpty`, `read(key, CODEC)`), and the writer's (`putInt`, `store`), with every codec it meets handed to `GenNbtSchema` — including one a record component holds (followed to where the record is built) and one a lambda captures (which describes the compound it is mapped over). A helper handed the tag is followed into, so what it writes is part of the format (`NbtUtils.addCurrentDataVersion` puts `DataVersion` there) |

Container layout: `/cache` (server jars), `/jsons/<version>` (output), `/java` (this
directory, read-only). Minecraft 26.1 and later only.

While working on one extractor, `mc26 extract --version 26.2 --only GenNbtSchema` recompiles
all of them but runs just the named ones into the existing `temp/data/26.2` (no downloads, no
data generator): about fifteen seconds a round.
