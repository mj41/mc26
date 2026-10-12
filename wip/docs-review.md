# The docs and the command line, as a new user reads them

Status: **decision**: a review written 2026-10-12, and its fixes, one topic per commit (the
table at the end), on the branches `docs-review` of mc26 and of go-mc26-kit, built and tested
together for 26.3 and not merged: the owner reviews them. Left for the owner: the four items
under the table. The question: can someone write a Minecraft 26.3 bot from the docs
alone? The review read everything a user sees:
- mc26: the README, docs/, gen/README.md, and the `mc26` command's help and errors;
- go-mc26: the README of the `mc-26.3` branch and the godoc of `temp/lib/26.3`;
- go-mc26-kit: the README, the examples and the godoc of every package.

It was done as three passes:
- **a new user**: a reviewer who had never seen these modules wrote a bot from the two READMEs and
  godoc, with the modules from the public proxy (go-mc26 v0.263.1, go-mc26-kit v0.2.0). The bot
  joins offline, prints chat, answers "hi" and leaves cleanly on a signal. Since there was no
  vanilla server, the bot was tested against a stand-in built from the kit's `server` framework;
- **the godoc**: package comments and doc comments counted and read, as pkg.go.dev shows them;
- **the command line**: every subcommand's help and its errors for the common mistakes, plus every
  claim of the mc26 docs checked against the code.

**The answer**: yes, but only by reading godoc rather than copying the examples, and only when
installing with `go get`. Cloning the kit and running its README's command builds a 26.1 client.
The examples a newcomer copies teach the old chat callback and a Chinese locale. Nothing says
that offline mode is just a name. And the user has no way to test without a real server. The new
user's bot compiled on the first try. It worked once `msg.PlayerChat` had been found in godoc;
neither example uses it.

## Coverage, as pkg.go.dev shows it

| | go-mc26 (26.3, `data/lang` left out) | go-mc26-kit (examples left out) |
|---|---:|---:|
| packages | 36 | 26 |
| a package synopsis that reads right | **14** | 19 |
| no package comment | 8 | 7 |
| synopsis "Code generated … DO NOT EDIT." | **11** | 0 |
| synopsis a file's description, not the package's | 3 | 0 |
| exported identifiers without a doc comment, of hand-written code | 326 of 629 | 236 of 576 |
| the same, of generated code | 6,750 of 9,066 | – |
| on the bot's entry path (`bot`, `basic`, `msg`, `playerlist`, `microsoft`, `yggdrasil`), undocumented | – | 65 |
| `Example` functions | 20 (none in `protocol/*`, `data/*`, `level/block`, `registry`, `save`) | 6 (two of the three in `bot` are wrong, see 5) |

The generated packet structs are all documented. Most of the generated gaps are per-item and
per-entity variables, block types, and enum constants without a group comment.

## Findings, in the order a new user meets them

### A. Writing a bot (go-mc26-kit, the go-mc26 README)

1. **A clone of the kit builds a 26.1 client.** `README.md:40-41` says `go run ./examples/daze
   -address host:port`. But the kit's `go.mod` requires the oldest supported library (v0.261.1),
   so in a clone the examples speak protocol 775, and a 26.3 server rejects them as outdated. `go
   run github.com/mj41/go-mc26-kit/examples/daze@v0.2.0` does the same. *Blocks.* Fix: one README
   line before the commands (`go get github.com/mj41/go-mc26@v0.263.1` first, the version of
   your server), and the examples print `version.Name` and the protocol at start.
2. **The two READMEs install differently, and nothing pairs the kit with the library.** The go-mc26
   README says `go get github.com/mj41/go-mc26@mc-26.3`. That is a branch, which becomes a
   pseudo-version once the branch moves past its tag. The kit README says `@v0.263.1`. The kit's
   phrase "whichever tag is newest" does not say which library versions that tag supports; only
   the kit's `ci.yml` matrix does. *Slows.* Fix: the template installs by tag; the kit README gets a
   line saying which library versions the kit builds against.
