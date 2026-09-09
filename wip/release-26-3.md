# Minecraft 26.3

Status: **open** — waits for Mojang; the release itself is the owner's push.

## When 26.3 is in the manifest

```bash
go run ./gen/cmd/mc26 update                  # picks the newest release, 26.3
```

`update` extracts, checks both schemas, prints the wire and registry diffs against 26.3-pre-3,
builds, verifies every extracted version against a vanilla server and commits data and library
locally (`docs/new-version.md`). It stops on a schema hole (the extractor's work) or a compile
error (`gen/src`'s work), then `update --version 26.3 --skip-extract` goes on. After it:
`mc26 release --version 26.3 --skip-extract --no-smoke --push` (the owner), and the examples'
`go.mod` bumped to `v0.263.0` (one `main` branch; the examples were identical across versions).

## Rehearsed

- 2026-09-05, 26.3-pre-2: the preview needed generator guards (`fixedBitSet`, resolvable
  numbers, the chunk packet's new shape); everything since is generated.
- 2026-09-09, 26.3-pre-3, the day it appeared: `update --pre --no-commit` extracted it, found
  all 400 schema entries described, one new packet (`add_transient_block`) and no layout or
  registry change against pre-2, built, and verified 26.1, 26.2, 26.3-pre-2 and 26.3-pre-3
  green: 9m30s, nothing touched by hand. Its data is in `mc26-data-pre` as `mc-26.3-pre-3` /
  `v0.263.0-pre3.0`.

## Afterwards

26.1 is the third maintained line until 26.4; then it freezes (its tags stay).
