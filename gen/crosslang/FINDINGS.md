# Can you write a codec from the JSON alone?

Experiment: build a Minecraft 26.2 packet codec in Python from six JSON files and
nothing else — no Go, no Java, no jar, no protocol knowledge pulled from memory —
then decode a capture of real packets and re-encode each one, byte for byte.

Everything below is what the JSON did or did not tell me, in the order it bit me.

## What was built

| file | what it is |
|---|---|
| `decode.py` | the codec: a generic reader/writer driven by node kind, ~1100 lines, no per-packet code |
| `gen_capture.py` | synthetic capture: makes up a value for every packet in `packets.json` and encodes it |
| `gen_deep.py` | forces the recursive `ref` corner (SlotDisplay inside SlotDisplay) that the ordinary generator avoids |
| `coverage.py` | which node kinds / primitives a capture actually exercises |
| `holes.py` | which packets the JSON cannot describe, and whether the hole is avoidable |
| `negcontrol.py` | negative control: does the round-trip test have teeth? |

The reader has one function per node kind and one per native. Nothing in it names
a packet. The only place a packet name appears at all is `packets.json` id lookup.

## Result

```
$ python3 decode.py --data /home/mj/work-mc/mc26/temp/data/26.2 \
                    --prims /home/mj/work-mc/mc26/gen/hand-crafted/prims.json \
                    --nodes /home/mj/work-mc/mc26/gen/hand-crafted/nodes.json \
                    --capture capture.jsonl
decoded 110 of 124 packets, 110 round-tripped byte for byte
```

The 14 that did not decode all failed for one reason: **NBT**. With an NBT reader
supplied from outside the permitted files (`--assume-nbt`, counted separately):

```
decoded 124 of 124 packets, 124 round-tripped byte for byte
(14 of those 124 needed the externally-supplied NBT reader,
 which the permitted JSON does not describe)
```

Every packet that decoded re-encoded to the identical bytes, and consumed the body
exactly — no leftovers, first try, no debugging of a wrong field width. The capture
is 124 packets / 51 distinct ids / 72 736 bytes of play-clientbound traffic,
including a 30 KB `commands` packet and 35 KB of `level_chunk_with_light`.

Two synthetic captures (765 and 60 packets, covering every packet in `packets.json`
and forcing the recursive corner) also round-trip 100%. Those are weaker evidence —
my writer and my reader share any misunderstanding — but they prove every node kind
and 44 of the 46 primitives are reachable and internally consistent.

## The verdict

**The claim is nearly true, and fails on one thing.** For the wire *structure* —
which bytes, in what order, how wide, which branch — the JSON was enough. I did not
once have to guess a field width, a prefix, or an order. Six things that a
reasonable person would have guessed wrong are written down explicitly and were
right (§2). That is a real result.

But it fails on **NBT**, and NBT is not a corner: it is 11% of the real capture and
16 of 232 packets can never be decoded without it. And it fails on `coverage:
"full"`, which is asserted about packets that are demonstrably incomplete (§1.2).
So: a working codec for most of the protocol, yes; a *complete* description, no.

---

## 1. What the JSON did not tell me

### 1.1 NBT — the one real hole (highest confidence)

Both authorities say the same sentence and then stop.

- `nodes.json` / `nbt`: *"Exactly one NBT tag in network form — a 1-byte tag id then
  that tag's payload with NO root name and no length prefix; tag id 0 (TAG_End) is
  the whole value and carries no payload."*
- `prims.json` / `NBT`: the same, in the same words.

Neither says what a tag id *is* (there is no id → kind table anywhere), nor what any
tag's payload looks like. Not the u16-then-bytes of a string, not the element-type +
i32-count of a list, not the id/name/payload/0x00 structure of a compound. `TEXT`,
`OPTIONAL_TEXT` and `OPTIONAL_NBT` all resolve to `NBT`, so this is not a rare leaf:

- 76 `prim TEXT` nodes, 12 `prim NBT`, 8 `OPTIONAL_TEXT`, 17 bare `nbt` nodes.
- 16 of the 232 packets have an NBT read on the mandatory path — they can never be
  decoded from the JSON, whatever the data: `system_chat`, `disconnect`,
  `set_title_text`, `server_data`, `player_chat`, `block_entity_data`, `open_screen`,
  `tag_query`, `tab_list`, `set_score`, `set_action_bar_text`, `set_subtitle_text`,
  `player_combat_kill`, `disguised_chat`, `test_instance_block_status`,
  and `custom_click_action` (for a different reason, §1.3).
