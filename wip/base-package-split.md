# Is go-mc26 the binding only?

Status: **decision** — the owner's; deferred by the owner on 2026-09-08 until the generators
were finished, which they are.

## The question

`docs/hand-written.md` sorts what is still hand-written into tiers. Tier A is what any binding
of the protocol needs: the primitives, the node kinds, the frame, NBT, the registry container,
the bridges. Tier B is an application on top: the bot (its event model, world view, screens),
the server framework, the world-file readers, the account flows. Should `go-mc26` ship only
Tier A and the generated packages, with Tier B in a second module or repository?

## What speaks for a split

- The library's promise stays "the protocol and the data as Go types"; the client and the server
  are opinions on top and change for reasons that have nothing to do with a Minecraft version.
- Another binding (a second language, a second Go client) depends on Tier A alone.
- `bot` and `server` are the only hand-written packages a version bump has to touch (they read
  generated fields); in their own module that work is visibly separate from the generated one.

## What speaks against

- One more repository and one more tag to pin; the examples would depend on both.
- `bot` is what makes the library usable in an afternoon; a reader who finds only packets and
  registries has to look for the client.
- Nothing forces the decision: the packages already have that layering inside one module.

## If yes

`go-mc26` keeps `protocol/`, `data/`, `level/`, `nbt/`, `net/`, `registry/`, `chat/`, `wire/`,
`management/`, `version`; a new module (`go-mc26-client`, say) takes `bot/`, `server/`, `save/`,
`yggdrasil/`, `microsoft/`, `offline/`, built by the same `build` from the same `gen/src` with
a second output tree, the same branch and tag scheme, the smoke and e2e tests moving with it.
