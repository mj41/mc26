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
| `gen/` | Go: the `mc26` command (extract, build, smoke, e2e, crosscheck, pipeline, release), the generators, the hand-written library sources (`gen/src`), templates, `cmd/packetdiff`, `cmd/mcmeta`, `cmd/schemacov` |
| [mc26-data](https://github.com/mj41/mc26-data) | the JSON of every release: `mc-<version>` branches, `v0.<YYN>.<n>` tags |
| [mc26-data-pre](https://github.com/mj41/mc26-data-pre) | the same for snapshots and pre-releases |
| [go-mc26](https://github.com/mj41/go-mc26) | the generated library: `mc-<version>` branches, `v0.<YYN>.<patch>` tags |
| [go-mc26-examples](https://github.com/mj41/go-mc26-examples) | examples and bots on the library |

## The flow, locally

Requirements: Go and podman or docker — the extractors and the vanilla test server both run in
`eclipse-temurin:25-jdk` (`--runtime host` runs the server on the host's Java 25 instead).
Everything below writes only under `temp/`.

```bash
go run ./gen/cmd/mc26 pipeline --version 26.2 --smoke --e2e   # extract → build → vet/test → smoke → example bots
go run ./gen/cmd/mc26 extract  --version 26.2           # only the JSON: temp/data/26.2 (+ _meta.json)
go run ./gen/cmd/mc26 build    --data 26.2              # only the library: temp/lib/26.2
go run ./gen/cmd/mc26 smoke    --version 26.2           # the library's smoke test against a vanilla server
go run ./gen/cmd/mc26 e2e      --version 26.2           # the bots of ../go-mc26-examples (its mc-26.2 branch) against a vanilla server
go run ./gen/cmd/mc26 crosscheck --version 26.2         # a recorded session read back from the JSON alone, in another language
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
  version — the local `pipeline --smoke --e2e` (the examples checked out at their `mc-<version>`
  branch), plus the coverage report and the artifacts.
- `release` (`.github/workflows/release.yml`): on demand with a version — the local `release
  --e2e --push`, using the `MC26_PUSH_TOKEN` secret (contents: write on the three target
  repositories). Tags are created only here.

The smoke test joins the server, waits for chunks and its own chat echo. The end-to-end run
builds every example against the built library and checks: `mcping` reports the version;
`daze` sees a broadcast, a private message and an item given over RCON while chunks stream in;
two `daze` bots hear each other's chat (typed on their consoles), and one of them, made an
operator, teleports itself to the other (checked with `data get entity … Pos`) and gives it a
diamond the other bot sees in its inventory; `minimal` and `autofish` log in; `pressureTest`
logs three bots in; `mcadump` reads a region file the server wrote.

Versions: Minecraft 26.1 and later, release ids and pre-release ids alike (`26.3-pre-2` goes to
`mc26-data-pre` and tags as `v0.263.0-pre2.<n>`). The three newest Minecraft versions receive
fixes; older branches are frozen.

## Docs

[docs/](docs/README.md): [architecture](docs/architecture.md), [generated versus
hand-written](docs/generated-vs-hand-written.md), [what is still hand-written and why](docs/hand-written.md), [a new Minecraft version](docs/new-version.md),
[testing](docs/testing.md), [releasing](docs/release.md); [gen/README.md](gen/README.md) for the
commands, generators and packet structs; [data-gen/README.md](data-gen/README.md) for the extractors.
[wip/](wip/README.md) is the backlog: one task per file, in order, with the decisions that are
still open and the records of what was done.

Built with Claude Opus and Claude Fable. Carries code from
[Tnze/go-mc](https://github.com/Tnze/go-mc) (MIT); `gen/src/COPIED` lists the origin of every
hand-written library file — `go run ./gen/cmd/mc26 report --version 26.2` measures the split.
