# mc26

The code that turns Mojang's unobfuscated Minecraft 26.x server jars into a generated Go library,
with one branch per Minecraft version in the data and library repositories:

```
Mojang jar ──► data-gen/ ──┬─► mc26-data      (releases)                ──► gen/ ──► go-mc26 ──► go-mc26-examples
                           └─► mc26-data-pre  (snapshots, pre-releases)
```

| directory / repository | what |
|---|---|
| `data-gen/` | Java extractors: jar → JSON (`java.lang.classfile` reads the packet and component wire schemas) |
| `gen/` | Go: the `mc26` command (extract, build, smoke, pipeline, release), the generators, the hand-written library sources (`gen/src`), templates, `cmd/packetdiff`, `cmd/mcmeta`, `cmd/schemacov` |
| [mc26-data](https://github.com/mj41/mc26-data) | the JSON of every release: `mc-<version>` branches, `v0.<YYN>.<n>` tags |
| [mc26-data-pre](https://github.com/mj41/mc26-data-pre) | the same for snapshots and pre-releases |
| [go-mc26](https://github.com/mj41/go-mc26) | the generated library: `mc-<version>` branches, `v0.<YYN>.<patch>` tags |
| [go-mc26-examples](https://github.com/mj41/go-mc26-examples) | examples and bots on the library |

## The flow, locally

Requirements: Go, podman or docker (the extractors run in `eclipse-temurin:25-jdk`), Java 25 on
the host for the smoke test. Everything below writes only under `temp/`.

```bash
go run ./gen/cmd/mc26 pipeline --version 26.2 --smoke   # extract → build → vet/test → vanilla-server smoke
go run ./gen/cmd/mc26 extract  --version 26.2           # only the JSON: temp/data/26.2 (+ _meta.json)
go run ./gen/cmd/mc26 build    --data 26.2              # only the library: temp/lib/26.2
go run ./gen/cmd/mc26 smoke    --version 26.2           # only the smoke test against temp/lib/26.2
go run ./gen/cmd/schemacov 26.2                         # how much of the packet schema is typed
go run ./gen/cmd/packetdiff 26.1 26.2                   # wire-layout changes between two versions
go run ./gen/cmd/mcmeta diff 26.2 26.3-pre-2            # registry preview without Java (misode/mcmeta)
```

A release is the same chain plus commits and tags in the sibling checkouts (`../mc26-data`,
`../mc26-data-pre`, `../go-mc26`); nothing is pushed without `--push`:

```bash
go run ./gen/cmd/mc26 release --version 26.2            # branches mc-26.2, tags v0.262.<n> / v0.262.<patch>
go run ./gen/cmd/mc26 release --version 26.2 --push
```

## The flow, in GitHub Actions

- `pipeline` (`.github/workflows/pipeline.yml`): every pull request, and on demand for any
  version — the local `pipeline --smoke`, plus the coverage report and the artifacts.
- `release` (`.github/workflows/release.yml`): on demand with a version — the local `release
  --push`, using the `MC26_PUSH_TOKEN` secret (contents: write on the three target repositories).
  Tags are created only here.

Versions: Minecraft 26.1 and later, release ids and pre-release ids alike (`26.3-pre-2` goes to
`mc26-data-pre` and tags as `v0.263.0-pre2.<n>`). The three newest Minecraft versions receive
fixes; older branches are frozen.

Built with Claude Opus and Claude Fable. Carries code from
[Tnze/go-mc](https://github.com/Tnze/go-mc) (MIT); `gen/src/COPIED` lists the origin of every
hand-written library file.
