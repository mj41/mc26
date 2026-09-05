# data-gen — Java extractors

Java programs that read Mojang's unobfuscated Minecraft 26.x server jar and write the JSON
published in [mc26-data](https://github.com/mj41/mc26-data). They run inside a JDK 25 container
started by `mc26 extract` (see `../gen`); the host only downloads the jar and the language files.

| file | writes | how |
|---|---|---|
| `ExtractAll.java` | orchestrates: runs the data generator (`--all` reports: `registries.json`, `blocks.json`, `packets.json`, `commands.json`, `datapack.json`), copies `version.json` from the inner jar, compiles and runs the extractors below; any failure is fatal | `java --source 21 ExtractAll.java <version>` |
| `GenEntities.java` | `entities.json` — entity types with dimensions | registry + reflection |
| `GenComponents.java` | `components.json` — data component types, networkable flag | registry |
| `GenComponentSchema.java` | `component_schema.json` — wire shape of every component | reflection over the codecs |
| `GenBlockEntities.java` | `block_entities.json` — block entity types and their blocks | registry |
| `GenBlockProperties.java` | `block_properties.json` — block state property definitions | reflection |
| `GenBiomes.java` | `biomes.json` — biome ids in network order | runtime registry |
| `GenItems.java` | `items.json` — per-item stack size and name | runtime registry |
| `GenPacketSchema.java` | `packet_schema.json` — typed wire layout of every packet and shared structure | `java.lang.classfile` over the packet codecs |

Container layout: `/cache` (server jars), `/jsons/<version>` (output), `/java` (this
directory, read-only). Minecraft 26.1 and later only.
