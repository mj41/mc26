# Work in progress

One task per file, its status on the first line (`open`, `decision`, `research`). The order
below is the order to do them. A finished task is removed: what is worth keeping goes into
`docs/`, the rest is the git history.

## 1. Decisions — the owner's

| order | task | what is asked |
|---|---|---|
| 1 | [zero-hand-written.md](zero-hand-written.md) | everything with data behind it is generated, the save formats included (nine defects came out, each in code that had been wrong against a real server or world). Two things left, both a call rather than work: ~1,900 lines that generating would only move into the generator, and ~7,500 of runtime for a module of its own |

## 2. Defects found on the way

None open.

## 3. Research

| task | what is asked |
|---|---|
| [exports.md](exports.md) | exports of the schema to other ecosystems' formats, and the errors a comparison with one found (docs/minecraft-data.md) |

## 4. The robot — done 2026-10-04

The kit's robot plays the way a person with the vanilla client does — it ticks like the client,
moves under the game's physics, sees blocks and entities, finds its way, digs, places, uses,
eats, crafts, fights, respawns, and pursues goals (`get`, `build hut`) — and the `robot`
end-to-end scenario checks all of it against a vanilla server of every supported version
(docs/testing.md). What is left is in section 2 and in the kit: the physics' gaps are listed in
`bot/physics`'s package comment.

## 5. The robot in use — scenarios that look for bugs

Real uses of a bot, each going down paths of the library nothing has exercised yet, with the
vanilla server as the judge.

| order | task | what is asked |
|---|---|---|
| 1 | done 2026-10-04 | stacks with every kind of component: decoded, listed as the server has them, clicked without a resend (the e2e step `items`; `component_hashes.json` checks every hash against the game's; found: texts lost the elements NBT wraps in a mixed list) |
| 2 | done 2026-10-04 | one of every entity type: tracked and its data decoded (the e2e step `entities`: 152–156 types per version, each where the server put it, named by its data, every data value at an index and serializer its type's layout has; equipment and renames sent later) |
| 3 | done 2026-10-04 | the Nether and the End, by teleport and by a portal it builds (the e2e step `dimensions`: the Nether's bedrock at y 0 and 127, a dig and a walk there, a 14-block frame placed, lit with flint and steel, walked into; out of the overworld's portal; the End and back) |
| 4 | done 2026-10-04 | commanded by a player in chat (the e2e step `chat`: "robot pos", "robot come", "robot follow", "robot stop" in public chat, a dig whispered with /msg and answered the same way — a whisper of an unsigned command arrives as disguised chat; an unknown order) |
| 5 | done 2026-10-04 | the goals in ordinary terrain (the e2e scenario `survival`: its own server, seed 26265; found: the planner knew only the recipe book, which a new player's is all but empty of — data/recipe; it dug through rock it could not see and into pits; no room for a table among trees; a hut on a slope) |
| 6 | done 2026-10-04 | farming, a boat and a minecart, a bed (the e2e steps `farm`, `sleep`, `minecart`, `boat`; riding needed the extracted attachment points and, for the boat, the client's boat physics: no move corrected) |

## 6. The first days — the survival scenario goes on

The `survival` scenario continues in the world it built the hut in, the way a person's first
days go: light, a night with the monsters out, a way down to the ores, food. Each stage is judged
by the server before the next starts.

| order | task | what is asked |
|---|---|---|
| 1 | done 2026-10-05 | torches: coal from coal ore, or charcoal from logs smelted in a furnace it builds; the hut lit (found: the torch recipe comes to the book only with a stone pickaxe — crafted by hand; an ingredient out of reach — another option; the furnace's table ate the log to burn; a path round a hill needed more than 20000 places; the light predicate's key is "type" from 26.3) |
| 2 | [days-02-night.md](days-02-night.md) | a night on normal difficulty: in the closed, lit hut, fighting what gets near; alive at dawn |
| 3 | [days-03-mine.md](days-03-mine.md) | a staircase down, lit as it goes: iron, a furnace, an iron pickaxe; down to the deepslate and a diamond |
| 4 | [days-04-farm.md](days-04-farm.md) | the next day: seeds, a hoe, water by bucket, a field, wheat grown and harvested, bread baked and eaten |

Conventions for these files: public text — no private repositories, hosts or infrastructure of
the owner's deployments; the owner's own consumers are "the consumers". Dates are absolute.
