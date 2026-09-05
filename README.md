# mc26

The code that turns Mojang's unobfuscated Minecraft 26.x server jars into a generated Go
library, with one branch per Minecraft version in the data and library repositories:

    Mojang jar ──► data-gen/ ──┬─► mc26-data      (releases)       ──► gen/ ──► go-mc26 ──► go-mc26-examples
                               └─► mc26-data-pre  (snapshots, pre-releases)

| directory / repository | what |
|---|---|
| `data-gen/` | Java extractors + Go launcher: jar → JSON (`java.lang.classfile` reads the packet and component wire schemas) |
| `gen/` | Go generators, the hand-written framework sources, templates, the `build` command, `cmd/packetdiff`, `cmd/mcmeta`, `cmd/schemacov` |
| [mc26-data](../mc26-data) | the JSON of every release, `mc-<version>` branches, `v0.<YYN>.<n>` tags |
| [mc26-data-pre](../mc26-data-pre) | the same for snapshots and pre-releases |
| [go-mc26](../go-mc26) | the generated library, `v0.<YYN>.<patch>` tags |
| [go-mc26-examples](../go-mc26-examples) | examples and bots on the library |

`pipeline.yml` runs extract → build → test for one Minecraft version; `release.yml` publishes a
data tag and a library tag from one job. Built with Claude Opus and Claude Fable. Carries code
from [Tnze/go-mc](https://github.com/Tnze/go-mc) (MIT); the `COPIED` manifest under `gen/` lists
every such file.
