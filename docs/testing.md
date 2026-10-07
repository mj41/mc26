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
`eclipse-temurin:25-jdk`, offline mode, flat world (no structures: each version puts its villages elsewhere), RCON on; waits for "Done"; runs `go test
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
| `survival` | after the flat server, a second one of ordinary terrain (level type normal, seed 26265 or `MC26_SEED`, made by 26.1 for every version — below); the robot, `Settler`, is put in the nearest forest (`locate biome`, `spreadplayers`) with nothing, at noon with the clock stopped (`gamerule advance_time false`); its view of a cube of the terrain around it is judged block by block (`execute if block`: big palettes, many states), then `get wooden_pickaxe` (recipes not yet in the book: the ingredients first, from the game's recipes), `get cobblestone 25`, `get torch 4` (coal ore if some is in sight, else charcoal from a furnace it builds), `get stick 8` and `get oak_log 16`, then `shelter` — a room three by three and two high dug into a hill (a hut built round it on open ground): the server checks it closed on every side (walls, the way in, floor and roof, nothing a monster passes), furnished along its right wall going in (crafting table, chest, furnace) and lit (block light at least 8, an inline `location_check` predicate); `get stone_pickaxe 3` at its table; then normal difficulty and the night (`time set 13000`) and `descend 16`, a staircase down from the shelter lit as it goes — the server must have the robot no higher than y 16, alive, and the server log must hold no complaint |
| `days` | only when named (`--only days`), about an hour: on its own ordinary-terrain server, the game's clock running from the first morning at normal difficulty, the robot `Settler` lives two days and nights as the kit's `examples/robot/SCENARIO.md` describes — a shelter dug into a hill, stairs and mines by night, a field by day, an iron pickaxe and diamonds on the second night; the server must show the iron pickaxe and a diamond, the robot must not die, and the server log must hold no complaint about it |
| `week` | only when named (`--only week`), about two and a half hours: `days` and then the days after, as the kit's `SCENARIO.md` "Day 3 and on" says — each day up the stairs, the field harvested, sown and baked into bread, wood till dusk; each night down and mines — for `MC26_DAYS` days (7); the robot must not die |
| `field` | only when named (`--only field`), a few minutes: on the survival world by day, the robot is given a bucket, a hoe, 40 seeds, dirt and cobblestone; a 2×2 pond of still water is sunk into the ground thirty blocks east (`execute positioned over motion_blocking_no_leaves`, the trunks standing there taken out first; no walk built: the robot finds its own way over the land), and `farm 40` must make the field by its place with its own water — a 2×2 pool (two buckets fetched from the pond, poured in opposite corners: an endless source) and two channels of ten filled from it, the rows across the field seed, water, seed, seed, water, seed — the pool and channels all still water (`execute if block … water[level=0]`), at least 30 of the 40 farmland sown, and at most 6 logs left within four blocks of the field (the trees round it felled) |
| `fieldwild` | only when named (`--only fieldwild`): no bucket — a 3×3 pond is put twenty blocks east in grass; `water` must find it and `farm 24` must make the field round it (at least 16 sown) |
| `farmstart` | only when named (`--only farmstart`), up to an hour: the first day handed over (stone tools, a hoe, logs, torches, a table, stone and dirt), then `shelter`, `water` (real water found in the seed's world: downhill, the rings), `farm 16` (round the water found without a bucket, or by home with one), three iron given and `harvest` (the field by home with its own water); each step in the morning, a step stopped by the dusk going on the next morning; it logs where the world's nearest river, ocean and frozen land are |
| `village` | only when named (`--only village`): on the survival world, the robot is put by the nearest village (`locate structure #minecraft:village`, `spreadplayers`) with 64 each of coal, wheat and sticks; `village 3` must find its villagers and `tradeall` trade with them — real villagers with the trades the game gave them (the merchant's offers, the selected trade, the result slot), tried again every half a minute, nine times at most, while they take their jobs — then the server must have emeralds in the robot's inventory and log no complaint |
| `fish` | only when named (`--only fish`): on the survival world by day, clear weather, a 4×4 pond of still water sunk into the ground four blocks east, a fishing rod, a furnace and coal given; `fish 3` must catch three cod or salmon and cook them — the server must have three cooked fish in the robot's inventory |
| `robot` | `examples/robot` joins and, once the server said the level is loading and its chunk arrived, sends the player-loaded notice; it then ticks like the vanilla client (keys, movement packets, tick end, every 50 ms). Its own position must equal `data get entity Robot Pos` after the spawn, after a `tp` (the teleport confirmation carries the position from 26.3) and after a minute online; a `look` must show up in `data get entity Robot Rotation`; `block x y z` answers are judged by the server (`execute if block x y z <answer>`) for the chunk data, a filled cube and single blocks; then an obstacle course built over RCON, one station at a time — a walk that must end within 0.001 of the distance the game's arithmetic gives, a sprint and jump, slabs stepped onto, a block jumped onto, a drop, sneaking at an edge, a pool swum across and left, a ladder climbed — each ending where it should with no teleport but the scenario's own; the robot's inventory slot by slot against the server's `Inventory` and equipment, the held slot, an effect, the experience level; summoned entities found where the server has them, after a teleport too, and gone once removed; `goto` through a maze (a three-high opening, a raised floor to jump onto, a trench to jump, a ladder to the exit), across a field where a wall is put in its way while it walks, and `follow` the daze bot through two teleports; `dig` dirt by hand, stone with a wooden pickaxe and by hand, obsidian with a diamond pickaxe, each gone on the server, in exactly the ticks the game's arithmetic gives, with the right drop picked up (none for stone by hand); `place` a pillar and a wall, `use` a door twice and a button that lights its lamp, `eat` bread after a moment of hunger (difficulty easy for it), each judged by the server's block or food level; `craft` planks, sticks and a crafting table from logs in the inventory's grid, place and open the table and craft a wooden pickaxe in it, `store` and `take` cobblestone in a chest, `smelt` raw iron in a furnace — each judged by the server's NBT of the player and the chest; `guard` in a fenced arena against a husk with a stone sword (difficulty easy, noon): the husk dies, the robot lives; then `/kill` and the robot respawns and plays on. goals: in a patch with two trunks of logs and a stone boulder, from an empty inventory, `get wooden_pickaxe` (logs dug, planks, sticks, a crafting table placed and used), `get cobblestone 25` (stone dug with the pickaxe), `build hut` around itself — the server checks the pickaxe and every block of the hut. an enchanted, damaged pickaxe moved by two clicks with no slot sent back (the component hashes of a click are right); standing in a channel of flowing water, the robot drifts downstream with no complaint from the server; a sign written (its front text read back), a sheep sheared and its wool collected, two cows fed wheat (both in love), two trades with a villager (four bread for two emeralds) — each judged by the server's NBT. items: 25 stacks with every kind of component given, decoded, listed as the server's `Inventory` has them, clicked out and back with no slot sent back (every component's hash right); entities: one of every entity type summoned around the robot, each seen, where the server put it, named by its data, with only data values its type's layout has; dimensions: the Nether by teleport (its bedrock at y 0), a dig and a walk there, a portal frame built, lit and walked into, the End and back; chat: commanded by the daze bot in public chat and by a whispered order, each answered the same way; farm: grass tilled, wheat sown, bone-mealed ripe, harvested; sleep: a bed placed and slept in, the night skipped; minecart: put on a rail, ridden to the end of a powered track, the robot seated where the server has it; boat: put on a pool, rowed and turned with the robot's own boat physics, no move corrected by the server; `MC26_ROBOT_STEPS=goals,fight` runs just those steps of the scenario (a step it does not know is an error naming the known ones); and the server log must hold no complaint about it (`moved wrongly`, `moved too quickly`, a kick). The scenario grows with the robot (`wip/README.md`, sections 4 and 5); the robot's output is kept in `temp/e2e/<version>/robot.log`, `ROBOT_TRACE=1` adds its physics state every tick |
| `mcadump` | after the server stopped, dumps an overworld region file it wrote |
| `saveschema` | the same world read by `gen/crosslang`, the reader that has only the JSON (`--world`): the region container from `nodes.json`'s `region` entry; every chunk of the overworld's region files, every entity of its entity region files (each by the format of its type, which its `id` names), every player file and `level.dat`, as generic NBT through the tags table, re-encoded byte for byte, and walked against `save_schema.json` — a key on disk the schema does not name, a key whose tag does not fit its node, a required key absent, each a finding. A schema that is complete only to a reader who already has this library fails here. The daze scenario runs `save-all flush` while its bot is online so there is a player file to read |
| `savechunk` | every chunk of every region file the server wrote is converted to a `level.Chunk` and back (`go test ./level -run TestSaveChunk` in the kit tree, with the file's path). The saved shapes are generated from the reader and the writer Mojang parses and writes a chunk with, and this is the only place a real save file of the version is read; the scenario fails when the test matches nothing, so it cannot pass by checking nothing |

To watch a scenario from the game itself, `MC26_PLAY_ADDR=<a LAN address of this machine>`
publishes the server's game port on that address too (RCON and the management protocol stay
on 127.0.0.1); the run logs `players can join at <address>:<port>`. The servers are offline
mode, so any client joins under any name — `gamemode spectator <name>` over RCON (or `/op` in
the server's console) keeps the visitor out of the robot's way — the scenarios on ordinary
terrain do it themselves, a few seconds after a visitor joins. The robot's events
(`temp/e2e/<version>/<scenario>.events.jsonl`, the kit's `examples/robot/EVENTS.md`) are what
go-mc26-robotview draws.

That file is a link into the run's own directory: every robot run is kept in a directory of
its own — in the mc26-runs repository beside this checkout when there is one (`MC26_RUNS`
names another place; without either, `temp/e2e/runs/`), each run committed to it with its
result — `<date>_<version>_<scenario>_<robot>/` — `events.jsonl`, `robot.log` and
`meta.json` (when it started and ended, the version, the scenario, the world's generator and
seed, the mc26 and kit commits and whether their trees had changes). `robotview -replay
<run>/events.jsonl -speed 20` plays a run again, faster than it ran — runs side by side, a
night's mining in minutes. `MC26_SEED=<seed>` plays the ordinary-terrain scenarios on another
world, or on a kept run's again: the same seed is the same world and the robot chooses the
same on it; the mobs, the server's other chances and the timing are not the same.

One world for every version: the same seed makes slightly different land in each Minecraft
version, so the ordinary-terrain scenarios play a world the oldest version the library speaks
makes — 26.1, or `MC26_WORLD_FROM=<version>` (`own`: each version its own; a version older than
it plays its own) — its forest located and every chunk 10 round it made, kept under
`temp/e2e/worlds/` for the next scenarios. Each version plays a copy, upgraded as its server loads
it, the robot at the same forest: a scenario passes or fails on every version for their code,
not their land (past those chunks each version makes its own).

Stopped from outside (SIGTERM, as a test bench's pod goes, or Ctrl-C), the harness closes the
robots it runs as a scenario's end would — `robot.log` kept, `ended` noted, `run-stopped` in the
index — stops every server it runs (the flat world's, or the one a survival-world scenario
started) and exits (143, 130).

A robot the server puts back tick after tick (its world and the server's disagree: more than
forty corrections in ten seconds) ends itself with `robot: relog`; with `MC26_HOLD=1` the run
starts it again at once, with no fix to wait for — five times on one command; a sixth relog
holds the run as a failure does.

In the `days` and `week` scenarios the clock is moved on (`time add`, which keeps the day
count) while the robot only waits: home at dusk waiting for the night, a night it stays in, a
night walled in where it is. `MC26_REALTIME=1` keeps the game's own time. Crops and furnaces do
not go on in time skipped.

In the `days` and `week` scenarios a death does not end the run at once: it is counted, the
robot goes back for its things (`recover`) and the command is tried again, five deaths at
most; a run with a death fails at its end even when it lived its days.

`MC26_HOLD=1` makes the `days` and `week` scenarios hold instead of failing — for working on
the robot without playing its first day again for every fix. A command that fails, or the
robot standing still three minutes on one command (it is stopped), holds the run; a death
holds it only when the robot's process has gone too (stopped while it lay dead), before the
`recover`, and a sixth death holds it as a failure does. Held: the server and its world stay;
the robot is stopped and its run kept as `held` (with the error); the run's log says what to
do. Fix the robot, re-assemble the kit (`mc26 kit --version <v>`), then `touch
temp/e2e/<version>/<scenario>.resume`: the robot is built anew and started in the same world —
the server kept where it was and what it carried; its memory file (`<scenario>.memory.json`,
its shelter, stairs, field) tells it the rest — and the command is tried again, in a run of
its own whose `meta.json` names the run it `resumes`. `<scenario>.abort` ends the held run.
A held run is not a passing one: a resumed run is a debugging aid.

Kept runs are a contract with their readers (the dashboard, a replay, an analysis), as the
events in them are (the kit's `examples/robot/EVENTS.md`). A run's directory is named
`<YYYYMMDD-hhmmss>_<version>_<scenario>_<robot>` (local time it started) and holds:

| File | What |
|---|---|
| `events.jsonl` | the robot's events, the format of `EVENTS.md` |
| `robot.log` | the robot's output, written when it stops |
| `meta.json` | what ran, below; written at the start and again with `ended` when it stops |

`meta.json`: `started`, `ended` (RFC 3339; no `ended` while it runs or after a crash),
`version` (the game's), `scenario`, `robot` (its name), `server` (address), `levelType` and
`seed` (empty for the flat world), `mc26` and `kit` (`commit`, `subject`, `dirty`: whether the
tree had changes not committed), `host`, `bench` (`MC26_BENCH`: the cluster test bench it ran
on, absent on a workstation), `delivery` (`MC26_DELIVERY_FILE`: the JSON object a bench's
supervisor wrote for the robot's code — commit, signer, who, when — as it stood when the run
started), `resumes` (the held run this one goes on from). Fields may be added; none is renamed
or removed without saying so here. `result` (`ok` or `fail`) and `error` are added when the
scenario ends; `held` (and the error it was held on) when the run is held (`MC26_HOLD`): its
robot stopped, the run after it, started again in the same world, names it in `resumes`. Beside
the runs, `runs.jsonl` is their index, append-only, in the events' envelope (`source`
`mc26:e2e`): `run-started` (with `events`, the absolute path of the run's events, and `meta`),
`run-stopped`, `run-result` (`result`, `error`); each entry's `data` has `run` (the directory's
name) and `dir`. A reader following "the run now" follows the index and switches to a run at
its `run-started`. mc26-runs' README has the details. `temp/e2e/<version>/
<scenario>.events.jsonl` is also a link to the latest run's events, pointed at the next run
when that one starts.

Where the run keeps its files: `MC26_ROOT` (the checkout; found from the working directory when
unset), `MC26_TEMP` (the scratch directory: the e2e work directories and worlds, default
`temp/`), `MC26_CACHE` (the server jars, default `cache` in `MC26_TEMP`) and `MC26_DATA` (the
extracted data, default `data` in `MC26_TEMP`); a relative one is taken from the working
directory and made absolute (a relative path given to `podman -v` would be a named volume). A
server jar is kept with the sha1 Mojang's manifest gave for it (`<version>-server.jar.sha1`); a
jar that matches it is used without asking Mojang, so a cache filled once runs with no network
(a test bench in a cluster).

The save package's own tests read a small world offline, `save/testdata/world` in the built
library: four chunks around the spawn, four entity chunks, `level.dat` and one player file,
each sector as the server wrote it, in region files of the same names. The end-to-end run cuts
it from its server's world (the `fixtures` scenario, once the flat world's server has stopped:
after `mcadump`, `savechunk` and `saveschema`, before `survival` and the scenarios run only
when named; also `go run ./gen/cmd/mc26 fixtures --version <v>` from the world `mc26 e2e` left)
into `gen/src/save/testdata/<version>/`, with a `SOURCE` note; the build ships the version's
own and no other, and lists its files in `COPIED`. The end-to-end run cuts it only when the
version has none yet, so `verify` leaves committed worlds alone; `mc26 fixtures` is how an
existing one is cut again. The worlds of released versions are committed (68 KB each); a
pre-release's or snapshot's is written the same way but ignored by git, since it changes with
every pre-release. A version's first build, before its end-to-end run, has none, and the save
tests skip with a note; the next build has it. The files are not edited by hand.

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
that is already there without starting a server. The same reader reads a world with `--world`
(the `saveschema` scenario of the end-to-end run): the region container from `nodes.json`, the
chunks, the entities by their type and the player files from `save_schema.json`. `gen/crosslang/FINDINGS.md` records what that
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

`--only robot,daze` runs just those scenarios (a name it does not know is an error naming the
known ones). For work on the kit, `mc26 kit --version 26.3` copies the kit checkout into
`temp/kit/26.3` against the library built there before, without building the library again;
then `mc26 e2e --version 26.3 --only robot` runs the scenario you are working on.

## Adding a scenario

`gen/internal/e2e/e2e.go`: write a `func scenarioX(o Options, bin string, srv *smoke.Server)
error` — `bin` holds one binary per example, `srv` gives the address and the RCON address — and
add it to the `scenarios` list in `Run` (or to `after`, the ones run once the flat world's
server has stopped; `long` runs one only when named). Start bots with `startDaze` (console on
stdin, output in a `syncBuffer`) or `runFor` (run until every expected line appeared); drive
the server with `dialRCON`. Keep every wait bounded; report the tail of the bot's output on
failure.

## Where the files are

`temp/lib/<version>/` is the built library and `temp/kit/<version>/` the kit assembled against
it; `temp/smoke/<version>/` and `temp/e2e/<version>/server/` hold the servers (world,
`server.log`); `temp/e2e/<version>/bin/` the built examples; `temp/capture/<version>.jsonl` the
last recorded session. `temp/` is ignored by git.