- 16 more have one on some branch only.

**What I did:** by default `decode.py` raises a named error at the NBT byte and
reports the packet as undescribed, which is why the honest number is 110/124. I also
wrote an NBT reader from knowledge that is *not* in the permitted files, behind
`--assume-nbt`, and the program prints how many packets needed it, so the two
numbers can never be confused. That reader keeps every string as raw bytes rather
than decoding it, so it round-trips regardless of Java's modified-UTF-8.

**Confidence this is a genuine gap: certain.** I re-read both definitions twice
looking for a tag table. There is none, and `nbt_schema.json` — which by its name
might contain one — is outside the permitted set.

### 1.2 `coverage: "full"` is not a completeness claim (highest confidence)

This is the finding I would not have believed without catching it live.

At 07:07 during this session the extractor re-ran and `packet_schema.json` changed
underneath me (`_meta.json` extracted_at `2026-09-07T05:07:07Z`). In the *earlier* extraction, `clientbound/minecraft:boss_event` was:

```json
{"coverage": "full",
 "type": {"k":"struct","name":"ClientboundBossEventPacket","fields":[
   {"name":"id","type":{"k":"prim","t":"UUID"}},
   {"name":"enum","type":{"k":"enum","name":"ClientboundBossEventPacket$OperationType", ...}}]},
 "tokens": ["UUID", "enum:ClientboundBossEventPacket$OperationType"]}
```

Two fields, and `"coverage": "full"`. The real packet in the capture is 30 bytes:

```
5c2cc31fec97448daf43a44709549c1d 00 08000363617000000000060000
uuid (16)                        op  13 bytes the schema did not mention
```

The whole `OperationType` dispatch — the name Component, progress float, colour and
overlay enums, the flags byte — was simply absent, and the file asserted it was
complete. The current extraction has the dispatch and decodes correctly.

**Why this matters more than the individual bug:** the round-trip test caught it in
one run, from `17 of 30 bytes consumed, 13 left over`. A generator that trusts
`coverage` would have emitted a silently truncating codec, and a decoder that did
not check for leftover bytes would never have noticed. `PacketDecoder` on the Java
side throws on leftovers; per `nodes.json`, the Go library does not check. So this
class of defect can sit in a generated library indefinitely.

I cannot tell how many *other* packets are quietly in this state; `coverage` cannot
be used to find out, because it was "full" here. The only detector is real traffic
plus an exact-consumption check.

**Confidence: certain** — I have the old schema text, the bytes, and the byte count.

### 1.3 `optional{nbt}`: the two authorities contradict each other (high confidence)

`serverbound/minecraft:custom_click_action`, `"coverage": "full"`:

```json
{"name":"payload","type":{"k":"optional","elem":{"k":"nbt"}}}
```

`nodes.json` / `optional` says that exact construct is a lie: *"The sixth
(optionalTagCodec, :681) is mislabelled and has NO boolean byte at all; its single
use additionally sits inside a length prefix the extractor erases."*

So the schema says `bool, tag`, and the prose says `varint length, tag`. There is no
third source to break the tie, and the two differ in byte count from the very first
byte. This is the one place where I could not pick.

**What I did:** `decode.py` refuses `optional` whose `elem` is a bare `nbt` node with
a message naming the contradiction, rather than choosing. (The four `optional{prim
NBT}` nodes are *not* affected — `nodes.json` says those are genuinely
boolean-prefixed — and the code distinguishes them by node kind, which works because
the broken one is the single `nbt` node with no `java` key.)

**Confidence: high** that this cannot be resolved from the permitted files. I have
no real bytes for it (the capture is clientbound only), so I cannot say which is
right, only that the JSON asserts one thing and its own documentation asserts
another.

### 1.4 `opaque` — 54 admitted holes (as designed)