3. **No first program.** The kit README is a package table and two commands. `examples/minimal`
   joins and does nothing; `examples/robot` is 13,000 lines. Nothing shows a program that joins,
   reads chat and answers. *Slows.* Fix: `examples/minimal` becomes that program (about 60 lines:
   offline join, chat printed, an answer, a clean exit on a signal or a kick), and the README shows
   it.
4. **Offline mode is not documented.** `bot.Auth` says only "Auth includes an account", and its
   fields have no docs. The examples' `-uuid` and `-token` flags suggest an account is needed. The
   `offline` package suggests the UUID has to be set. And `bot.JoinOptions.KeyPair` says "If nil,
   it will be obtained from Mojang when joining", although nothing reads the field. In fact a name
   is enough: the session server is called only when the server asks for encryption (an
   online-mode server). *Slows; the new user guessed it.* Fix: document `Auth`'s fields and
   offline mode (in the README and in `bot`'s package doc); `KeyPair` is either documented as
   unused or removed.
5. **The `bot` examples in godoc are wrong.**
   - `ExampleClient_JoinServer_offline` never calls `basic.NewPlayer`, and the play-phase
     keep-alive is answered only there, so a copy of the example is timed out by the server.
   - `ExampleClient_JoinServer_online` logs in with `yggdrasil.Authenticate(e-mail, password)`, a
     login Mojang has retired. It sets `c.Events.GameStart`, `ChatMsg` and `Disconnect`, fields
     `bot.Events` does not have. And it links `go-mc26/issues/106`, an issue number from the
     upstream project.

   *Misleads.* Fix: the offline example with `basic.NewPlayer`; the online one with
   `microsoft.AuthenticateCached`, as `examples/microsoft` does.
6. **The examples teach the old API and Chinese defaults.**
   - `examples/daze` uses `PlayerChatMessage(msg, validated)`, which carries no sender, not
     `msg.EventsHandler.PlayerChat`, the one a program that answers needs.
   - daze imports `data/lang/zh-cn`, so translated system messages print in Chinese.
   - `basic.DefaultSettings` sends the server `Locale: "zh_CN"` (`settings.go:40`), and the
     `Settings` fields are commented only in Chinese.
   - `minimal` and `daze` define their own `DisconnectErr` beside the undocumented
     `bot.DisconnectErr`, and neither handles Ctrl-C.

   *Misleads.* Fix: daze on `PlayerChat` without zh-cn; `DefaultSettings` in en_us with English
   field comments; the examples use `bot.DisconnectErr` and `signal.NotifyContext`.
7. **The entry path is half undocumented.**
   - In `bot`:
     - `Client.Close`, the only way out, has no doc;
     - `HandleGame` says "will block if you don't receive from Events", but `Events` is not a
       channel;
     - `NewClient` says "login your Mojang account";
     - `Events.AddListener`, `PacketHandler`, `DisconnectErr`, `PacketHandlerError`, `LoginErr`,
       `ConfigErr` and `JoinOptions` have no docs.
   - In `basic`: `Player` has no doc, and the package doc's `[Client]` link does not resolve.
   - In `msg`: no package comment, `SendCommand`'s doc is a copy of `SendMessage`'s, and the
     exported name `ErrUnknwonChatType` is misspelt.
   - `playerlist.New`, the ten exported names of `microsoft`, and the package doc of `offline` are
     missing too.

   *Slows.* Fix: the docs, `bot`'s package doc first: what to create, in what order, what is
   required.
8. **What a well-behaved bot needs is not said.** `basic.EventsListener.Teleported` says "Without
   a controller, call [Player.AcceptTeleportation] here", and neither example sets it or runs a
   controller. `control` lists the packets it sends, but not whether a bot that only chats needs
   it. *Slows.* Fix: one paragraph in `bot`'s package doc, and the examples do it.
9. **No way to test without a server.**
   - The kit's `bot/simworld` (a game stepped in the test's own process) is on `main`, unreleased,
     and unmentioned in the README.
   - The `server` package doc points to `examples/frameworkServer`, which does not exist, and to
     the upstream project's `go-mc/server`. `server/README.md` points there too, and
     `Packet757`/`Packet758` are leftovers from Minecraft 1.18.
   - A stand-in server needed knowledge nothing documents: a list-ping handler is `PingInfo` plus
     a `PlayerList`, and a player chat line before a player-info update is dropped as "unknown
     player".

   *Blocks testing.* Fix: the README's "testing without a server" section, written with the
   release that carries simworld; the `server` package doc corrected.
