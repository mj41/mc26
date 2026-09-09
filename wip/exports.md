# Exports of the schema to other formats

Status: **research** — open; the owner's idea of 2026-09-09.

The schema is a protocol description nobody else has: derived from the jar, checked against real
sessions and a reader written from the JSON alone. Other ecosystems describe the same protocol in
their own schema languages, by hand. An export from ours to theirs gives them derived definitions
and gives this project further independent consumers, and, the owner's point, people who care
about the same thing.

What was compared, what the comparison found, why our names stay as they are, and what an export
would take: [docs/minecraft-data.md](../docs/minecraft-data.md). `gen/cmd/protodefdiff` is the
comparison.

## Steps, if the owner wants this

1. Report the layout errors the comparison found upstream, one issue per packet, with the
   bytecode as evidence (`gen/cmd/javap`).
2. `mc26 export --format <theirs> --version V`, with an alias table for names computed by
   matching packet ids; check the result with their client against a vanilla server.
3. The read-only export for the format with the web IDE and the hex view.
4. Publish `mc26-data` first ([publish-repositories.md](publish-repositories.md)): nothing above
   is visible to anyone until then.

Our JSON stays the source; exports are consumers, checked like the Go reader is.