`opaque` is the extractor saying "I could not describe these bytes", and since it
carries no length, everything after it in the packet is lost too. 39 of the 54 are
`StreamCodec.recursive`, and the one that matters in practice is
`SlotDisplay$OnlyWithComponent.component`: the recipe packets decode fine until a
recipe uses that case, and then they are unreadable. `decode.py` raises at the node
rather than skipping, and my deep generator simply cannot build those cases.

This is honest — the JSON says it does not know — but it is worth stating as a
limit: `recipe_book_add`, `place_ghost_recipe` and `update_recipes` are decodable in
practice and undecodable in principle.

### 1.5 `conditional: true` — 9 structs whose branches are lost

`struct FilterMask` (in `clientbound:player_chat`) and eight `struct Path` (in the
four debug packets) list their fields and then flag that the extractor could not
work out which are present. `FilterMask` is on `player_chat`'s mandatory path, so
that packet is undecodable in principle even before its NBT. `decode.py` raises.

### 1.6 `enum` with no `values` — 4 nodes

The four `Orientation.fromIndex` nodes carry `{"k":"enum","java":...}` and no
constants. The wire form is still one VarInt, so a *byte-level* reader can survive —
but nothing says the arity, so a reader cannot validate and a generator cannot name
the constants. I raise, because `nodes.json` treats it as a hole; had the capture
contained a debug packet I would have relaxed it to "read a VarInt and report the
number", which I believe is correct.

### 1.7 Caseless `dispatch` — 4 nodes

`consume_effect_type` ×2 and the two `either`-keyed `DataComponentPredicate`. The key
is readable; the payload is not described at all. Raise.

### 1.8 The frame is in no JSON file at all

`packet_schema.json` describes the fields of a packet and nothing else. Length
prefix, compression, encryption, the packet-id VarInt, and how the state changes are
in `nodes.json`'s prose `frame` section only — which is documentation for a human,
not data. The capture handed me the body and the id already split out, so I did not
need it; but a program that had to read a socket could not be written from the JSON.
This also means `REST_BYTES` ("every byte remaining in the packet frame") is
*undefined* by the schema alone: its length comes from a frame the schema never
mentions.

### 1.9 Smaller things the JSON leaves to prose in `nodes.json`, not to data

These I got right, but only because I read the prose; a program consuming the JSON
mechanically would have to hard-code them:

- **`guard` linkage.** A struct's `guard` is a Java internal class name
  (`net/minecraft/.../ClientboundPlayerInfoUpdatePacket$Action`) and its
  string-`when` fields are selected by an `enumset` node *somewhere in an enclosing
  scope* whose `name` is the last `/`-segment of that class name. Nothing in the JSON
  links them; it is a string match described only in prose. Exercised 18 times on the
  real capture (10 fields present), so the rule is right.
- **Enum-dispatch cases have no `k`.** 13 case nodes in the file are
  `{"id","num","type"}` with no `k` key at all, while 925 are `{"k":"case","id","type"}`.
  You must detect them by key-set. My `decode()` special-cases this; without it, they
  fall through as an unknown kind.
