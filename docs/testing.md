# Testing

Four layers, all runnable locally with Go and a container runtime; the workflows run the same.

## Unit tests — inside the built library

`mc26 build` runs `go vet ./...` and `go test ./...` in the result. The generated packet
packages carry round-trip tests (encode, decode, compare) for every generated struct; the
hand-written packages keep their own tests (NBT, region files, chat, the server framework
against the bot on a random port, RCON on a random port).

## Smoke test — the library against a vanilla server

```bash
go run ./gen/cmd/mc26 smoke --version 26.2 [--lib temp/lib/26.2] [--runtime podman|docker|host]
```

Starts Mojang's server of that version (the jar the extraction cached) in
`eclipse-temurin:25-jdk`, offline mode, flat world, RCON on; waits for "Done"; runs `go test
./bot -run TestSmoke` in the library with `MC26_SMOKE_ADDR` set. The test joins, waits for the
login to complete, for 25 chunks and for its own chat message to come back, then disconnects.
`--runtime host` uses the host's Java 25 instead of a container.

## End-to-end — the example bots against a vanilla server

```bash
go run ./gen/cmd/mc26 e2e --version 26.2 [--examples ../go-mc26-examples]
```

Builds every example of the examples checkout (its single `main` branch; the harness builds it
against the given library through a temporary `go.work`, whatever `go.mod` pins), starts a server as above, and runs:

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

## Everything at once

```bash
go run ./gen/cmd/mc26 pipeline --version 26.2 --smoke --e2e [--skip-extract]
```

Extract (or reuse `temp/data/26.2`), build, unit tests, smoke, e2e. This is what
`.github/workflows/pipeline.yml` runs on every pull request. `release` runs the smoke test
before it commits the library, and the e2e too with `--e2e`.

## Adding a scenario

`gen/internal/e2e/e2e.go`: write a `func scenarioX(o Options, bin string, srv *smoke.Server)
error` — `bin` holds one binary per example, `srv` gives the address and the RCON address — and
add it to the `scenarios` list in `Run`. Start bots with `startDaze` (console on stdin, output in
a `syncBuffer`) or `runFor` (run until every expected line appeared); drive the server with
`dialRCON`. Keep every wait bounded; report the tail of the bot's output on failure.

## Where the files are

`temp/smoke/<version>/` and `temp/e2e/<version>/server/` hold the servers (world, `server.log`);
`temp/e2e/<version>/bin/` the built examples. `temp/` is ignored by git.
