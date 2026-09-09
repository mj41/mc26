# Work in progress

One task per file, its status on the first line (`open`, `decision`, `deferred`, `done`). The
order below is the order to do them; a group is what they have in common. A finished task stays
here as its record until the docs carry everything worth keeping, then it goes.

## 1. Decisions — the owner's

| order | task | what is asked |
|---|---|---|
| 1 | [publish-repositories.md](publish-repositories.md) | create the five GitHub repositories, re-tag, first push |
| 2 | [old-fork.md](old-fork.md) | the note on the old fork, the consumers repointed, the fork archived |
| 3 | [base-package-split.md](base-package-split.md) | is `go-mc26` the binding only, with the client and server elsewhere? |

## 2. Next

| order | task | what is asked |
|---|---|---|
| 4 | [release-26-3.md](release-26-3.md) | Minecraft 26.3 through `mc26 update`, then `release --push` |

## 3. Deferred

| task | why it waits |
|---|---|
| [multi-version.md](multi-version.md) | a bot that speaks two Minecraft versions at once: no need yet |

## 4. Done — the record

| task | done |
|---|---|
| [generation.md](generation.md) | 2026-09-09: every packet, component, registry and shared type generated; what stays hand-written and why |
| [automation.md](automation.md) | 2026-09-09: `verify`, the schema check in every build, `update`, `nbtdiff` |
| [repositories-and-versioning.md](repositories-and-versioning.md) | 2026-09-05: the repositories, the branch and tag scheme, no backward compatibility, the workflows |

Conventions for these files: public text — no private repositories, hosts or infrastructure of
the owner's deployments; the owner's own consumers are "the consumers". Dates are absolute.