- **`whilelist`'s `while` test kinds disagree.** `nodes.json` states the test *"is
  `bit` and only `bit`"* — but `prims.json`'s own `ENTITY_DATA` uses `{"field":
  "index", "test": "eq", "value": 255}`. A reader that took `nodes.json` literally
  would reject entity metadata. I implemented the general test evaluator, which is
  why both worked.
- **`packet_schema.json`'s `state` is not a connection state.** It carries pseudo-states
  (`common`, `cookie`, `ping`), so a packet must be resolved by `flow/name`, never by
  the `(state, flow)` you looked the id up under. `packets.json` lists 256
  `(state, flow, id)` triples; `packet_schema.json` has 232 entries, because
  `common` packets are shared.
- **Field names are not unique inside a struct**, so `when.field` means "the nearest
  *preceding* field with that name". `FloatArgumentInfo$Template` has two fields
  named `float` with different `in` tests. I index fields positionally and resolve
  `when` backwards; a name-keyed map would silently take the wrong one.
- **`when`'s `bit` `value` is a mask, not a bit index** (`commands` uses 8 and 16).
  30 KB of real `commands` data round-tripping is the proof I read it right.
- **Semantics that are not bytes.** `nodes.json` warns that an `enum`'s VarInt is the
  *declaration index* for the `readEnum` path but the enum's own stored id for the
  `idMapper` path, and that `minecraft:rabbit/variant` (EVIL: ordinal 6, id 99) and
  `minecraft:tropical_fish/pattern` (0, 256, 512 …) differ. This never touches a
  round trip — I keep the raw VarInt — but any generator that maps to constants gets
  those two wrong, and the schema records only order. Same class of problem: the
  `registry` label `"?"` names no registry at all.

---

## 2. What the JSON told me that I would otherwise have got wrong

Worth recording, because it is the positive half of the result. Every one of these is
stated explicitly in `prims.json` or `nodes.json`, and every one is a thing I would
have guessed wrong:

1. **`CHUNK_POS` puts z first.** `x` is the low 32 bits of the long, `z` the high —
   *"on the wire the four bytes of z arrive FIRST"*. The obvious guess is x-then-z.
2. **`OPTIONAL_NBT` has no boolean.** *"Byte-for-byte an NBT and NOT boolean-prefixed:
   absence is the single TAG_End byte 0x00."* Contrast `OPTIONAL_TEXT`, which *is*
   boolean-prefixed. The names give no hint.
3. **`OPTIONAL_VAR_INT` is `n-1`, not a boolean.** 0 means absent, n means n−1.
4. **`holder`'s `+1` is only on the direct form.** With `direct`: 0 = inline value,
   otherwise the id is n−1. Without `direct`: a plain id with no offset. Two nodes
   with the same kind and different arithmetic, distinguished only by the presence of
   a `direct` key. (My negative control shows the real capture would *not* have
   caught this: 5 direct-capable holders, all by reference, so the inline branch is
   untested by real bytes — see §3.)
5. **`holderset`'s `c == 1` is the empty set,** `c == 0` is the tag form, otherwise
   c−1 elements. Three meanings for one VarInt.
6. **`COMPONENT_PATCH` is not two lists.** Both var-int counts come first, *then* both
   runs — which is exactly why the `counted` node kind exists and why `list` would be
   wrong. `prims.json` says so in as many words.
7. **`enumset` has no length prefix**; its size is `ceil(len(values)/8)`, so `values`
   is load-bearing twice — for the bit order and for the byte count.
8. **`ENTITY_DATA`'s `while` is an exit condition, not a continue condition,** and the
   0xff terminator is itself an entry (with the value field suppressed by a `when`).
9. **`when`'s `cmp` `not` inverts the operator, not the expression.**
10. **`FIXED_BIT_SET`'s width comes from a `bits` sibling of `t` on the node**, not from
    a count on the wire.

Ten explicit saves. That is the substance of the claim, and it holds.

---

## 3. How much the result is worth (test power)

I ran a negative control (`negcontrol.py`) so the 110/110 is not taken for more than
it is:

```
(a) single-bit mutations: 118 rejected, 602 silently accepted
(b) baseline                      (124, 0)
    BYTE read as 2 bytes          (85, 39)   <- caught
    STRING len as u16, not varint (116, 8)   <- caught
    optional with no boolean byte (107, 17)  <- caught
    holder direct marker 1 not 0  (124, 0)   <- NOT caught
