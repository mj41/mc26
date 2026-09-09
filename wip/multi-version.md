# One binary, two Minecraft versions

Status: **deferred** — until a bot must serve two versions at once.

Packet ids, registries and packet layouts are package-level constants and generated types, so
one build is one Minecraft version. A real need exists only in a migration window (a test
server on 26.3 while another is still on 26.2), and the cheap answer covers it: build the bot
twice, against two library tags. The full answer — data tables keyed by protocol version and
per-version packet packages behind a version switch — is an order of magnitude more work in the
generators and the framework, for a window of a few days per release.
