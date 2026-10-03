# The old fork

Status: **decision** — the owner's; the new repositories are public since 2026-10-03.

`go-mc26` is another approach, not a continuation of the owner's fork of Tnze/go-mc (decided
2026-09-05): it starts at Minecraft 26.1, carries code from Tnze/go-mc under its MIT notice
(`LICENSE`, the `COPIED` manifest), and has no fork history. Upstream is dormant (last commit
2024-12, the owner's pull requests for the previous line were closed unreviewed; issue #299
"26.2" has no reply).

## Steps

1. Replace the fork's `main` with this note and nothing else:

   > **go-mc (mj41)** — This fork is frozen and not maintained. The work continued as a new
   > project, [go-mc26](https://github.com/mj41/go-mc26): a generated Go library for Minecraft
   > 26.x with one branch per Minecraft version. Use that.

2. Repoint the consumers: imports to `github.com/mj41/go-mc26/...`, the `replace` directive
   gone, a tag pinned (`require github.com/mj41/go-mc26 v0.262.0`); build workflows and tools
   that track the fork's branch to the new repository and tag. Re-run their tests.
3. Optionally a one-line pointer to `go-mc26` on upstream issue #299.
4. Archive or delete the fork once nothing references it; deleting also removes its branch
   names from public view.
