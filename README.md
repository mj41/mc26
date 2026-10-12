# mc26

The code that turns Mojang's unobfuscated Minecraft 26.x server jars into a generated Go library,
with one branch per Minecraft version in the data and library repositories.

**To use the library** (a bot, a server, a tool that reads worlds or packets), nothing here needs
to be built: [go-mc26](https://github.com/mj41/go-mc26) is published, one tag per Minecraft
version, and a bot starts in [go-mc26-kit](https://github.com/mj41/go-mc26-kit), whose README has
the first program.

```
go get github.com/mj41/go-mc26@v0.263       # Minecraft 26.3: the newest v0.263.x
go get github.com/mj41/go-mc26-kit          # the bot, the server framework, the account flows
```

This repository is for building the library: for a new Minecraft version, or a change to the
generators or to the hand-written sources.

```
Mojang jar ──► data-gen/ ──┬─► mc26-data      (releases)                ──► gen/ ──► go-mc26     (the library, one branch per version)
                           └─► mc26-data-pre  (snapshots, pre-releases)              ◄── go-mc26-kit (bot, server, accounts, examples: tested against every build)
```

| directory / repository | what |
|---|---|
| `data-gen/` | Java extractors: jar → JSON (`java.lang.classfile` reads the packet and component wire schemas) |
| `gen/` | Go: the `mc26` command (extract, build, smoke, e2e, crosscheck, verify, update, release), the generators, the hand-written library sources (`gen/src`), templates, `cmd/packetdiff`, `cmd/nbtdiff`, `cmd/mcmeta`, `cmd/schemacov`, `cmd/javap`, `cmd/source` |
| [mc26-data](https://github.com/mj41/mc26-data) | the JSON of every release: `mc-<version>` branches, `v0.<YYN>.<n>` tags |
| [mc26-data-pre](https://github.com/mj41/mc26-data-pre) | the same for snapshots and pre-releases |
| [go-mc26](https://github.com/mj41/go-mc26) | the generated library: `mc-<version>` branches, `v0.<YYN>.<patch>` tags |
| [go-mc26-kit](https://github.com/mj41/go-mc26-kit) | the bot, the server framework, the account flows and the examples: its own repository and history, tags of its own; builds against every supported library version, and the pipeline here tests every build with it (a checkout next to this one, `--kit-src`) |

## The flow, locally

Requirements:

- Go 1.25 or later, git, and the network (Mojang's downloads, the container image, Go modules);
- podman or docker: the extractors and the vanilla test server run in `eclipse-temurin:25-jdk`
  (`--runtime host` runs the server on the host's Java 25 instead);
- about 8 GB of free memory: the extraction container takes 6 GB;
- a checkout of go-mc26-kit next to this one. Every build also builds and tests the kit against
  the library it made, since the kit's bot is the test client:
  `git clone https://github.com/mj41/go-mc26-kit ../go-mc26-kit` (or `--kit-src DIR`).

The first build, a few minutes for the extraction and one for the build:

```bash
go run ./gen/cmd/mc26 extract --version 26.3     # the JSON: temp/data/26.3 (+ _meta.json)
go run ./gen/cmd/mc26 build   --data 26.3        # the library and the kit against it; the last line:
                                                 #   built 26.3 → <this checkout>/temp/lib/26.3
```

A program of your own uses that build with `replace github.com/mj41/go-mc26 => <this
checkout>/temp/lib/26.3` in its `go.mod`. The rest of the chain, and the tools:

```bash
go run ./gen/cmd/mc26 smoke    --version 26.3           # the kit's bot and the library's management client against a vanilla server
go run ./gen/cmd/mc26 e2e      --version 26.3           # the kit's example bots against a vanilla server (long: the robot and survival scenarios)
go run ./gen/cmd/mc26 pipeline --version 26.3 --smoke --e2e   # all of it: extract → build → vet/test → smoke → e2e
go run ./gen/cmd/mc26 fixtures --version 26.3           # the save tests' small world, cut from what e2e left
go run ./gen/cmd/mc26 crosscheck --version 26.3         # a recorded session read back from the JSON alone, in another language
go run ./gen/cmd/schemacov 26.3                         # how much of the packet schema is typed (reads temp/data/26.3)
go run ./gen/cmd/packetdiff 26.2 26.3                   # wire-layout changes between two versions (reads temp/data)
go run ./gen/cmd/source 26.3 LocalPlayer                # a vanilla class as readable Java under temp/source/ (downloads the client jar; decompiles in the container)
go run ./gen/cmd/mcmeta diff 26.2 26.3                  # registry preview without Java (misode/mcmeta, over the network)
```

`go run ./gen/cmd/mc26` lists every command, `mc26 <command> -h` its flags; the commands take
`--version`, the small tools take versions as arguments. Everything is written under `temp/`,
except two things: the first e2e run of a version cuts its fixture world into
`gen/src/save/testdata/<version>/` (the save tests' world, meant to be committed), and when a checkout of
`mc26-runs` sits next to this one, every robot run is committed there.

A release is the same chain plus commits and tags in the sibling checkouts (`../mc26-data`,
`../mc26-data-pre`, `../go-mc26`); nothing is pushed without `--push`:

```bash
go run ./gen/cmd/mc26 release --version 26.2            # branches mc-26.2, tags v0.262.<n> / v0.262.<patch>
go run ./gen/cmd/mc26 release --version 26.2 --push
```

## The flow, in GitHub Actions

- `pipeline` (`.github/workflows/pipeline.yml`): every pull request, and on demand for any
  version — the local `pipeline --smoke --e2e`, plus the coverage report and the artifacts.
- `release` (`.github/workflows/release.yml`): on demand with a version — the local `release
  --e2e --push`, with a write deploy key per target repository (docs/release.md). Tags are created only here.

The smoke test joins the server, waits for chunks and its own chat echo. The end-to-end run
builds every example against the built library and checks: `mcping` reports the version;
`daze` sees a broadcast, a private message and an item given over RCON while chunks stream in;
two `daze` bots hear each other's chat (typed on their consoles), and one of them, made an
operator, teleports itself to the other (checked with `data get entity … Pos`) and gives it a
diamond the other bot sees in its inventory; `minimal` and `autofish` log in; `pressure`
logs three bots in; `mcadump` reads a region file the server wrote. Then the robot plays: on a
flat world (`robot`) and on ordinary terrain in a server of its own (`survival`); and the world the
server wrote is read back, by the library (`savechunk`) and by the reader that has only the JSON
(`saveschema`). [docs/testing.md](docs/testing.md) lists every step and scenario.

Versions: Minecraft 26.1 and later, release ids and pre-release ids alike (`26.3-pre-2` goes to
`mc26-data-pre` and tags as `v0.263.0-pre2.<n>`). The three newest Minecraft versions receive
fixes; older branches are frozen.

## Words

| word | what |
|---|---|
| the kit | go-mc26-kit: the bot, the server framework, the account flows and the examples, on top of the library |
| smoke | the short live test: the kit's bot joins a vanilla server of the version and sees chunks and its own chat |
| e2e | the end-to-end scenarios: the kit's example bots, the robot among them, against a vanilla server, judged by the server |
| crosscheck | a recorded session decoded again by a reader that has only the JSON (`gen/crosslang`) |
| hole | a place in a schema the extractor could not describe; `build` refuses a schema with one (`--allow-holes` while it is being fixed) |
| overlay | an older version's own copy of a hand-written file (`gen/src/_versions/<version>/`), applied over `gen/src` when that version is built |
| `YYN` | a Minecraft version without its dot, as tags carry it: 26.3 → `v0.263.<patch>` |
| unobfuscated | since 26.1 Mojang ships the server jar with its real class and field names, which is what lets the extractors read the wire layout from the classes themselves |

## Docs

[docs/](docs/README.md): [architecture](docs/architecture.md), [generated versus
hand-written](docs/generated-vs-hand-written.md), [what is still hand-written and why](docs/hand-written.md), [a new Minecraft version](docs/new-version.md),
[testing](docs/testing.md), [releasing](docs/release.md); [gen/README.md](gen/README.md) for the
commands, generators and packet structs; [data-gen/README.md](data-gen/README.md) for the extractors.
[wip/](wip/README.md) is the backlog: one task per file, in order, with the decisions that are
still open and the records of what was done.

Built with Claude Opus and Claude Fable. Carries code from
[Tnze/go-mc](https://github.com/Tnze/go-mc) (MIT); `gen/src/COPIED` (and `COPIED` in the kit) lists the
origin of every hand-written file — `go run ./gen/cmd/mc26 report --version 26.2` measures the split.
