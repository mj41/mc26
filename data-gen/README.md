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
| `GenItems.java` | `items.json` — per-item stack size and name | runtime registry |
| `GenPacketSchema.java` | `packet_schema.json` — typed wire layout of every packet, shared structure and data component | `java.lang.classfile` over the stream codecs |
| `GenEntityData.java` | `entity_data.json` — the entity metadata serializers with their wire form and the synched fields (index, serializer) of every entity type | reflection after bootstrap + `GenPacketSchema`'s interpreter over `EntityDataSerializers` |
| `GenConstants.java` | `constants.json` — the compile-time constants of a few classes (inventory slot layout, section geometry, level limits, NBT keys) | reflection |
| `GenNbtSchema.java` | `nbt_schema.json` — NBT shape of the registries sent in the configuration phase and of the chat style, events and decoration | `java.lang.classfile` over the DataFixerUpper codecs (shares the class access and JSON helpers of `GenPacketSchema`) |

Container layout: `/cache` (server jars), `/jsons/<version>` (output), `/java` (this
directory, read-only). Minecraft 26.1 and later only.

While working on one extractor, `mc26 extract --version 26.2 --only GenNbtSchema` recompiles
all of them but runs just the named ones into the existing `temp/data/26.2` (no downloads, no
data generator): about fifteen seconds a round.
