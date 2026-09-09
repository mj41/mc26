# mc26 documentation

| page | what it answers |
|---|---|
| [architecture.md](architecture.md) | how a Mojang jar becomes a tagged Go library, and which repository holds what |
| [generated-vs-hand-written.md](generated-vs-hand-written.md) | how much of the library is generated, how much is written by hand, and where the hand-written part came from |
| [hand-written.md](hand-written.md) | what is written by hand and why, package by package, in three tiers: the wire base every binding has, the application code, the harness's tests — and what another language takes from the JSON versus writes itself |
| [new-version.md](new-version.md) | what to do when Mojang ships a new Minecraft version |
| [testing.md](testing.md) | unit tests, the smoke test, the end-to-end scenarios, the cross-language check, and how to add one |
| [release.md](release.md) | branches, tags, the release workflow and its secret |
| [minecraft-data.md](minecraft-data.md) | the one hand-written description of the protocol this one is compared with, what the comparison found, and why the names differ |
| [../gen/README.md](../gen/README.md) | the `gen/` tree: commands, generators, packet structs, overlays |
| [../data-gen/README.md](../data-gen/README.md) | the Java extractors and what each writes |
| [../gen/hand-crafted/hand-crafted.md](../gen/hand-crafted/hand-crafted.md) | the inputs no Mojang output provides |