```

Deliberately breaking a field width, a length prefix, or the optional marker is
caught. Breaking the `holder` direct marker is **not**, because all 5 direct-capable
holders in the capture are by-reference. Most single-bit mutations are correctly
accepted — they land in payload bytes where any value is legal.

Coverage of the real capture, measured (`coverage.py`):

- 51 of 256 `(state, flow, id)` triples; **all `play`/`clientbound`**. Nothing from
  handshake, status, login or configuration, and nothing serverbound.
- 31 of 46 primitives. Never touched by real bytes: `CHUNK_POS`, `FIXED_BYTES`,
  `FIXED_BIT_SET`, `REST_BYTES`, `PUBLIC_KEY`, `ROTATION_BYTE`, `UNSIGNED_SHORT`,
  `ITEM_STACK`, `UNTRUSTED_ITEM_STACK`, `DELIMITED_COMPONENT_PATCH`, `JSON_TEXT`,
  `MESSAGE_SIGNATURE`, `OPTIONAL_NBT`.
- Node kinds never reached by real bytes: `either`, `ref`, `stringenum`,
  `lenprefixed`, `opaque`. The synthetic captures reach all of them, but only against
  my own writer.

So: strong evidence for the play-clientbound structure, no evidence at all for the
login/configuration handshake, for `either`, or for the delimited component patch.

---

## 4. Decisions I made where the JSON was silent, and how sure I am

| decision | why | confidence |
|---|---|---|
| Keep raw bytes for `STRING` rather than decoding to text | `prims.json` says "that many UTF-8 bytes"; the `max` caps are character counts and validation-only, so text is not needed to get the bytes right | high |
| Re-encode canonically (varint 0 → one byte, bool true → `0x01`) rather than replaying captured bytes | otherwise the round trip is vacuous — it would pass by construction. A non-canonical varint in a capture *should* be reported as a mismatch | high; nothing in the capture tripped it |
| `LP_VEC3` decoded to its raw wire fields (`b0`, `b1`, `u`, `h`) | `prims.json` defines only the *read* — the quantisation `q()` is not invertible from what is written, so there is no encode direction to follow. `lp_vec3_value()` computes the documented (x,y,z) for reporting | high that this is the only faithful choice; 8 real occurrences round-tripped |
| `BIT_SET` kept as the `LONG_ARRAY` it is defined to be, not as a bit set | `prims.json` notes the writer trims trailing empty words, so decoding to a bit set and re-encoding could legally change the byte count | high |
| `bits` (`BLOCK_POS`, `CHUNK_POS`) kept as the raw long, with the fields computed alongside | the packing is exactly recoverable, so this is lossless; unpacking to signed fields and repacking would be too, but adds a chance to get the sign extension wrong for no gain | high |
| A hole raises only when actually *reached*, not when present in the tree | a `SlotDisplay` `opaque` sits in one case out of 37; refusing the whole packet would lose the 99% that decode | high |
| `enum` decoded as a raw VarInt, never mapped to a constant | `nodes.json` documents that the number is the declaration index for some enums and a stored id for others, and the schema does not distinguish them | high — this is why the round trip is immune to a documented defect |
| Floats decoded to values and re-packed rather than kept as bytes | honest test; the widen-to-double/narrow-back path is exact for every value except possibly a signalling NaN payload | high for real data, would note if a NaN ever appeared |

---

## 5. Reproducing

```
cd <this directory>
D=/home/mj/work-mc/mc26/temp/data/26.2
P=/home/mj/work-mc/mc26/gen/hand-crafted/prims.json
N=/home/mj/work-mc/mc26/gen/hand-crafted/nodes.json