10. **The kit README and the physics godoc disagree.** The README says `bot/physics` collides
    with "the entities a player stands on (boats, shulkers)" and swims. `go doc bot/physics` says
    "Not yet: … swimming pose, … entity collisions". *Misleads.* Fix: one of them, after checking
    which is true.
11. **`yggdrasil` is described wrongly.** The kit README calls it "Mojang session server"; it is
    the legacy authentication server. Its package doc is a quote from wiki.vg (gone) from 2013, and
    it does not say that Mojang accounts are now Microsoft accounts. *Misleads.* Fix: the README
    row and the package doc ("new programs log in with `microsoft`; this remains for
    Yggdrasil-compatible third-party servers").
12. **The error messages are mostly good.** "connection refused", a DNS failure and a refused
    login each say what happened. Two exceptions:
    - a listener that is not Minecraft fails as `[encrypt start] … connection reset by peer`, a
      misleading stage for an offline server;
    - an unreachable host hangs, and no dial timeout is documented.

    *Cosmetic.*

### B. The library's godoc (go-mc26: generators and `gen/src`)

13. **The generated header is the package doc of 11 packages.** `generatedHeader`
    (`gen/internal/generate/helpers.go:36`) ends in one newline, so the "// Code generated …"
    line is attached to the `package` clause. That makes it the synopsis of `data/entity`,
    `data/version`, `level`, `level/biome`, `level/component`, `protocol/*` and `wire`.
    `go doc ./level/component` prints it eight times. *Misleads every package list.* Fix: two
    newlines. Related problems:
    - `protocol/*` writes its package comment into both the clientbound and the serverbound file
      (`gen_packets.go:2107`);
    - `gen_nbt.go:1036`, `gen_save.go` and `gen_entitydata.go` put a file's description directly
      above `package`, so it becomes the synopsis of `registry`, `save` and `protocol/types`. These
      synopses show generator internals ("… and 182 more").
14. **Hand-written packages without a package comment**: `chat/sign`, `level/block`, `net/packet`,
    `net/queue`, `save/region` and `yggdrasil/user`, plus `level`, `registry` and `save`, which
    are left with only the fragments of 13. The entry types on these paths are undocumented too
    (`block.StateID`, `level.Chunk`, `registry.Registry`). Fix: in `gen/src`.
15. **`chat`'s package doc** names `github.com/mj41/go-mc26/data/en-us`, which does not exist.
    - Its English is broken ("It's very recommended that use…"), and so is `Message.String`'s doc
      ("you must set On Windows…").
    - It cites wiki.vg.
    - `Message`'s style fields are commented only in Chinese.

    Fix: rewritten. The part about languages waits for the language decision (wip/lang.md).
16. **Generated smaller things.**
    - `version.Name`'s doc gives "26.2" as its example in the 26.3 build.
    - The packet-id and registry-id packages have no package comment.
    - The 95 registry variables have no doc.
    - Enum blocks have no group comment.
    - No `protocol` package has an `Example` showing a packet decoded (`p.Scan(&v)`) and encoded
      (`pk.Marshal`).
17. **wiki.vg links** (the site is gone; its pages moved to minecraft.wiki): `chat`,
    `level/bitstorage.go`, and four in the kit (`bot/login.go`, `bot/basic/events.go`,
    `bot/pinglist.go`, `server/auth/auth.go`).

### C. Building the library (mc26: the README, the docs, the `mc26` command)

18. **The README never says what go-mc26 is for, or how to use it.** It describes the pipeline.
    The one consumer line, in `docs/release.md:320`, gives `v0.262.0`. Fix: a "use it" block near
    the top: the `go get` with the newest tag, and the kit for the bot.
19. **The kit checkout is a requirement nobody lists, and `build` finds out at the end.**
    - "Requirements: Go and podman or docker" leaves out the sibling `go-mc26-kit` checkout, the
      network and the memory (6 GB for the extraction container).
    - `build` builds, vets and tests the library first. Only then does the kit assembly fail. The
      error is good ("clone github.com/mj41/go-mc26-kit there, or --kit-src DIR"), but the
      atomic swap throws away the library just built.

    *Blocks.* Fix: the requirement in the README, and the checkout checked before any work in
    `build`, `pipeline`, `verify`, `update` and `release`.
20. **The first command shown is the longest, and for 26.2.** `pipeline --version 26.2 --smoke
    --e2e` runs the robot and survival scenarios. Every example in the README is 26.2, and none
    says what success looks like. Fix: `extract --version 26.3` and `build --data 26.3` first, with
    the last line `build` prints; `pipeline` lower, marked long.
21. **`gen/README.md` says a published mc26-data checkout can be built from; none can.**
    mc26-data's `mc-26.3` (v0.263.0, extracted 2026-10-03) has no `data/`, no
    `block_behaviour.json` and no `component_hashes.json`. The generators added since need them,
    and they fail only after the generators before them have run. *Blocks, if tried.* Fix: the data
    re-extracted and released with the next release (the owner's call). Until then, the sentence
    says "an extraction by a compatible mc26 commit". `build` also checks its inputs first.
22. **"Everything below writes only under temp/" is not so.** The first e2e run of a version cuts
    its fixture world into `gen/src/save/testdata/<version>/`, and robot runs are committed to
    `../mc26-runs` when that checkout exists. Fix: say so.
23. **The help.**
    - `mc26`, `-h`, `--help` and `help` print the usage to stderr with exit 2.
    - `mc26 biuld` prints the usage without saying the command is unknown.
    - The usage leaves out `crosscheck` and `fixtures`, which the README shows.
    - Most commands have no description.
    - `--out` looks required.
    - Nothing says `mc26 <command> -h`.

    Fix: help on stdout with exit 0, an "unknown command" line, every command with one line.
24. **The errors for a mistyped version or a missing directory.**
    - `build --version 26.3` answers "--data is required", although every other command takes
      `--version`.
    - `build --data 26.33` answers with a raw `open …/temp/data/26.33/version.json`.
    - `--data /nonexistent/dir` is quietly joined under `temp/data`.
    - `report` and `docs` lead with a missing file.

    Fix: `--version` accepted by `build`; "no data for 26.33 in temp/data (have: 26.1, 26.2,
    26.3); run mc26 extract --version 26.33".
25. **`extract --dry-run` downloads the jar and every language file before printing.**
    `extract --version 26.33` creates `temp/data/26.33` before Mojang's manifest says the version
    does not exist, and a later `verify` counts it as extracted. Fix: the dry run before the
    downloads; the directory after the manifest lookup.
26. **`smoke`, `e2e` and `crosscheck` without a built kit** download the jar and start the server
    before failing, where `mc26 kit` checks first ("mc26 build first"). Fix: the same check.
27. **A subcommand's `-h`.**
    - It prints `Usage of build:` and flags, with no synopsis.
    - `fixtures -h` renders a backquoted phrase as the argument name.
    - `latest -h` fetches the manifest.
    - `rcon` without a command exits 0 silently.
    - Extra arguments are ignored.
    - `verify --steps biuld` runs and writes a log before rejecting the step.
    - Outside a checkout, `-h` fails on the flag defaults.
28. **Stale statements.**
    - "the twelve generators" (there are 24), and `gen/README.md`'s generator list.
    - Overlays "none at present" (26.1 and 26.2 have one).
    - The kit's tags `v0.1.<n>` (it is at v0.2.0).
    - "until the data repository is public" (it is).
    - `ci.yml` for `lib-ci.yml`.
    - `new-version.md` waiting for 26.3.
    - 26.3-pre-2 as the newest checked version.
    - The e2e list in the README.
29. **Words used before they are explained**: kit, smoke, e2e, crosscheck, hole, overlay, prims,
    `YYN`, and why "unobfuscated" matters. Fix: a short glossary after the repository table.
30. **The small tools**: no arguments gives a clean usage line (good); `-h` is inconsistent, and
    `runstats -h` opens a file named `-h`. The README does not say that they read `temp/data`,
    that `source` downloads the client jar, or that `mcmeta` needs the network.
31. **`import-src` empties `gen/src`** (`os.RemoveAll`), which now holds overlays, fixtures and new
    files, and it is in the usage without a warning. Fix: out of the usage, or behind `--force`.

## What is good and stays

- Every relative link of README.md, docs/, gen/README.md and data-gen/README.md resolves.
- Every error exits non-zero and names its command.
- The errors that already say what to do are the pattern for the rest: `mc26 kit`'s "(mc26 build
  first)", the kit assembly's "clone … or --kit-src DIR", `update`'s "(fix gen/src for V, then …)".
