# Testing

Four layers, all runnable locally with Go and a container runtime; the workflows run the same.

## Unit tests — inside the built library and the kit

`mc26 build` runs `go vet ./...` and `go test ./...` in the result. The generated packet
packages carry round-trip tests (encode, decode, compare) for every generated struct; the
hand-written packages keep their own tests (NBT, region files, chat, RCON on a random port).
Then it assembles the kit (the `../go-mc26-kit` checkout, `--kit-src` to point elsewhere)
against the result under `temp/kit/<version>` — a workspace there points the library's import
path at the built tree — and runs `go build`, `go vet` and `go test` in it (the server framework
against the bot on a random port, the accounts). The kit is the pipeline's test client, which
is why a build here needs its checkout.

## Smoke test — the kit and the library against a vanilla server

```bash
go run ./gen/cmd/mc26 smoke --version 26.2 [--kit temp/kit/26.2] [--runtime podman|docker|host]
```

Starts Mojang's server of that version (the jar the extraction cached) in
`eclipse-temurin:25-jdk`, offline mode, flat world, RCON on; waits for "Done"; runs `go test
./bot github.com/mj41/go-mc26/management -run TestSmoke` from the kit tree with
`MC26_SMOKE_ADDR` set. The bot's test joins, waits for the
login to complete, for 25 chunks and for its own chat message to come back, then disconnects.
It also asks the registries what the server sent that the generated element types do not cover:
the bot keeps the NBT of every registry entry (`Registries.KeepRaw(true)`) and re-reads it
strictly, so a missing field or a wrong generated `nbt` tag fails the test with the registry and
the entries that carry the unknown key, instead of decoding to a zero value in silence.
`--runtime host` uses the host's Java 25 instead of a container.

The server is started with its management protocol on (`management-server-enabled`, plain text,
a generated 40-character secret), published as a third port, and `go test ./management` runs in
the same pass: `TestSmokeManagement` dials it with the generated client, reads the status and
checks the protocol number against `data/version`, changes a setting and reads it back, adds a
player to the allowlist and waits for the notification that causes, lists the game rules, and
checks an unknown method comes back as the server's error.

A second test in the same package (`TestSmokeEntityData`) checks the generated entity metadata:
it summons a handful of entity types over RCON, each with NBT that moves the fields a server
would otherwise leave at their defaults and never send, and compares every value it receives
with `data/entitydata` — the index must be a field of that entity type and carry the serializer
the table names. It says how many fields it compared and fails when that number collapses, so it
cannot pass while checking nothing.

## End-to-end — the example bots against a vanilla server

```bash
go run ./gen/cmd/mc26 e2e --version 26.2 [--kit temp/kit/26.2]
```

Builds every example of the kit tree (`examples/<name>`, against the library the tree was
assembled with), starts a server as above, and runs:

| scenario | what must happen |
|---|---|
| `mcping` | the server-list ping reports the protocol number and version id of the data |
| `daze` | logs in, chunks stream in; a broadcast (`say`), a private message (`tellraw`) and an item (`give`) sent over RCON show up in the bot's log; a teleport is acknowledged by the server |
| `twobots` | DazeOne and DazeTwo: each hears the other's chat (typed on the bots' consoles); DazeOne is made an operator, teleports itself to DazeTwo (checked with `data get entity DazeOne Pos`) and gives DazeTwo a diamond that DazeTwo's inventory events show |
| `minimal` | logs in |
| `autofish` | logs in, reaches game start |
| `pressureTest` | three bots log in and reach game start |
| `mcadump` | after the server stopped, dumps an overworld region file it wrote |

The `daze` example has a console: a line on stdin is sent as chat, a `/line` as a command. That
is how the harness drives bots; it works for a person too.

## Cross-check — the JSON against a reader that has never seen Go

```bash
go run ./gen/cmd/mc26 crosscheck --version 26.2 [--keep]
```