python3 decode.py --data $D --prims $P --nodes $N --capture capture.jsonl
python3 decode.py --data $D --prims $P --nodes $N --capture capture.jsonl --assume-nbt
python3 gen_capture.py --data $D --prims $P --nodes $N --out synth.jsonl --reps 3
python3 decode.py --data $D --prims $P --nodes $N --capture synth.jsonl --assume-nbt
python3 gen_deep.py && python3 decode.py --data $D --prims $P --nodes $N --capture synth_deep.jsonl --assume-nbt
python3 coverage.py capture.jsonl
python3 holes.py
python3 negcontrol.py
```

Input versions this was run against (`packet_schema.json` changed once mid-session,
see §1.2 — these are the later, current files):

```
efd621067c84426b1ebaad9cc34e7fd1  packet_schema.json      (extracted_at 2026-09-07T05:07:07Z)
27dec3c1f99dafac6529fd7cf29feef0  packets.json
5a487f5d35f50a473927d0721847fe78  registries.json
a0777727385677744693123c79cfcdf9  entity_data.json
8fac058606283f466b59e95494aa423b  prims.json
af664b1b9d10aab388fad3fb51cfae87  nodes.json
279da892bcacc63ba51e96b400979238  capture.jsonl
```

---

## 6. Disclosure — what I read

Only the six permitted files were read for protocol content:
`packet_schema.json`, `packets.json`, `registries.json`, `entity_data.json`,
`prims.json`, `nodes.json`. Nothing under `gen/src`, `gen/internal`, `data-gen`,
`temp/lib`, and no jar or disassembly. No `go`, `javap` or `schemacov` was run.

Two deviations, both disclosed rather than hidden:

1. **`_meta.json`** (5 lines of version metadata: version id, protocol number, jar
   URL and sha1, extractor commit, extraction timestamp) — read once, *after*
   `packet_schema.json` changed underneath the experiment, to establish which
   extraction I was looking at. It contains no protocol description. Cited in §1.2
   and §5.
2. **The NBT tag format**, behind `--assume-nbt`, is knowledge from outside the
   permitted files. It is isolated in one clearly-marked block of `decode.py`, it is
   off by default, and every run that uses it prints how many packets depended on it.
   It exists so the size of the gap could be measured rather than merely asserted:
   without it 110/124, with it 124/124.

---

## 7. Since then (2026-09-08)

The experiment above was run once, on 26.2, against a capture of play-clientbound
traffic. `decode.py` is now what `mc26 crosscheck` runs on every version, against a
session recorded by a proxy (all four states, both directions, 86–89 state/flow/id
triples, ~290 packets), and it passes on 26.1, 26.2 and 26.3-pre-2: every packet
decoded and re-encoded byte for byte, with the Go library required to consume each
one exactly on the way. The companion scripts (`gen_capture.py`, `holes.py`,
`negcontrol.py`, …) were not kept; the decoder was. What the findings look like now:

| finding | state |
|---|---|
| §1.1 NBT not described | **closed** — `prims.json`'s `NBT` is a `native` definition that carries the tag table: every tag id, its payload layout, and what `EndTag`, lists and compounds do, each read from the `Tag` classes' `load`/`write` bytecode. `decode.py` reads NBT from that table; `--assume-nbt` is gone. |
| §1.2 `coverage: "full"` on incomplete packets | **closed as a class** — the cross-check requires exact consumption on both sides, and both sides run on every version; six wire bugs were caught that way on 2026-09-08 alone (movement packets, light data, chunk sections, enum ids, …). `coverage` itself still only says what the extractor believed. |
| §1.3 `optional{nbt}` contradiction | **closed** — serverbound `custom_click_action` is `lenprefixed{nbt}`: the var-int byte count, then the tag. |
| §1.4 `opaque` holes (54) | **closed in packets and components** — 0 opaque nodes in the `packets` and `components` sections of all three versions (`recursive` codecs are interpreted; the registry dispatches have bootstrap rules). The `structs` section, which lists helper readers by name, still carries 8–13 for readers whose count is out of band, and those are described in `prims.json` instead (`CHUNK_SECTIONS`). |
| §1.5 `conditional` structs (9) | **one left** — `FilterMask` in `player_chat` of 26.1 and 26.2 (a switch on an enum inside a reader); 26.3-pre-2 has none. |
| §1.6 `enum` without `values` (4) | **closed** — the `Orientation` id map is a `registry` node labelled `Orientation`, which is what it is: an id space that is not an enum. |
| §1.7 caseless `dispatch` (4) | **closed** — every dispatch of the three versions has cases (`consume_effect_type` has a bootstrap rule, and the either-keyed predicate dispatch is gone from the schema: its key and payload are read as fields). |
| §1.8 the frame is prose | **open** — `nodes.json`'s `frame` is still text; the recording proxy is written from it, which is a check of the text, not data. |
| §1.9 `guard` linkage by name | **open** — still a string match described in prose. |
| §1.9 enum-dispatch cases without `k` | **closed** — every case node says `"k":"case"`. |
| §1.9 `whilelist` test kinds | **closed** — `nodes.json` describes the general test. |
| §1.9 pseudo-states in `state`, non-unique field names, `bit` value as mask | **open**, and documented in `nodes.json` as rules a reader must follow. |
| §1.9 enum ids that are not ordinals | **closed** — an `enum` node carries `ids` when its numbers are not 0..n-1 (three in 26.3-pre-2), or `idsUnknown` when they could not be read. |
| §1.9 `registry` label `"?"` | **closed** — the stat value is a dispatch on `stat_type` whose cases name their registry; the display slot is the `DisplaySlot` enum. |
| §3 coverage of the real capture | play-clientbound only then; now every state, both directions, and the chunk sections down to the palettes and packed longs (a section with a hash-map palette and a two-biome section are built by the traffic test). A global palette (more than 256 block states in one section) is not provoked; its width is checked only against the formula. |