- Versions before 26.1 are refused before any work. Nothing is pushed without `--push`.
- The godoc of `msg.PlayerChat`, `control`, `physics.Mover` and `path.Walker` was enough to wire up
  chat answers and walking without reading code.
- The generated packet structs all carry their Mojang name and id.
- The docs index (`docs/README.md`, "what it answers").

## The order of the fixes

One topic per commit, each repository on a branch of its own (mc26 `docs-review`, the kit
`docs-review`), nothing pushed until the owner says so.

| order | repository | topic | findings |
|---|---|---|---|
| 1 | mc26 | this review | – |
| 2 | mc26 | generated package docs: the header, `protocol` once, file descriptions as file comments, version and id packages | 13, 16 |
| 3 | mc26 | package docs of the hand-written library packages, `chat`'s doc, wiki.vg links | 14, 15, 17 |
| 4 | mc26 | the go-mc26 README template: install by tag, where to start | 2 |
| 5 | kit | README: the pairing, the clone trap, offline mode, physics, yggdrasil, the first program | 1, 2, 4, 10, 11 |
| 6 | kit | `examples/minimal` as the first program; daze on `PlayerChat`, en_us | 3, 6 |
| 7 | kit | `basic.DefaultSettings` en_us, its fields in English | 6 |
| 8 | kit | `bot`'s package doc, its examples, the entry path's docs, `KeyPair` | 4, 5, 7, 8 |
| 9 | kit | `msg`, `playerlist`, `microsoft`, `offline`, `yggdrasil`, `server` docs; the misspelt error | 7, 9, 11, 17 |
| 10 | mc26 | README: what it is, requirements, the first commands, temp/ | 18, 19, 20, 22, 29 |
| 11 | mc26 | the `mc26` command: help, unknown commands, `build --version`, the early checks, the errors | 19, 23–27 |
| 12 | mc26 | stale statements | 21, 28, 30 |

Left for the owner: re-extracting and releasing 26.3's data (21), releasing simworld (9, the
README already describes it), whether `import-src` stays (31), and pipeline.yml's default version
(26.2, while its comment says the newest). Not done: the e2e scenarios that read daze's output
need a run against a vanilla server before release (daze prints in English now; the lines they
wait for are unchanged), and release's and update's -h still need a checkout.
