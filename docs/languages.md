# Languages

go-mc26 has English built in. Every other language of a Minecraft version is outside the library, and a
program takes the ones it wants: some, all, or none. Decided by the owner on 2026-10-12; the design's
measurements and alternatives are in the git history of `wip/lang.md`.

## What is where

| where | what |
|---|---|
| go-mc26 `data/lang/en_us.lang` | English, embedded, parsed the first time a string is asked for (`lang.English()`) |
| go-mc26 `data/lang/manifest_gen.go` | `lang.Manifest`: the checksum (SHA-256) of every language of the version, by locale; `lang.Version` |
| go-mc26 `data/lang` (hand-written, `gen/src`) | the format (`Parse`, `ParseJSON` for the game's own `.json`), the registry (`Register`, `Get`, `Locales`) |
| go-mc26 `cmd/langget` | puts the languages a project lists into one of its packages, by their checksums |
| go-mc26-lang branch `mc-<version>` | one module per language but English: `cs-cz/` is module `github.com/mj41/go-mc26-lang/cs-cz`, package `cs_cz` with `Data`, `Locale`, `Version`, `Sum` |
| go-mc26-lang tags `<lang>/v0.<YYN>.<n>` | a language's module at a version (`cs-cz/v0.263.0`), made only when its file changed since its last tag of the line |
| go-mc26-lang branch `blobs` | every file of every version by its checksum (`<sha256>.lang`), only ever added to: what langget fetches |

A `.lang` file is a language's keys and strings, sorted by key, each followed by a NUL byte; the game's
positional placeholders (`%2$s`) are written as Go's (`%[2]s`). en_us is 478 KB; 143 languages were 142 MB
of Go map literals in the library before (and gopls on the 26.3 library 1.52 GB, now 0.85 GB).

## In a program

English needs nothing. Another language is registered, and `lang.Register` takes its file only when its
checksum is the one `lang.Manifest` has for the locale: a file of another Minecraft version is refused, with
what to do, rather than half used.

- **A module**, for a language or two:

  ```
  go get github.com/mj41/go-mc26-lang/cs-cz@v0.263.0
  ```

  ```go
  lang.MustRegister(cs_cz.Locale, cs_cz.Data)
  chat.SetLocale(cs_cz.Locale)
  ```

- **langget**, for a list a project keeps (or "all"): the file `mc26-langs` in a package's directory lists
  locales (`MC26_LANGS` overrides it); the package holds

  ```go
  //go:generate go run github.com/mj41/go-mc26/cmd/langget
  ```

  and `go generate` writes `<locale>.lang` and `langs_gen.go`, which registers them when the package is
  imported. A language no longer listed is removed; with no list the package has English alone. The files
  come from go-mc26-lang's blobs, or `-from <dir>` with no network. A project may commit what langget
  writes, or keep it and `mc26-langs` out of git and run `go generate` where it builds; after an upgrade to
  another Minecraft version, `go generate` again.

A key the chosen language lacks is English's, then the component's fallback, then the key itself
(`chat`, as the game looks it up).

## From the earlier library

Until 2026-10-12 the library had a package per language. What changes for its callers:

| before | now |
|---|---|
| `en_us.Map` (`data/lang/en-us`) | `lang.English()` (`data/lang`) |
| `import _ ".../data/lang/zh-cn"` | `go get github.com/mj41/go-mc26-lang/zh-cn@v0.263` and `lang.MustRegister(zh_cn.Locale, zh_cn.Data)`, or `zh_cn` in `mc26-langs` and langget |
| `chat.SetLanguage(xx_yy.Map)` | `chat.SetLocale("xx_yy")` |

English renders byte for byte as it did: `chat`'s TestEnglishGolden checks every key against the earlier
build's renders (`chat/testdata/en_us_<version>.tsv.gz`).

## Releasing

`mc26 build` writes en_us and the manifest into the library. `mc26 lang --version V` writes the other
languages as modules into `temp/lang/V`; with `--repo ../go-mc26-lang` it commits them there (the branch
`mc-V`, the new files on `blobs`) and tags each language whose file changed. Nothing is pushed: pushing the
branch, `blobs` and the tags is a step of its own, and must come before a go-mc26 release whose manifest
names those files, so that langget and the modules can find them.

Mojang changes a version's translations without a new version (2026-10-06 it refreshed 26.1, 26.2 and 26.3
alike; the three share every translation but English), and `mc26 release` extracts afresh. So the modules
come from data as Mojang has it now, a fresh extraction or mc26-data's latest, and `mc26 release --push`
checks before it pushes anything that go-mc26-lang's `blobs` (`--lang-base`) has every language of its own
extraction, and stops otherwise with what to run. A change of translations is a new file on `blobs`, a new
tag of each changed language (`cs-cz/v0.263.1`) and a new go-mc26 release.
