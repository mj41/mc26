# The work around generation, automated

Status: **done** 2026-09-09 (commits `1240d1f`, `85f0a8c`, `ca094be`).

Generation was finished as far as the JSON honestly describes ([generation.md](generation.md));
what was left was the work around it, done by hand on every commit for three days.

| task | done |
|---|---|
| `mc26 verify` | build, smoke, crosscheck and e2e for every extracted version, one step at a time under the memory limits, one log per step under `temp/verify/`, one table; six minutes for three versions with the jars cached. Replaced the shell script every commit was checked with. |
| the schemas checked by every build | `gen/internal/schemacheck`: no partial entry, opaque node, caseless dispatch, unnamed case or dangling ref in either schema; `build` stops on one (`--allow-holes` to go on while the extractor is fixed), `report` prints the result. 381 / 383 / 399 / 400 entries described in 26.1 / 26.2 / 26.3-pre-2 / 26.3-pre-3. |
| `mc26 update` | manifest → extract → schema check → `packetdiff` and `nbtdiff` against the previous version → strict build → verify of every extracted version → data and library commits and tags, never a push; stops where a person is needed and says what to do. `docs/new-version.md` is written around it. |
| `nbtdiff` | what the registry elements and shared types gained, lost or changed between two versions, every union once by case; 26.2 → 26.3-pre-2 reads as the world-gen refactor it is. |
| `javap` | already existed (`gen/cmd/javap`, 2026-09-06): a class of a cached jar disassembled in the JDK container. |
| documentation from templates | 2026-09-09: what a node kind and the frame are on the wire is Markdown (`gen/docs/*.mc26tmpl.md`, readable as it is; directives are HTML comments: a block for some versions, a block for this repository only, a generated table or tree, a fact inline). `mc26 docs --version V` renders four documents per version (protocol, packets, components, registries) and `release` puts them in the data branch as `docs/`; the build checks every node kind has its section. The prose is out of `nodes.json`; `prims.json` keeps its short notes, rendered into the primitive table. |
| the cross-language reader in Go | 2026-09-09: `gen/crosslang` is a Go module of its own (no import of the library or the generators), the port of `decode.py`; the same three sessions round-trip byte for byte in both, and `crosscheck` runs it. |

Rehearsal: 26.3-pre-3 appeared in Mojang's manifest on 2026-09-09 and went through `update`
untouched ([release-26-3.md](release-26-3.md)).