The question the extracted JSON has to answer is whether it describes the protocol, or only
describes it to a reader who already has this library. `crosscheck` starts a vanilla server as
above and runs the traffic test (`TestSmokeTraffic`, the same bot the smoke test uses, driven
over RCON through gives, summons, a scoreboard, a boss bar, a built chunk section and a rejoin)
through a recording proxy. The proxy splits the stream by the frame `nodes.json` describes
rather than by the library's framing code, follows the state changes `frame.data` names (which
packet switches to which state, which one turns compression on, with the ids `packets.json`
gives them in that version), and keeps the first few
packets of every state/flow/id triple of each session, both directions, in
`temp/capture/<version>.jsonl`.

The recording is then read twice. In Go, `TestCaptureCheck` decodes every packet into its
generated type and requires the body to be consumed exactly — a type short of a field reads
without complaint, so reading all of it is the test. Then `gen/crosslang`, a reader
written from `packet_schema.json` (its `prims` section included), `packets.json`,
`registries.json`, `entity_data.json` and `nodes.json` and nothing else, decodes each packet and encodes it again; the
command passes only when every packet comes back byte for byte. `--keep` re-reads the capture
that is already there without starting a server. `gen/crosslang/FINDINGS.md` records what that
decoder found the JSON did and did not say when it was first written, and what has closed since.

## Everything at once

```bash
go run ./gen/cmd/mc26 verify                       # every extracted version, all four steps
go run ./gen/cmd/mc26 verify --versions 26.3-pre-2 --steps build,smoke
```

`verify` is the check to run before a commit that touches an extractor, a generator or the
sources: build (the schema check, the generators, the unit tests), smoke, cross-check and e2e, for every version under
`temp/data` (or the ones named), one step at a time so that only one server and one compile
run at once. It prints one table:

```
version        build          smoke          crosscheck     e2e
26.1           ok 16s         ok 34s         ok 35s         ok 26s
26.2           ok 16s         ok 35s         ok 35s         ok 26s
26.3-pre-2     ok 13s         FAIL 20s       ok 36s         ok 27s
```

and exits non-zero naming every failed step and its log; each step's full output is in
`temp/verify/<version>/<step>.log` (`--quiet` keeps it out of the terminal). A failed build
skips that version's other steps. A run over three versions takes about six minutes with the
jars cached and the container image pulled.

```bash
go run ./gen/cmd/mc26 pipeline --version 26.2 --smoke --e2e [--skip-extract]
```

`pipeline` is one version from the extraction on: extract (or reuse `temp/data/26.2`), build,
unit tests, smoke, e2e. This is what `.github/workflows/pipeline.yml` runs on every pull
request. `release` runs the smoke test before it commits the library, and the e2e too with
`--e2e`.

## Memory

Everything the harness starts is bounded (`gen/internal/limits`): a go build, vet or test
compiles four packages at a time (`GOFLAGS=-p=4` — a translation table is a large compile), a
server's container has 2.5 GB and its JVM 1.5 GB of heap, the extraction container 6 GB and
every JVM in it 4 GB. Run the versions one after the other, not side by side, which is what
`verify` does: one full pass (build, smoke, cross-check, e2e) holds one server and one compile
at a time. Keep editors'
language servers out of `temp/` — it carries one built library per version, 1.2 million lines
each, rewritten by every build; `.vscode/settings.json` filters it out of gopls, which otherwise
indexes all of them (14 GB seen).

## Adding a scenario

`gen/internal/e2e/e2e.go`: write a `func scenarioX(o Options, bin string, srv *smoke.Server)
error` — `bin` holds one binary per example, `srv` gives the address and the RCON address — and
add it to the `scenarios` list in `Run`. Start bots with `startDaze` (console on stdin, output in
a `syncBuffer`) or `runFor` (run until every expected line appeared); drive the server with
`dialRCON`. Keep every wait bounded; report the tail of the bot's output on failure.

## Where the files are

`temp/lib/<version>/` is the built library and `temp/kit/<version>/` the kit assembled against
it; `temp/smoke/<version>/` and `temp/e2e/<version>/server/` hold the servers (world,
`server.log`); `temp/e2e/<version>/bin/` the built examples; `temp/capture/<version>.jsonl` the
last recorded session. `temp/` is ignored by git.
