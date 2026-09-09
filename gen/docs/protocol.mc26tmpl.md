# The protocol as the schema describes it

<!-- This file is a template: gen/docs/protocol.mc26tmpl.md, rendered by `mc26 docs` for one
Minecraft version into the data repository. Directives are HTML comments; see
gen/internal/docgen. Prose is written here; tables and trees come from the JSON. -->

Minecraft <!-- mc26 value: id --> (<!-- mc26 value: name -->), protocol <!-- mc26 value: protocol -->, data version <!-- mc26 value: data-version -->.

<!-- mc26 include: version-facts -->

`packet_schema.json` describes every packet, and every data component an item stack can carry,
as a tree of nodes; the leaves are named primitives. This document says what each node kind and
each primitive is on the wire, and what the frame around a packet is, which the schema never
mentions. With it, the JSON is enough to write a reader in any language: `gen/crosslang` in the
project repository is such a reader, written from these files alone, and every packet of a
recorded session round-trips through it byte for byte.

## The frame

A connection carries a stream of self-delimiting frames. Without compression a frame is a
length, a var int of one to three bytes, followed by exactly that many bytes of body; the body is
the packet id, a var int, then the packet's fields as the schema's node tree describes them. The
length counts the id and the fields, never itself; it must not be zero and cannot exceed 2097151,
since the reader copies at most three length bytes (21 data bits).

Once compression is on, one field joins the frame: the length, then a data length (a var int),
then the payload. A data length of zero marks the payload as a plain uncompressed body; any other
value is the byte count the payload inflates to, the payload then being a zlib stream (RFC 1950
framing, default level, one stream per packet) whose plaintext is again the packet id and the
fields. The length still counts everything after it. The sender compresses when the whole body,
id and fields together, is at least the threshold the server announced.

Encryption changes no framing: from the moment the key is installed, every byte of the stream in
each direction goes through AES-128/CFB8 with no padding, the key and the initialisation vector
both being the 16-byte shared secret, one long-lived cipher per direction with no per-packet
reset. The length prefix is enciphered too, so a reader deciphers before it can find a frame
boundary.

Nothing in the frame names the state: the packet id is an index into a dense, zero-based table
chosen by state and direction, and the connection moves from handshake to status or login, from
login to configuration, from configuration to play and back, by swapping that table when a
designated packet crosses.

<!-- mc26 include: frame -->

### In more detail

length (var int; 1-3 bytes and 1..2097151 as enforced by Mojang, unbounded in Go): the byte count that follows it in this frame. Varint21FrameDecoder.copyVarint loops `for (i=0; i<3; i++)` and throws CorruptedFrameException("length wider than 21-bit") on a 4th byte; decode throws CorruptedFrameException("Frame length cannot be zero") on 0, resetReaderIndex()es until readableBytes >= length (so a frame may span TCP reads), then readBytes(length); Varint21LengthFieldPrepender.encode throws EncoderException when VarInt.getByteSize(len) > 3.

data length (var int, present only while compression is on): 0 = the rest of the frame is a plain uncompressed body; non-zero = the plaintext size of the zlib payload. Written by CompressionEncoder.encode as VarInt 0 or VarInt(readableBytes); read by CompressionDecoder.decode.

payload: raw body (data length 0, or no compression) or a zlib stream. the inflated-length check is ONE-SIDED, not an equality check on both ends — CompressionDecoder.inflate allocates directBuffer(dataLength), calls Inflater.inflate once into it and throws DecoderException only when the bytes produced are FEWER than dataLength; a stream that would inflate to more is silently truncated to dataLength and only shows up later (or not at all) via PacketDecoder's leftover check.

threshold (int, not on the wire; carried once by clientbound login packet minecraft:login_compression, login id 3, as a single VAR_INT — confirmed in packet_schema.json: one field compressionThreshold, {k:prim,t:VAR_INT}): Mojang compresses iff (id+fields) >= threshold; 0 compresses everything; negative disables and removes both handlers (Connection.setupCompression). the server enables compression only when getCompressionThreshold() >= 0 AND !connection.isMemoryConnection(). Server-side receive validation (CompressionDecoder built with validateDecompressed=true, from setupCompression(threshold, true) in the login listener's send-listener): data length < threshold is a DecoderException, and so is data length > 8388608; the vanilla client passes false and skips both — but the Go client does NOT skip them (packet.go:180,183).

maximum body size (8388608, MAXIMUM_UNCOMPRESSED_LENGTH): CompressionEncoder.encode throws IllegalArgumentException only when readableBytes > 8388608 (8388608 itself is legal); CompressionDecoder.decode applies it only while validating. MAXIMUM_COMPRESSED_LENGTH = 2097152 is declared but never loaded by any code in the class (the only ldc in CompressionDecoder is 8388608) — confirmed; the effective cap on a compressed frame is the 21-bit `length`.

packet id (var int, first bytes of the body / of the decompressed plaintext): index into the per-(state,flow) table. IdDispatchCodec.decode rejects id < 0 or id >= byId.size() with DecoderException; encode looks the type up in an Object2IntMap (default -1 -> EncoderException) then VarInt.write. Ids are the map size at insertion in IdDispatchCodec$Builder.build (putIfAbsent; a duplicate is IllegalStateException), so dense and 0-based per state and direction — confirmed against packets.json: all nine tables are exactly 0..n-1 (handshake sb 1, status 2/2, login cb 6 / sb 5, configuration 20/10, play cb 141 / sb 69).

fields: everything after the id, decoded by that state's StreamCodec — the part packet_schema.json describes. PacketDecoder.decode throws IOException when readableBytes > 0 after the codec, so there is no trailing padding (Go does not enforce this: Packet.Scan, packet.go:30-39, never checks that the fields consumed all of p.Data).

state (not on the wire after the handshake): handshake/status/login/configuration/play (ConnectionProtocol ids read as the ldc'd strings "handshake","play","status","login","configuration"). Only the handshake carries an explicit selector: serverbound minecraft:intention (sole handshake packet, id 0) ends with ClientIntent written as a var int, 1=STATUS, 2=LOGIN, 3=TRANSFER. those are the enum's `id()` values (private ConstantValues STATUS_ID=1, LOGIN_ID=2, TRANSFER_ID=3, and byId's tableswitch 1..3), NOT ordinals — the ordinals are 0/1/2 and are never written; ClientIntentionPacket.write does writeVarInt(intention.id()). Later switches are implicit, on terminal packets: login -> configuration on serverbound login_acknowledged (login sb 3); configuration -> play on finish_configuration (configuration id 3 both ways); play -> configuration on clientbound start_configuration (play cb 118) answered by serverbound configuration_acknowledged (play sb 16).

flow (not on the wire): serverbound/clientbound (PacketFlow ids "serverbound","clientbound"), each with its own table and its own swap — handleIntention calls setupOutboundProtocol(StatusProtocols.CLIENTBOUND) before setupInboundProtocol(StatusProtocols.SERVERBOUND), so the outbound table can already be in the new state while the inbound one is not.

pipeline order: setEncryptionKey addBefore("splitter","decrypt") and addBefore("prepender","encrypt"); setupCompression addAfter("splitter","decompress") and addAfter("prepender","compress"); configureSerialization addLast splitter, FlowControlHandler, inbound handler, prepender, outbound handler. So inbound decrypt -> splitter -> decompress -> decoder, outbound encoder -> compress -> prepender -> encrypt: length measures post-compression, pre-encryption bytes and encryption is outermost.

in-memory (single-player) connections swap the length prefix for LocalFrameEncoder/LocalFrameDecoder — they do not merely pass ByteBufs through, they wrap and unwrap with HiddenByteBuf.pack/unpack, and createFrameDecoder returns a third class, MonitoredLocalFrameDecoder, when a BandwidthDebugMonitor is present.

<!-- mc26 internal -->
### Where it lives in this repository

Neither the extractor nor the generator emits or renders the frame: it is exactly what the JSON never mentions. Extractor data-gen/java/GenPacketSchema.java: 44-51 the eight PacketTypes holder classes each labelled with a state string; 202 reads PacketType.flow(), 203 reads PacketType.id() (that id() is the ResourceLocation NAME, not a number), 207 builds the key "<flow>/<name>"; 295 writes "state". A grep of the whole file for Connection|Compression|Cipher|Varint21|protocol_id|packets.json returns nothing, so it never touches the frame machinery. Its "state" is not a ConnectionProtocol: the emitted values are play 188, common 19, configuration 9, login 9, cookie 2, ping 2, status 2, handshake 1 — the pseudo-states common/cookie/ping are real, as claimed. Generator gen/internal/generate/gen_packets.go: genPackets at :260 reads packet_schema.json at :262 and also reads packets.json at :268-271 (as packetsReport, to enumerate names per state/flow; it still writes no ids); :301-303 skips the handshake ("the framework writes it by hand"); the goType switch is at :819 and its cases (prim, string, unit, nbt, registry, resourcekey, holderset, holder, list, whilelist, ref, optional, map, either, enum, stringenum, enumset, dispatch, struct) contain no frame kind. The id table is rendered by gen/internal/generate/gen_packetid.go: PacketsJSON with field protocol_id at :15-17, genPacketID at :41-101, writing <goMCRoot>/data/packetid/packetid.go from gen/templates/packetid.go.tmpl. that package is not in this checkout — gen/src/data holds only README.md and registryid — so the id table exists only after generation. The frame itself is hand-written Go under gen/src/net.

### In Go

pk.Packet{ID int32; Data []byte} at gen/src/net/packet/packet.go:13-17 (MaxDataLength = 0x200000 at :11). Read by (*Packet).UnPack :117 -> unpackWithoutCompression :125 / unpackWithCompression :155; written by (*Packet).Pack :46 -> packWithoutCompression :54 / packWithCompression :72 (compressPacket :104 uses compress/zlib, matching Deflater's zlib framing). Length and id var ints are pk.VarInt: ReadFrom at gen/src/net/packet/types.go:292, WriteTo :254, WriteToBytes :263, MaxVarIntLen = 5 at :85. Wrapper net.Conn in gen/src/net/conn.go: ReadPacket :222, WritePacket :227, SetCipher :232 (cipher.StreamReader/StreamWriter over the socket, exactly Mojang's CipherDecoder/CipherEncoder), SetThreshold :247, threshold -1 = off (:37, :214). AES-CFB8 in gen/src/net/CFB8/cfb8.go (NewCFB8Decrypt :18, NewCFB8Encrypt :22), driven from gen/src/bot/login.go:262-274 with the 16-byte secret as both key and IV and installed at :170; SetThreshold is called at login.go:98 on clientbound login_compression. The handshake packet is generated like every other (protocol/handshake, packetid.ServerboundHandshakeIntention, types.ClientIntent 1/2/3). Where the Go library and Mojang disagree, seven places: (1) Length prefix on read — Go uses the generic VarInt reader and applies no 21-bit rule; confirmed, and its `if num > MaxVarIntLen` at types.go:297 does admit a 6th byte, but so does Mojang's VarInt.read (it accumulates, then tests i > 5 before testing the continuation bit) — the real difference is that Mojang always throws RuntimeException("VarInt too big") once a 6th byte is consumed while Go returns a value if that 6th byte terminates (and its shift of 35 on a uint32 discards those bits). (2) Decompressed cap — Mojang 8388608, Go MaxDataLength 2097152 at packet.go:183: confirmed, Go rejects compressed packets a vanilla server considers legal. (3) compression boundary — Mojang compresses iff idLen+fieldsLen >= threshold, Go iff fieldsLen >= threshold (packet.go:78), so at the boundary Go leaves uncompressed what Mojang would deflate. (4) Go has no zero-length check; unpackWithoutCompression reads the packet id straight from the stream after the length (packet.go:125-137), so length 0 or a short length silently desynchronizes the stream instead of raising Mojang's "Frame length cannot be zero". (5) unpackWithCompression caps PacketLength not at all before io.CopyN(buff, r, PacketLength) at packet.go:170 — a 5-byte length is honoured. (6) the Go write path enforces neither the 3-byte/21-bit length cap (Mojang: EncoderException) nor the 8 MiB uncompressed cap (Mojang: IllegalArgumentException). (7) Go's client applies the data-length < threshold check at packet.go:180 that vanilla's client disables (validateDecompressed=false); harmless against a vanilla server, an error against a peer that compresses below threshold. Plus a non-frame one: Mojang forbids leftover bytes after a packet's fields (PacketDecoder IOException), Go's Packet.Scan does not check.
<!-- mc26 end -->

## Primitives

The schema's leaves. A definition is a node tree in the schema's own vocabulary, a `bits`
layout inside one integer, or a native every language implements in its runtime; a definition
may name another primitive, which a reader resolves recursively. The definitions are the
`prims` section of `packet_schema.json`, read by the extractor from the jar member the name
stands for (`gen/hand-crafted/prims.json` says which), the same way it reads any packet; the
natives are what the buffer itself does; four are still written by hand, where the bytecode of
the reader does not say what the count or the width is (the chunk sections, the two paletted
containers, the entity data). The uses column counts how often this version's packets and
components name the primitive. A primitive this version has no member for is left out.

<!-- mc26 include: prims -->

### Notes on the primitives

What each one is, beyond its definition. Where a note names a version, that is where the fact
was checked.

- `BIT_SET` — Byte-for-byte a LONG_ARRAY (var-int word count, then that many big-endian longs), interpreted as java.util.BitSet.valueOf(long[]): bit i is bit (i mod 64) of word i/64 counting from the least significant bit, and the writer trims the word count to the last word that has a bit set.
- `BLOCK_POS` — One big-endian signed 64-bit long packing three two's-complement fields: y in bits 0-11, z in bits 12-37, x in bits 38-63 (read from BlockPos.getX/getY/getZ over the long).
- `BOOL` — A single byte: 0 means false and any non-zero byte means true, and true is written as 1.
- `BYTE` — One byte read as a signed two's-complement value in -128..127.
- `BYTE_ARRAY` — A var-int count then exactly that many raw bytes; a node's `max` only caps the count and is not on the wire - note Mojang checks only count > max, not count < 0, so a generated reader must reject a negative count itself.
- `BYTE_BIT_SET` — Byte-for-byte a BYTE_ARRAY (var-int byte count, then that many bytes), interpreted as java.util.BitSet.valueOf(byte[]): bit i is bit (i mod 8) of byte i/8 counting from the least significant bit, and the writer trims the count to the last byte that has a bit set. Not the same wire form as BIT_SET, which is a long array; 26.3 moved the chunk light masks from one to the other, so a version may use either or both.
- `CHUNK_POS` — One big-endian signed 64-bit long with x in the low 32 bits and z in the high 32 bits (read from ChunkPos.unpack) - so on the wire the four bytes of z arrive FIRST, then the four bytes of x.
- `CHUNK_SECTIONS` — The chunk data of level_chunk_with_light: a var int byte count, then that many bytes holding one LevelChunkSection per 16 blocks of the dimension's height, back to back and nothing else. The section count is not in the packet - the client takes it from the dimension type it was sent at configuration time - which is why this is `rest` inside the window rather than a list: reading sections until the bytes run out gives the same result. Byte-identical across 26.1, 26.2 and 26.3-pre-2 (fluidCount is in all three).
- `COMPONENT_PATCH` — Two var-int counts first, then positiveCount components (each a component type id and the value in that type's own codec) and then negativeCount component-type ids — it is NOT two `list`s, because both counts precede both runs. The guard the reader puts on both runs (either count non-zero) changes nothing on the wire: a run of zero is empty either way.
- `CONTAINER_ID` — A plain var-int with no bias or offset - the token only records that the number identifies an open container.
- `DELIMITED_COMPONENT_PATCH` — COMPONENT_PATCH with every added value preceded by its length in bytes, so a reader can step over a component it does not know.
- `DOUBLE` — Eight big-endian bytes reinterpreted as an IEEE-754 double-precision float.
- `ENTITY_DATA` — Entries of { unsigned-byte field index, var-int serializer id, value in that serializer's codec } repeated UNTIL an index byte of 0xff, which is the terminator and carries nothing after it; the `while` clause is the schema's end-of-list condition, not a continue condition. The serializer ids and their value types are the serializers list of entity_data.json.
- `FIXED_BIT_SET` — A bit set whose size the packet already knows: ceil(bits/8) bare bytes with no count, LSB-first within each byte.
- `FIXED_BYTES` — A block of exactly `len` bytes with no length prefix; all six occurrences in the 26.2 schema are len 256, the chat message-signature block, and several are conditional fields (present only when the preceding packed id says so).
- `FLOAT` — Four big-endian bytes reinterpreted as an IEEE-754 single-precision float.
- `GAME_PROFILE_PROPERTIES` — A var-int count of at most 16 properties (the count is read with that cap; the list carries it as `max`), each a name string (max 64 chars), a value string (max 32767 chars) and a boolean-prefixed optional signature string (max 1024 chars); every string is a var-int BYTE length then UTF-8, and each max is a character cap.
- `IDENTIFIER` — Exactly a `string` (var-int byte length + UTF-8), cap 32767 characters, whose text is a namespaced id "namespace:path"; a string with no ':' (or a leading ':') takes the default "minecraft" namespace.
- `INSTANT` — An ordinary big-endian signed 64-bit long holding milliseconds since the Unix epoch (Instant.ofEpochMilli).
- `INT` — Four bytes, most significant first, read as a signed two's-complement 32-bit value.
- `ITEM_STACK` — Byte-for-byte OPTIONAL_ITEM_STACK with the empty encoding forbidden: the count is always at least one, so the item id and the component patch always follow. The creative mode slot packet sends UNTRUSTED_ITEM_STACK instead, which delimits its components.
- `JSON_TEXT` — An ordinary `string` read - var-int byte length + UTF-8 bytes - whose text is then parsed as JSON; `max` on the prim node is the character cap given to lenientJson (262144 for login disconnect, 32767 for the status response).
- `LONG` — Eight bytes, most significant first, read as a signed two's-complement 64-bit value.
- `LONG_ARRAY` — A var-int count followed by that many big-endian signed 64-bit longs; the decoder rejects a count larger than readableBytes()/8.
- `LP_VEC3` — Minecraft's low-precision packed vector: a 1-byte zero marker, else 6 mixed-endian bytes holding three 15-bit quantised components plus 3 scale/flag bits, optionally followed by a VarInt carrying the rest of the scale.
- `MESSAGE_SIGNATURE` — A 256-byte signature block with no length prefix; the 26.2 extractor never emits this token - the codec field it is keyed on does not exist, and signatures reach the schema as FIXED_BYTES len 256 through the inlined reader.
- `NBT` — One NBT tag in network form: a 1-byte tag id followed by that tag's payload with NO root name; id 0 (TAG_End) is the entire value, and whether that means "absent" or is a decode error depends on the reader (FriendlyByteBuf.readNbt returns null; ByteBufCodecs.TAG/COMPOUND_TAG throw). The payload of every tag id is in the tags table of the definition, so a reader needs nothing else: the format is self-describing and recursive, which is why it stays native.
- `OPTIONAL_ITEM_STACK` — A var-int count; when it is <= 0 the stack is empty and nothing else is read, otherwise a var-int item registry id and a COMPONENT_PATCH follow.
- `OPTIONAL_ITEM_STACK_LIST` — An ordinary var-int-counted list whose elements are OPTIONAL_ITEM_STACK, so empty stacks may appear inside it; no wire-relevant maximum.
- `OPTIONAL_NBT` — Byte-for-byte an NBT and NOT boolean-prefixed: absence is the single TAG_End byte 0x00 that NBT already allows, and when a tag is present it must be a TAG_Compound (anything else is a decode error).
- `OPTIONAL_TEXT` — A boolean; when true one TEXT (a network NBT tag) follows, when false nothing follows — genuinely boolean-prefixed, unlike OPTIONAL_NBT.
- `OPTIONAL_VAR_INT` — A single var-int where 0 encodes absence and n > 0 encodes the value n - 1. The entity-data serializer OPTIONAL_BLOCK_STATE is named with this token too, and there it is wrong: that one writes the state id itself with 0 for absent, no n - 1.
- `PALETTED_BIOMES` — As PALETTED_BLOCK_STATES over 64 entries (a 4x4x4 grid of biomes per section): 0 = single value; 1, 2, 3 = a linear palette at that many bits; 4 and above = global at ceillog2(number of biomes), 7 for the 65-67 vanilla biomes of 26.1-26.3 but a property of whatever biome registry the server synchronised. Identical in 26.1, 26.2 and 26.3-pre-2.
- `PALETTED_BLOCK_STATES` — One byte selects the palette and the storage width: 0 = a single value (one var int block state id, no data longs); 1-4 = a linear palette (var int count, then that many block state ids) stored at 4 bits per entry whatever the byte said; 5-8 = a hash-map palette (same bytes as linear) at that many bits; 9 and above = the global palette (nothing) at ceillog2(number of block states) bits, 16 in 26.3-pre-2 and 15 in 26.1 and 26.2. Then the 4096 entries packed into longs, see `packed`. The writer emits the storage width as the byte (PalettedContainer$Data.write writes storage.getBits()), so vanilla never sends 1-3 for block states, but a reader must take them as 4. Identical in 26.1, 26.2 and 26.3-pre-2.
- `PUBLIC_KEY` — A var-int length (the reader rejects above 512) then that many bytes, holding the DER X.509 SubjectPublicKeyInfo encoding of an RSA public key; the writer imposes no cap of its own.
- `REGISTRY_KEY` — One IDENTIFIER naming the registry itself (e.g. "minecraft:block"); the ResourceKey's root-registry parent is a static field in Java and never travels.
- `REST_BYTES` — The raw remainder of the buffer after the fields before it, with no length prefix; it is opaque bytes - when a channel is one the server knows (minecraft:brand carries a STRING) that structure is inside the blob, not described by this token.
- `ROTATION_BYTE` — One signed byte covering a whole turn in 256 steps: the angle in degrees is b * 360 / 256 (b is signed, so -128..127 covers -180..+179.3).
- `SHORT` — Two bytes, most significant first, read as a signed two's-complement 16-bit value.
- `STRING` — Var-int BYTE count followed by that many UTF-8 bytes; where a cap applies it is a CHARACTER count (the byte length is rejected above 3x the cap before reading, the decoded String.length() above the cap after), and in the 26.2 schema no prim STRING node carries a cap at all - the separate `string` node kind is what carries `max`.
- `TEXT` — A chat component carried as exactly one network NBT tag (parsed with NbtOps, the codec is ComponentSerialization.CODEC): a TAG_String for a plain literal, a non-empty TAG_List of components, or a TAG_Compound — TAG_End is rejected, and there is no length prefix on the codec field itself.
- `TYPED_DATA_COMPONENT` — A var-int data_component_type registry id, then the value in that component type's own stream codec, with no length prefix: a struct of the type and a dispatch on it. The cases are the components section of packet_schema.json, keyed by the same registry name, which casesFrom points at. It is marked recursive because several components (container, bundle_contents, charged_projectiles, use_remainder) hold item stacks, which hold a component patch again: the format recurses, so a reader has to stop inlining here and recurse at run time instead.
- `UNSIGNED_BYTE` — The same single byte as BYTE, but interpreted as unsigned 0..255.
- `UNSIGNED_SHORT` — The same two big-endian bytes as SHORT, interpreted as unsigned 0..65535; the writer truncates the int to 16 bits.
- `UNTRUSTED_ITEM_STACK` — OPTIONAL_ITEM_STACK with the delimited component patch: a count of at most zero is an empty stack and nothing follows it; the item is a holder of the item registry (a var-int id).
- `UUID` — Sixteen bytes: the high 64 bits then the low 64 bits, each a big-endian signed long - the same as writing the 128-bit value big-endian.
- `VAR_INT` — Little-endian base-128: each byte carries seven data bits, the high bit says another byte follows, and a value is at most five bytes; negative numbers are the 32-bit two's-complement pattern, so they always take five bytes.
- `VAR_INT_ARRAY` — A var-int count followed by that many var-ints; the no-arg overload caps the count at readableBytes().
- `VAR_INT_LIST` — Identical on the wire to VAR_INT_ARRAY - a var-int count then that many var-ints - differing only in the Java container (IntList rather than int[]).
- `VAR_LONG` — The same base-128 scheme as VAR_INT but over 64 bits and at most ten bytes; negative numbers are the 64-bit two's-complement pattern and always take ten bytes.

## Node kinds

<!-- mc26 include: node-kinds -->

### bits

**On the wire.** Nothing new: exactly the bytes of the primitive named by `of`. `bits` adds only an interpretation - how to cut named runs of bits out of that integer - so byte for byte a `bits` node and {"k":"prim","t":<of>} are the same value. The runs tile the whole width, so packing the fields back gives the integer that was read.

**Keys.** `bits` = width of the underlying integer (8, 16, 32 or 64); `of` = the primitive to read ("LONG", "VAR_INT", "UNSIGNED_BYTE", "BYTE"); `name` = the Java class the packing was read in, on nodes the extractor emits; `fields` = array of {name, offset, width, signed?}, offset counted from the LEAST significant bit, `signed` meaning the run is two's complement. A guard or a count reaches one run by a dotted name, "flags.stepCount", which is the field holding the integer and the run's name (see `when` and `counted`). BLOCK_POS: x offset 38 width 26 signed, z offset 12 width 26 signed, y offset 0 width 12 signed. CHUNK_POS: x offset 0 width 32 signed, z offset 32 width 32 signed - so z's four bytes arrive first.

<!-- mc26 internal -->
**Where it comes from.** Extractor: putBitField, from bitField reading the body of a static helper of one integer (ClientboundMoveEntityPacket.unpackStepCount is `packed >>> 1`, unpackOnGround is `(packed & 1) != 0`, ArgumentUtils.numberHasMin is `(flags & 1) != 0`). The integer's node is turned into a `bits` node in place, so guards and counts already pointing at it keep pointing at the same field, and the helper's name gives the run's name (unpack/get/is/read/has stripped). A helper that tests the whole value rather than a run of it stays a `pred`. In 26.3-pre-2 the packets with one are move_entity_pos, move_entity_pos_rot, move_player_pos, move_player_pos_rot, move_player_rot, move_player_status_only and commands. It is also defined twice in gen/hand-crafted/prims.json, for BLOCK_POS and CHUNK_POS, which the Go side reads with hand-written types instead.

**In Go.** A generated struct with one Go field per run - bool for a single bit, int32 or int64 otherwise - named after the Java class (MoveEntityPosPacked{OnGround bool, StepCount int32}). ReadFrom reads the integer and cuts the runs out of it; WriteTo packs them back and writes the integer, so the value round-trips. bitsType (gen_packets.go) refuses a packing whose runs do not tile the width, since bits no field accounted for would be lost on the way back. Guards and counts render as `$.Flags.StepCount` through fieldRef.
<!-- mc26 end -->

### case

**On the wire.** Nothing of its own. When the dispatch key equals this case's id, the bytes after the key are exactly what this case's `type` node describes — no tag, no length, no padding. A `type` of `{"k":"unit"}` means the key is followed by nothing at all for that case.

**Keys.** Two shapes, both with `k`:"case". (1) Registry-dispatch case: `{"k":"case", "id":"<ns>:<name>", "type":<node>}` — 958 in 26.3-pre-2 (920 in 26.1, 944 in 26.2), key-set exactly {id,k,type}. `id` is namespaced but NOT always `minecraft:` — the extractor prepends `minecraft:` only when the registration name had no colon, and 6 of the 215 distinct ids are `brigadier:` (bool/float/double/integer/long/string). It carries NO number: the VarInt that travels must be looked up in registries.json under `minecraft:<registry>` → entries[id].protocol_id. (2) Enum-dispatch case: `{"k":"case", "id":"CONSTANT", "num":<int>, "type":<node>}` — 18 in 26.3-pre-2 (13 in 26.1 and 26.2), key-set exactly {id,k,num,type}; `id` is the bare constant name and `num` its ordinal position. (Until 2026-09-08 this shape had no `k` and a reader had to recognise it by its key set.) Registry cases appear in bootstrap registration order and enum cases in ordinal order; in 26.2 every registry dispatch happens to have one case per registry entry in ascending protocol_id order (57/57 command_argument_type, 16/16 debug_subscription x2, 3/3 number_format_type, 125/125 particle_type, 2/2 position_source_type, 5/5 recipe_display, 11/11 slot_display), but index by id/num rather than position.

<!-- mc26 internal -->
**Where it comes from.** Extractor: GenPacketSchema.java:832 (registryCases — `node("case", "id", …, "type", …)`), :899 (registryMethodCases, same shape), enumDispatchCases (k, id, num, type). Generator: gen_packets.go:1211-1270 inside unionType (reads c["id"], c["num"], c["type"]), grouped and rendered into the `fields()` switch at :1303-1346.

**In Go.** No type of its own; the case's `type` is folded into the enclosing union struct. `unit` contributes no fields (the generated switch collects those ids into one `return nil, nil // no data` arm, :1337-1339). A `struct` is flattened field-by-field, EXCEPT a struct whose fields carry guards, which is kept as one nested field of its own type (:1236-1242) — e.g. ArgumentTypeInfo.Float is FloatArgumentInfoTemplate. Anything else (list, ref, prim, enum) becomes one field named after the case id suffix. A case the generator cannot type becomes a Hole and gets its own `return nil, fmt.Errorf(...)` arm (:1341-1343); an id absent from the switch falls to `"<registry> %d: unknown id"` (:1345).
<!-- mc26 end -->

### counted

**On the wire.** Exactly N repetitions of `elem` with NO count of its own in front of them; N is the value of an earlier sibling field of the enclosing struct, named by `count`. It exists because a count is not always adjacent to what it counts: DataComponentPatch writes both of its counts before either run, and VecDelta.read(buf, stepCount) is handed its count by the packet that called it.

**Keys.** `count` = the name of an earlier field of the same struct holding the repetition count, or a dotted name for one run of bits of a packed one ("flags.stepCount", see `bits`), as the schema names it before any naming override (the same rule `when.field` follows); `elem` = the node repeated. No max, no prefix of its own. A `counted` with no `count` is a hole: hasHole reports it, so a repetition nobody could name never passes as a full packet.

<!-- mc26 internal -->
**Where it comes from.** Extractor: collapseLoop's third case, for a loop whose bound is a PassedV — a value the caller read and passed in. The node is created without its `count` and recorded in pendingCounts; attachCounts names it in the struct where both the repetition and its count are fields, which for a spliced-in reader is the caller's. In 26.3-pre-2 that is move_entity_pos and move_entity_pos_rot (VecDelta.read); 26.1 and 26.2 have none. It also appears four times in gen/hand-crafted/prims.json, in the `added` and `removed` runs of COMPONENT_PATCH and DELIMITED_COMPONENT_PATCH, which the Go side reads with hand-written types.

**In Go.** types.Counted[T, *T] (gen/src/wire/wire.go), a plain slice of the element. The count is not part of the type: fieldsOf puts `types.CountedOf(&$.Field, int($.Count))` in the field's Ref, and fieldRefs writes that into the pk.Tuple in place of `&p.Field`, so the count is read by the time the repetition is. countedOf.ReadFrom reads exactly that many elements and refuses a negative count; WriteTo refuses a slice whose length disagrees with the count field, which would produce a packet nothing could read back.
<!-- mc26 end -->

### dispatch

**On the wire.** The dispatch key, then — with no separator, length prefix, tag or padding — the whole payload of the codec that key selects; the union itself contributes no terminator and no size. A key of kind `registry` is a single VarInt (ByteBufCodecs$28.decode = `VarInt.read(buf)` then `byId.apply`; ByteBufCodecs$29.decode = `VarInt.read(buf)` then `getRegistryOrThrow(...).byIdOrThrow`). Both `enum` keys in 26.2 are also a single VarInt, but by two different routes: TrackedWaypoint$Type through `FriendlyByteBuf.readEnum` (= `getEnumConstants()[readVarInt()]`) and ItemAttributeModifiers$Display$Type through `ByteBufCodecs.idMapper(BY_ID, Type::id)`, which is the same ByteBufCodecs$28 VarInt. An `either` key is one boolean byte (ByteBufCodecs$26.decode = `readBoolean()`; true = left) followed by the VarInt id from the left or the right registry.

**Keys.** `k`:"dispatch", `name`, `key` always; `cases` optional — 26.2 key-sets are exactly {cases,k,key,name}x54 and {k,key,name}x4. `name` is a NAMING HINT ONLY, never on the wire. It is the short name of the class whose bytecode the extractor was reading, except that a dispatch built by a static codec field of a bootstrap class is named after what that field's generic signature says travels (ParticleTypes.STREAM_CODEC is a StreamCodec<RegistryFriendlyByteBuf, ParticleOptions>, so `ParticleOptions`; NumberFormatTypes gives `NumberFormat`). `key` IS on the wire and comes first: kind `registry` (with `registry`, the name without the `minecraft:` prefix), `enum` (with `name` and `values` in ordinal order), or `either` (with `left`/`right`, each a nested node). `cases` is a description of the payloads, not bytes. Counts (packets and components): 26.1 and 26.2 have 58 dispatch nodes, 55 keyed by a registry and 3 by an enum; 26.3-pre-2 has 60, 55 by a registry and 5 by an enum; every one has `cases` (the caseless consume_effect_type of an earlier reading has a bootstrap rule, and the either-keyed predicate types are read as fields). No dispatch is keyed by an either any more.

<!-- mc26 internal -->
**Where it comes from.** Extractor: GenPacketSchema.java:608 (StreamCodec.dispatch; registry cases attached at :623-625, enum cases at :633-636, a getter of an element that carries no codec of its own is asked of each element as its bootstrap constructed it — Stat's cases — and a `dispatch` with no cases whose getter builds the same shape for every element is replaced by a plain struct of the key and that shape instead), :1978 (an enum constant holding its own reader — `type.constructor.apply(...)`, cases at :1979-1980), :2000 (a registry element whose method reads its own payload — `info.deserializeFromNetwork(buf)`, cases from registryMethodCases at :860), and SwitchWalk in interpretReader (a `switch` on the ordinal of an enum just read — FilterMask.read in 26.1 and 26.2 — walked arm by arm: the case of each constant is what its arm read, unit when nothing, which is the same dispatch the 26.3 codec form gives). Generator: gen/internal/generate/gen_packets.go:959-971 (goType `case "dispatch"`; hand-kept short-circuit at :960-962, `dispatch %v` error at :970), unionType :1178-1272, renderUnions :1277-1352.

**In Go.** A generated struct named by unionName (gen_packets.go:1171-1177, which applies naming_overrides type_names), with first field `Type` (`const keyField = "Type"`, :1208) — `pk.VarInt` for a registry key, or the generated ordinal enum (`type X pk.VarInt` with a `ReadFrom` delegating to `(*pk.VarInt)`, renderEnums :1786-1801) for an enum key — followed by the flattened fields of every case. ReadFrom is generated at :1347: read `v.Type`, then `v.fields()` (a `pk.Tuple` from a switch on Type, :1319-1346) and read that tuple. pk.VarInt.ReadFrom is gen/src/net/packet/types.go:292; pk.Tuple.ReadFrom is gen/src/net/packet/util.go:266. All 9 unions 26.2 actually generates land in temp/lib/26.2/protocol/types/structs_gen.go — none in level/component or wire.
<!-- mc26 end -->

### either

**On the wire.** One boolean byte, then exactly one of the two branches: non-zero selects `left`, zero selects `right`. Never both, nothing else — no length, no tag beyond that byte. ByteBufCodecs$29.decode and FriendlyByteBuf.readEither both read one boolean and then one side. 

**Keys.** `left` and `right`, both nodes, both required — 7/7 have exactly {k, left, right}, but they are NOT all in packets: 2 in packets (clientbound:server_links either{enum ServerLinks$KnownLinkType, TEXT}, clientbound:waypoint either{UUID, STRING}) and 5 in components (can_place_on, can_break x2 each, profile). No count, no max, no discriminator field — the boolean is the whole discriminator.

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:680 (ByteBufCodecs.either), :2022 (FriendlyByteBuf.readEither). generator gen_packets.go:928-937.

**In Go.** wire.Either[L, *L, R, *R] — gen/src/wire/wire.go:409 (struct{IsLeft bool; Left L; Right R}); ReadFrom :415 reads pk.Boolean then the selected side, WriteTo :430 writes pk.Boolean(IsLeft) then the selected side.
<!-- mc26 end -->

### enum

**On the wire.** One VarInt, and nothing else. What the number MEANS depends on the codec: for the readEnum path it is the ordinal (FriendlyByteBuf.readEnum = getEnumConstants()[readVarInt()], writeEnum = writeVarInt(ordinal())), and for the ByteBufCodecs.idMapper path it is an id the constant carries, which is not always the ordinal. A third route reads the same one VarInt: FriendlyByteBuf.readById(X.BY_ID) where BY_ID = ByIdMap.continuous(X::id, values(), …) (DisplaySlot in set_display_objective), and the number is that id. A node whose numbers are not the ordinals says so in `ids`.

**Keys.** `name`: the Java SHORT class name only (shortName(), so "Rabbit$Variant", never the package - a second-language generator cannot resolve the class from the schema). `values`: constant names in declaration order. `ids`: the number each constant travels as, in the same order, present only when those are not 0..n-1. Three in 26.3-pre-2: EquipmentSlot [0,5,1,2,3,4,6,7] (MAINHAND, OFFHAND, FEET, LEGS, CHEST, HEAD, BODY, SADDLE), Rabbit$Variant [0,1,2,3,4,5,99] (EVIL is declared seventh and travels as 99) and TropicalFish$Pattern [0,256,512,768,1024,1280,1,257,513,769,1025,1281] (packedId = base.id | index << 8). `idsUnknown`: true when the codec is an id mapper whose numbering could not be read; a hole, so that nothing silently assumes the ordinal. `java` (only when `values` is null): the id function, for an id map over something that is not a Java enum - Orientation, whose 48 entries are computed and have no names anywhere in the jar. Worth recording: ByIdMap.continuous(..., WRAP) and ByIdMap.sparse(..., DEFAULT) mean an unknown id does not fail on the Java side - it wraps or falls back - which a second generator will not reproduce by accident.

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java enumNode (name and values) and enumWireNode (the numbering): idMapperOf reads the enum's own <clinit> for a ByteBufCodecs.idMapper, and enumWireIds loads the class and asks each constant for the field the mapper's function returns - by reflection, since TropicalFish$Pattern computes its number in the constructor rather than taking it as an argument. An id function that is `ordinal` means the ordinal and adds nothing. Call sites: codecOfOwner (an enum's own stream codec), idMapper (an id map over a lambda's owner), buf.readEnum (the ordinal, always). generator gen_packets.go goType "enum" -> gs.enum with the ids, renderEnums writes them as explicit constant values instead of iota; an `enumset` or a dispatch key of such an enum is refused, since a bit per ordinal and a case per ordinal would both be the wrong number.

**In Go.** A generated named type `type X pk.VarInt` with ReadFrom/WriteTo delegating to pk.VarInt, and constants that are `= iota` when the ordinal is the number and explicit values when it is not. Each also gets Count() int, which wire.EnumSet needs. Before the ids were read the constants were always iota, so an item component carrying a rabbit variant of EVIL, a tropical fish pattern past the first, or any equipment slot but MAINHAND was read and written with the wrong number.
<!-- mc26 end -->

### enumset

**On the wire.** A fixed bit set of exactly ceil(N/8) bytes with no count and no length prefix, N being the number of constants — the size comes from the enum's arity, which both peers must know. Bit i is bit (i mod 8) of byte i/8 counting from the least significant bit (java.util.BitSet.valueOf(byte[]) order). The one 26.2 node has 8 constants, so it is exactly one byte.

**Keys.** name and values only (the single node carries exactly {k, name, values}). ClientboundPlayerInfoUpdatePacket$Action with ADD_PLAYER, INITIALIZE_CHAT, UPDATE_GAME_MODE, UPDATE_LISTED, UPDATE_LATENCY, UPDATE_DISPLAY_NAME, UPDATE_LIST_ORDER, UPDATE_HAT — the "actions" field of clientbound:player_info_update, where it also guards which parts of each list entry follow. values is load-bearing twice over: it fixes the bit order and, through its length, the byte count. Because the bit index is the declaration index, the idMapper id problem noted under enum does not apply here — readEnumSet indexes getEnumConstants() directly.

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:2013 (buf.readEnumSet; there is no ByteBufCodecs factory for it); generator gen_packets.go:952-958 (goType) with the guard wiring at :441-467.

**In Go.** wire.EnumSet[X] over the generated ordinal enum — type at gen/src/wire/wire.go:323 (a []byte), ReadFrom at :325 (io.ReadFull of exactly (Count()+7)/8 bytes), WriteTo :332, Has :341, Set :347; the EnumType constraint at :317 is what forces the generated enum to expose Count().
<!-- mc26 end -->

### guard

**On the wire.** Not a node kind — a key ON A STRUCT node holding the internal name of a Java enum. It means: which of this struct's fields are on the wire is decided by an EnumSet of that enum carried EARLIER in the same packet, and each guarded field says which constant selects it via its string `when`. On the wire the selector is that EnumSet: a fixed bit set of ceil(constants/8) bytes with no length prefix, least-significant bit of the first byte = ordinal 0 (FriendlyByteBuf.readEnumSet → readFixedBitSet(values.length) → Mth.positiveCeilDiv(n,8) bytes → BitSet.valueOf). The guarded fields then appear, for each entry, in ENUM DECLARATION ORDER, because Java iterates an EnumSet in ordinal order, skipping every constant not in the set. Unguarded fields (no `when`) are always present, at their place in the field order.

**Keys.** `guard` — the enum's internal name, in 26.2 `net/minecraft/network/protocol/game/ClientboundPlayerInfoUpdatePacket$Action`. It pairs with the string form of `when` on that struct's fields. It carries no count and no ids: the constants and their order come from the `enumset` node elsewhere in the packet whose `name` is the same SHORT class name (values ADD_PLAYER, INITIALIZE_CHAT, UPDATE_GAME_MODE, UPDATE_LISTED, UPDATE_LATENCY, UPDATE_DISPLAY_NAME, UPDATE_LIST_ORDER, UPDATE_HAT — 8 constants, so 1 byte). Consecutive fields sharing the same `when` constant are one group read together; a constant may own more than one field (ADD_PLAYER owns both `name` and `properties`), so the nine string `when`s cover eight actions, not nine.

<!-- mc26 internal -->
**Where it comes from.** Extractor: GenPacketSchema.java:1894-1946 expandEnumReaders — it walks the enum's <clinit>, interprets each constant's reader lambda (the one whose descriptor ends `…FriendlyByteBuf;)V`), splices that reader's fields into the builder struct with `g.put("when", constant)` (:1919) in constant-construction order (= ordinal order), and records `builder.put("guard", enumClass)` (:1931). Generator: gen_packets.go:441-471 (guardHook — finds the packet's `enumset` field whose name matches the guard class's short name, and fails with `guarded entries without an EnumSet<…> field` if there is none); the hook is armed at :579-582 for a `list` whose `elem` carries a guard; read/write emitted by renderGuarded :1616-1690.

**In Go.** The guarded struct becomes a separate `<Packet>Entry` type in the packet's own package with ALL fields present in Go, plus `func (e *XEntry) fields(guard types.EnumSet[types.PlayerInfoUpdateAction]) pk.Tuple` which appends `&e.F` unconditionally for an unguarded field and inside `if guard.Has(<Enum><Const>) { … }` for each `when` group (:1634-1650). The list field itself is a PLAIN Go slice `[]PlayerInfoUpdateEntry`, not wire.List: the packet's ReadFrom (:1656-1672) reads the fields before the list, then a bare `pk.VarInt` count, then `entry.fields(p.Actions).ReadFrom(r)` per entry, then the fields after. wire.EnumSet is gen/src/wire/wire.go:322-330 — ReadFrom at :325 is `io.ReadFull` of ceil(Count/8) bytes with no length prefix; Has (:341) tests bit i%8 of byte i/8.
<!-- mc26 end -->

### holder

**On the wire.** Without "direct" (holderRegistry, 127 nodes): one VarInt registry id, no offset — holderRegistry delegates to the same private registry(key,Function) → ByteBufCodecs$29 as the registry kind. With "direct" (63 nodes, ByteBufCodecs$30): one VarInt n; n == 0 means the element's own encoding follows inline, otherwise the id is n-1 and nothing follows. The +1 is on the id only in the direct form; the encoder writes getIdOrThrow()+1 for a reference and 0 then directCodec.encode for a direct value.

**Keys.** registry (always) and direct (optional, a full node subtree). No other keys — the 190 nodes carry exactly {k,registry} or {k,direct,registry}. Counts confirmed: 190 holder nodes, 63 with direct (trim_pattern 38, sound_event 15, chat_type 3, trim_material 2, and one each of dialog, instrument, jukebox_song, banner_pattern, painting_variant); largest direct-less group item 88. 62 of the 63 directs are structs, but one is not — clientbound:show_dialog's direct is an nbt node (Dialog.DIRECT_CODEC), so a reader must not assume "direct" is always a record.

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:671 (holderRegistry → holder without direct) and :672 (holder → holder with direct); generator gen_packets.go:863-871, where `if d, ok := n["direct"]` picks between the two Go types.

**In Go.** With direct: wire.Holder[D, *D] — type at gen/src/wire/wire.go:115, ReadFrom at :120 (reads ID, returns immediately unless ID == 0, only then reads Direct), WriteTo at :128. Without direct: plain pk.VarInt (types.go:292), with the comment "holder in <registry>" (that comment form is used for the direct branch and for holderset).
<!-- mc26 end -->

### holderset

**On the wire.** One VarInt c. c == 0 is the tag form: an Identifier (VarInt byte length + UTF-8) follows and the set is that tag. Otherwise exactly c-1 elements follow, each a plain VarInt registry id with no offset. So c == 1 is the empty explicit set. The decoder pre-sizes its list with min(c-1, 65536) but still reads all c-1 elements — there is no cap on the wire.

**Keys.** registry only — all 11 nodes carry exactly {k, registry}; no elem, no max. Distribution confirmed: item x3 (recipe_book_add, update_recipes, component repairable), block x3 (can_place_on, can_break, tool), damage_type x3 (damage_resistant, blocks_attacks x2), entity_type x1 (equippable), banner_pattern x1 (provides_banner_patterns).

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:673 (ByteBufCodecs.holderSet); generator gen_packets.go:861-862.

**In Go.** wire.IDSet — type at gen/src/wire/wire.go:138 (Tag pk.Identifier, IDs []pk.VarInt), ReadFrom at :143 (count==0 → read Tag and set IDs=nil; else make(count-1) and read each), WriteTo at :165 (writes 0+Tag, or len+1 then the ids). IDs == nil means the tag form.
<!-- mc26 end -->

### lenprefixed

**On the wire.** A var int giving the number of bytes that follow, then one `elem` inside exactly those bytes. The length is what lets a reader step over a value it cannot decode, so reading the value must not run past the window and must not leave part of it unread. Emitted for ByteBufCodecs.lengthPrefixed and its registry-aware form.

**Keys.** `elem` = the node inside the window. Two shapes: with no `length` key the var int byte count is on the wire immediately in front of the window (ByteBufCodecs.lengthPrefixed - serverbound custom_click_action); with `length` naming an earlier sibling field, that field already carried the count and nothing precedes the window (prims.json DELIMITED_COMPONENT_PATCH). The maximum Java checks is not recorded.

<!-- mc26 internal -->
**Where it comes from.** GenPacketSchema.java, the lengthPrefixed cases of applyFn; gen_packets.go goType case "lenprefixed"

**In Go.** wire.LenPrefixed[T, *T] in gen/src/wire/wire.go
<!-- mc26 end -->

### list

**On the wire.** A var int count, then exactly that many `elem` values back to back, nothing between and nothing after. the count is ALWAYS a var int and always immediately precedes the elements for every `list` node in 26.2. Two caveats. (1) The representation FORCES adjacency: collapseLoop removes the count node by identity and appends the list where the loop body began (:1494-1499), so a bytecode loop whose count was read earlier with intervening reads would be silently reported as adjacent. (2) `list` is not the only repetition in the file: readFixedSizeLongArray is N longs with N out of band and the extractor renders it as a single LONG (structs/PalettedContainer.read), so absence of a `list` does not mean absence of repetition.

**Keys.** `elem` = the node repeated (required, 187/187). `max` = an integer cap on the count, 13/187 — 5 in packets (64,100,128,256,256) and 8 in components (4,64,64,100,256,256,256,1024). `max` is NOT on the wire: ByteBufCodecs.readCount reads the var int and throws DecoderException only when `count > max`; a NEGATIVE count passes Mojang's check. writeCount throws EncoderException when size > max. Absent `max` = 2147483647 on the ByteBufCodecs path, no cap at all on FriendlyByteBuf.readCollection/readList. The 65536 in the decoders is only the initial collection capacity, Math.min(count, 65536). Also: some real caps are recorded NOWHERE — ClientboundLevelChunkPacketData's reader throws RuntimeException above 2097152 bytes, and FriendlyByteBuf.readPublicKey rejects above 512; neither reaches the schema.

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:663 (collection(fn, elem[, max])), :660 (list() -> FnV), :1095 (applyFn FnV "list"), :1103 (applyFn LambdaV list/collection), :1496 (a bytecode read-loop bounded by a just-read VAR_INT — the function is collapseLoop at :1463-1508, NOT "collapseCounted at 1460-1506" as previously written), :2006 (FriendlyByteBuf.readList/readCollection). generator gen_packets.go:872-883.

**In Go.** wire.List[T, *T] — gen/src/wire/wire.go:25; ReadFrom :27 (pk.VarInt count, rejects count < 0 with io.ErrUnexpectedEOF, then `make([]T, int(count))` and count element reads), WriteTo :47 (pk.VarInt(len) then the elements). pk.VarInt.ReadFrom at gen/src/net/packet/types.go:292. Note the allocation happens before any element is read, so a hostile count near 2^31 allocates before failing. `max` is never consulted: grep shows gen_packets.go reads n["max"] for no kind at all (only gen/cmd/schemacov/main.go:185 prints it), so the generated Go enforces no maximum anywhere.
<!-- mc26 end -->

### map

**On the wire.** A var int count, then that many entries, each the key immediately followed by the value, no separator and nothing after. Order on the wire is the encoder's java.util.Map iteration order; a reader must not assume sorting, and duplicate keys collapse on the Java side.

**Keys.** `key` and `val` (nodes, both required, 19/19: 13 packets + 1 structs + 5 components). `max` on 4, all in packets: 32, 128, 256, 256 — the same decoder-side cap as on `list` (readCount throws only on count > max, writeCount on size > max), never on the wire; absent means 2147483647 on the ByteBufCodecs path and no cap on FriendlyByteBuf.readMap. The Go generator ignores `max`. Note `key` need not be a scalar: components/minecraft:can_place_on has a map whose key is an `either`.

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:668 (map(fn,key,val[,max])); :667 pushes FnV("map", null) for the 1-arg form, which applyFn cannot consume (see problems). :2008 (FriendlyByteBuf.readMap — key is the second-to-last argument, val the last). generator gen_packets.go:918-927.

**In Go.** wire.Map[K, *K, V, *V] — gen/src/wire/wire.go:70; it is []wire.Entry[K,V] (Entry at :63), so it keeps wire order rather than becoming a Go map. ReadFrom :72 (pk.VarInt count, rejects count < 0, then per entry key.ReadFrom then val.ReadFrom), WriteTo :95.
<!-- mc26 end -->

### nbt

**On the wire.** Exactly one NBT tag in network form — a 1-byte tag id then that tag's payload with NO root name and no length prefix; tag id 0 (TAG_End) is the whole value and carries no payload. The node kind means "an NBT tag that a Mojang Codec then parses"; the parsed object's structure is not in the schema. Where the value is required, ByteBufCodecs$16 (tagCodec) throws on TAG_End ("Expected non-null compound tag"), because FriendlyByteBuf.readNbt(buf, accounter) returns null for id 0.

**Keys.** java only, and optional - the 19 nodes carry {k, java} (18) or {k} (1), the same in all three versions. java is a raw toString of the extractor's stack value for the Codec the tag is parsed with (e.g. "CodecV[n={k=opaque, java=not-a-codec:net/minecraft/network/chat/Style$Serializer.CODEC}]", and one "OtherV[what=Codec.listOf]"); it does not affect the bytes and a reader must ignore it. The one node without java is the payload of serverbound:custom_click_action — the optional-of-nbt case. No max, no elem. Sibling rule confirmed: nbtOrText special-cases ComponentSerialization.CODEC into the prim token TEXT instead of an nbt node.

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:1159-1166 (nbtOrText; the node() call is on :1165), reached from :679 (fromCodec / fromCodecTrusted / fromCodecWithRegistries / fromCodecWithRegistriesTrusted / compoundTagCodec / tagCodec) and :2012 (buf.readWithCodec / readJsonWithCodec); also :681 (optionalTagCodec → optional(nbt)); generator gen_packets.go:850-851.

**In Go.** wire.NBT, a struct wrapping nbt.RawMessage — type at gen/src/wire/wire.go:188, ReadFrom at :190 forwarding to pk.NBT(&RawMessage).ReadFrom = NBTField.ReadFrom (gen/src/net/packet/types.go:509) with NetworkFormat(true); WriteTo at :191-196 substitutes an empty TAG_Compound for a zero value rather than writing TAG_End. NBTField.ReadFrom swallows nbt.ErrEND (types.go:517-523, returning nil error), so on the Go side a TAG_End decodes as an empty value where Java's tagCodec throws — a behaviour divergence, though not a byte-count one.
<!-- mc26 end -->

### opaque

**On the wire.** Nothing is known. An opaque node is the extractor admitting it could not decide the wire form of this subtree — a hole marker, not an encoding. A reader that meets one cannot know how many bytes to consume, so it cannot even skip it, and everything after it in the packet is unreadable. The only correct responses are to refuse the packet or to hand it to a hand-written reader.

**Keys.** java only — all 48 nodes carry exactly {k, java}, a free-form reason string. 26.2 values: "StreamCodec.recursive" x39, "no-method:Target.readContents" x4, "ClientboundPlayerInfoUpdatePacket$Entry.<init>(no buffer overload)", "RemoteChatSession$Data.<init>(no buffer overload)", and three "not-a-codec:...ItemAttributeModifiers$Display$*.CODEC". Other prefixes the code can emit include depth:, no-clinit:, no-class:, empty-reader:, registry-case:, lambda:, apply:, reader-loop:, combined:, string-enum:, buf.<name>, ByteBufCodecs.<name>, StreamCodec.<name>, recursive:.

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:103 (the opaque() helper; 43 textual occurrences of `opaque(` in the file, i.e. the helper plus ~42 call sites). Generator: there is deliberately NO case for it in goType, so it reaches the default at gen_packets.go:994 whose error is on :995 ("node opaque"). It is caught before that by gs.untyped() (gen_packets.go:756, the opaque case at :769-770) and the packet is skipped at :330-333 with that reason in the generated file's skipped comment. holeSummary() (:1437-1446) is not the packet path at all — it is used only by gen_components.go:69 for components (and holeSummaryNBT in gen_nbt.go for the NBT schema).

**In Go.** None. The extractor marks such a packet coverage "partial" (coverage() at :2213 over hasHole() at :2215-2234, which treats opaque, either, a case-less dispatch, a conditional node, a values-less enum and a names-less stringenum as holes) and the generator refuses to type it.
<!-- mc26 end -->

### optional

**On the wire.** One boolean byte — 0 absent, any non-zero present, writers emit exactly 1 — then one `elem` when present and nothing when absent. True for five of the six emission paths. The sixth (optionalTagCodec, :681) is mislabelled and has NO boolean byte at all; its single use additionally sits inside a length prefix the extractor erases. `elem` can itself be `unit`, in which case the whole node is one boolean byte and nothing else (6 nodes, in the debug packets).

**Keys.** `elem` only — 158/158 nodes (103 packets + 2 structs + 53 components) have exactly {k, elem}. No max, no count; the prefix is implicit in the kind. Distribution of `elem` worth knowing: 44 struct, 16 FLOAT, 15 BLOCK_POS, 10 list, 9 holder, 6 unit, 4 prim NBT (genuinely boolean-prefixed), 1 nbt (the broken one), 1 REST_BYTES.

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:92 (READER_RULES override for ServerboundCustomQueryAnswerPacket.readPayload -> optional{REST_BYTES}), :665 (ByteBufCodecs.optional), :681 (optionalTagCodec — the wrong one), :1102 (a ByteBufCodecs::optional method reference), :1956 (Optional.of/ofNullable seen outside a known branch region — a heuristic), :2007 (readNullable/readOptional). generator gen_packets.go:912-917.

**In Go.** pk.Option[T, *T] — gen/src/net/packet/util.go:188 (struct{Has Boolean; Val T}); ReadFrom :202 reads Boolean and only then the value, WriteTo :193. pk.Boolean.ReadFrom at gen/src/net/packet/types.go:100 (one byte, `v != 0`), WriteTo :89 (emits 0x01/0x00). For optional{unit} this is pk.Option[wire.Empty, *wire.Empty] = one byte, correct. For optional{nbt} it is pk.Option[wire.NBT] = one byte then a tag, which is WRONG bytes — and the library already contains the right shape for the boolean-free case, wire.OptionalNBT at gen/src/wire/wire.go:199 (ReadFrom :204 peeks the tag byte), reached only through the OPTIONAL_NBT prim name.
<!-- mc26 end -->

### packed

**On the wire.** `entries` values of `width` bits each, packed into big-endian 64-bit longs with floor(64/width) values per long and no value crossing a long: value i is bits (i mod vpl)*width .. +width-1 of long i/vpl, counted from the least significant bit; the unused high bits of each long are zero. Exactly ceil(entries / floor(64/width)) longs, and NO count in front of them (FriendlyByteBuf.readFixedSizeLongArray reads into an array the reader sized itself); width 0 means no longs at all (ZeroBitStorage.RAW is an empty long[]). The width is NOT the byte on the wire that selected the palette: it is the storage width of the configuration that byte selects, which for a linear block palette is 4 whatever the byte said (Strategy.FOUR_BITS_LINEAR serves bytes 1-4), and for a global palette is ceillog2 of the size of the id space, a number that depends on the registry the client was given and is not on the wire at all.

**Keys.** `entries` = how many values (4096 for a section's block states, 64 for its biomes); `bits` = the name of the earlier sibling field holding the byte that selected the palette; `width` = an object mapping that byte's value (as a string) to the storage width, with the key "*" for every other value; a width may be an integer or {"registryBits": <id space>}, meaning ceillog2 of the number of entries in that id space ("block_state": the total of every block's states in blocks.json; "worldgen/biome": the biomes the server synchronised, biomes.json for vanilla). ceillog2(n) = ceil(log2(n)), so ceillog2(1) = 0 and ceillog2(64) = 6; net.minecraft.util.Mth.ceillog2.

<!-- mc26 internal -->
**Where it comes from.** Only inside the two definitions still written by hand in gen/hand-crafted/prims.json, PALETTED_BLOCK_STATES and PALETTED_BIOMES, derived from PalettedContainer.read, Strategy$1/Strategy$2.getConfigurationForBitCount, Configuration$Simple/$Global.bitsInMemory, SimpleBitStorage.<init>(int,int,long[]) (valuesPerLong = 64/bits, longs = (size + valuesPerLong - 1) / valuesPerLong) and ZeroBitStorage. The extractor emits an opaque 'length-not-on-the-wire:readFixedSizeLongArray' for the same bytes in the structs section, which is the honest answer from the bytecode alone. gen/crosslang (the `packed` cases of its codec).

**In Go.** wire.Packed (the longs as read) in gen/src/wire/wire.go, read and written through wire.PackedOf(&field, entries, width): the generated struct hands it the count and the width, the width from a generated function per packed field (level.PalettedContainerBlockStatesDataWidth, from this node's `width`), the global width being block.BitsPerBlock / biome.BitsPerBiome. level's own BitStorage is built from the longs (level.StatesFromWire). Note that the Go global width is bits.Len(n) where Java's is ceillog2(n): equal for every registry size in 26.1-26.3, different when a registry has exactly a power of two entries (bits.Len(64) is 7, ceillog2(64) is 6).
<!-- mc26 end -->

### pred

**On the wire.** Nothing. `pred` is never a wire node and never reaches packet_schema.json — a kind census of 26.2 finds none (the kinds present are prim 1545, struct 1028, case 925, unit 457, ref 333, holder 190, list 187, optional 158, enum 110, registry 98, dispatch 58, string 53, opaque 48, map 19, nbt 17, resourcekey 17, holderset 11, either 7, enumset 1, whilelist 1, stringenum 1). It is an internal value of the extractor's bytecode interpreter: a static one-argument boolean method applied to a value that was read, with the method evaluated over the whole byte domain -128..127 so the predicate becomes an explicit value set.

**Keys.** `of` — the node of the value the predicate was applied to (the value actually on the wire). `values` — the inputs in -128..127 for which the predicate returns true. No `name`, `max` or `elem`.

<!-- mc26 internal -->
**Where it comes from.** Extractor: created at GenPacketSchema.java:2128-2132 (the push is :2131, `node("pred","of",pv.n(),"values",vals)`); when evalPredicate cannot answer, an untyped OtherV is pushed instead so nothing is invented. evalPredicate is at :1768-1786 and runPredicate at :1788. Consumed at :1642-1648 in branchTest, where a branch on a pred becomes `{test:"in", field:<the `of` value's field>, value:<values>}` (IFEQ → the not-taken/`in` form, IFNE → the negated form). Explicitly refused as a comparison operand at :1690 (`if (x == null

"pred".equals(x.n().get("k"))) return null;`). Generator: no case for it — goType (gen_packets.go:819-996) falls to the default at :995 and errors `node pred`.

**In Go.** None. What survives into the schema is the `in` test on the underlying value (see `when`). In 26.2 that is EIGHT tests, not four: the min and max flag of each of FloatArgumentInfo$Template, DoubleArgumentInfo$Template, IntegerArgumentInfo$Template and LongArgumentInfo$Template, each `{field:"byte", test:"in", value:[128 values]}` — which the generator collapses back to a single bit test via oneBitOf (gen_packets.go:703-730).
<!-- mc26 end -->

### prim

**On the wire.** Whatever the schema's `prims` section defines for the name in `t`. It is the schema's only leaf.

**Keys.** t is the primitive's name; a few carry a parameter the primitive's definition uses (len for FIXED_BYTES, bits for FIXED_BIT_SET, max for a bounded one).

<!-- mc26 internal -->
**Where it comes from.** GenPacketSchema.java prim(); gen_packets.go goType case "prim"

**In Go.** primTypes in gen_packets.go maps each name to a Go type
<!-- mc26 end -->

### ref

**On the wire.** Nothing of its own — a back-pointer, not a byte. A `ref` stands for one complete instance of the node it names, encoded exactly as that node is: for `of:"dispatch"` that is the VarInt registry id followed by that case's payload; for `of:"struct"` it is that struct's fields in order. Both recursively, and in both cases the node it names is an ENCLOSING one - resolve by walking outwards. It exists so a self-containing codec does not produce an infinite JSON tree.

**Keys.** `field` — the Java static codec field the cycle goes through, as `<internal/class/Name>.<FIELD>`; this is the identity of the cycle. `name` — the `name` of the node referred to. `of` — that node's kind (`"dispatch"` or `"struct"`). 336 refs, of two shapes and no more, the same in all three versions: 333 with key-set {field,k,name,of}, field=net/minecraft/world/item/crafting/display/SlotDisplay.STREAM_CODEC, name=SlotDisplay, of=dispatch; and 3 with key-set {k,name,of}, name=MobEffectInstance$Details, of=struct - no `field`, because those come from StreamCodec.recursive rather than from a codec field, and the struct they name encloses them. Resolve by substituting the node with that name/of from the nearest enclosing occurrence: verified that all 333 sit inside an enclosing dispatch node named SlotDisplay, so a purely local resolution always succeeds.

<!-- mc26 internal -->
**Where it comes from.** Extractor: GenPacketSchema.java:439 codecFieldNode; the placeholder `node("ref","field",key)` is installed in codecFieldCache at :459-460 BEFORE the field's <clinit> is interpreted, so any use during the build resolves to it; once built the same map object is patched in place at :482-488 (adding `name`/`of` when the result is a named dispatch or struct, otherwise REWRITING it into `{"k":"opaque","java":"recursive:<field>"}`), and :489 replaces the cache entry with the resolved node so later, non-cyclic uses get the full tree. Generator: gen_packets.go:905-911 (goType `case "ref"`), plus the list special case at :874-878.

**In Go.** A pointer box. Inside package types the generated text is `Box[SlotDisplay, *SlotDisplay]` (protocol/types/types.go:28 aliases `Box = wire.Box`); from another package that is `types.Box[types.SlotDisplay, *types.SlotDisplay]`, and in level/component it is `wire.Box[...]`. ReadFrom is gen/src/wire/wire.go:392 — allocate a fresh T, call T's ReadFrom, keep the pointer; WriteTo (:399) writes the zero value when nil. A ref used as a LIST element is not boxed: gen_packets.go:874-878 emits `List[SlotDisplay, *SlotDisplay]` directly, since a slice already breaks the cycle — but only when `elem["of"] == "dispatch"`. A ref with any other `of` is refused: `recursive <of> <name>` (:907-909).
<!-- mc26 end -->

### registry

**On the wire.** One VarInt and nothing else — the numeric id of an element, written with no offset, no prefix and no length. By three different Java code paths: ByteBufCodecs$29.decode (VarInt.read → IdMap.byIdOrThrow(i), no ±1), ByteBufCodecs$28.decode (idMapper: VarInt.read → IntFunction.apply) and FriendlyByteBuf.readById (readVarInt() → IntFunction.apply). All three are one bare VarInt; none validates the number before applying it.

**Keys.** registry: a label for the id space, and nothing else (every node has exactly the keys k, registry - no elem, no max, no values). 144 nodes over 20 labels in 26.3-pre-2, 145 over 20 in 26.1 and 26.2 (packets and components; the structs section is not counted). (1) 57 sit in a dispatch's "key" slot, 77 are the type of a field or of a case, 6 a list's elem, 4 an either's side. (2) The label is NOT "usually the vanilla registry path (item, block, entity_type)": by frequency in 26.3-pre-2 it is data_component_type 44, slot_display 37, Block.BLOCK_STATE_REGISTRY 16, block 6, entity_type 5, item 5, debug_subscription 5, Orientation 4, block_entity_type 3, particle_type 3, position_source_type 3, recipe_display 2, number_format_type 2, data_component_predicate_type 2, consume_effect_type 2, and one each of stat_type, custom_stat, command_argument_type, menu, recipe_book_category. (item's other occurrences in the file are holder nodes.) (3) The label is not always lowercased: the idmap: path at :1016 uses shortName(owner)+"."+field verbatim ("Block.BLOCK_STATE_REGISTRY", 16 nodes) while the byId path at :1991 lowercases only the last segment; registryName (:542) lowercases only the ResourceKey static-field path (:498). (4) "dynamic" (KeyV at :694) reaches no node in 26.2 — grep for it in packet_schema.json gives 0 hits, so listing it among observed labels is wrong. (5) There is no label "?" any more. The two that carried it were not unknown registries: clientbound:set_display_objective's slot is buf.readById(DisplaySlot.BY_ID), an enum numbered by its own id field, and is now an `enum` node (the extractor follows BY_ID to the ToIntFunction ByIdMap.continuous was given and reads the ids as it does for an idMapper); clientbound:award_stats' Stat is registry(Registries.STAT_TYPE).dispatch(Stat::getType, StatType::streamCodec), where each StatType's codec is ByteBufCodecs.registry(<the registry it was constructed with>), so it is now a `dispatch` on stat_type with one case per stat type naming that registry — mined reads a block id, crafted/used/broken/picked_up/dropped an item id, killed/killed_by an entity_type id, custom a custom_stat id (the extractor walks Stats' makeRegistryStatType with its arguments bound and runs StatType's constructor with them). Two of the labels are not registries at all: "Block.BLOCK_STATE_REGISTRY" (an id map field, labelled <class>.<FIELD>) and "Orientation" (an id map over net.minecraft.world.level.redstone.Orientation, which is a class and not an enum - its 48 entries are computed from an (up, front, sideBias) triple and are named nowhere in the jar, so the schema gives the id space the class's name and lists no members). A reader must therefore treat the label as an opaque name for an id space and look the members up elsewhere, not assume registries.json has an entry for it.

<!-- mc26 internal -->
**Where it comes from.** extractor data-gen/java/GenPacketSchema.java:670 (ByteBufCodecs.registry), :1016 (idMapper over an IdMap value), :2000 (dispatch key on a registry element, label from :1991), byIdNode (buf.readById: an id map field gives a registry node, an enum's BY_ID field an enum node); generator gen/internal/generate/gen_packets.go:852-858 for a value, and :1187-1210 (unionType) when it is a dispatch key, where the key field is again pk.VarInt and the case numbers come from registries.json, not from iota.

**In Go.** pk.VarInt, with the generated comment "registry <name>"; as a union key the comment is "id in <name>". pk.VarInt declared at gen/src/net/packet/types.go:56, ReadFrom at :292. The label is dropped from the type entirely.
<!-- mc26 end -->

### resourcekey

**On the wire.** Exactly one Identifier — a STRING (VarInt byte count then that many UTF-8 bytes) holding a namespaced id. The parent registry never travels; both producers supply it Java-side to ResourceKey.create. The cap on that string is the Identifier default, 32767 characters, and the schema records no max here.

**Keys.** registry only — all 17 nodes carry exactly {k, registry}, no max, no elem. It is the lowercased Java static-field name (registryName, :542), so it is a label and not always a registry path: dimension x9, root_id x4 (clientbound:waypoint), game_rule x2, type_key x1 (update_recipes), test_instance x1. root_id and type_key are field names.

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:686 (ResourceKey.streamCodec) and :2010 (buf.readResourceKey); generator gen_packets.go:859-860.

**In Go.** pk.Identifier, a type alias for pk.String (gen/src/net/packet/types.go:53; String at :46, ReadFrom at :120 — VarInt length then io.ReadFull, no cap check), with the generated comment "resource key in <registry>".
<!-- mc26 end -->

### rest

**On the wire.** `elem` repeated until the enclosing window is exhausted, with no count anywhere: the number of repetitions is whatever fits, and a reader knows it has read the last one because the window has no bytes left. The window is the nearest enclosing `lenprefixed` (or the packet frame). It exists for a byte array whose contents are a sequence with an out-of-band count: the chunk data of level_chunk_with_light holds one LevelChunkSection per 16 blocks of the dimension's height, a number the client takes from the dimension type it was sent at configuration time and that appears nowhere in the packet; reading sections until the array ends is the same bytes without the out-of-band number.

**Keys.** `elem` = the node repeated. Nothing else: no count, no max, no terminator.

<!-- mc26 internal -->
**Where it comes from.** Only inside the definition of CHUNK_SECTIONS, still written by hand in gen/hand-crafted/prims.json; the extractor does not emit it, since the count it stands in for is not in the bytecode of the reader (ClientboundLevelChunkPacketData reads the array as bytes and LevelChunk reads the sections out of it later with the level's section count). gen/crosslang (the `rest` cases of its codec).

**In Go.** wire.Rest[T, *T] in gen/src/wire/wire.go: elements until the reader is exhausted, which inside wire.LenPrefixed is the window. CHUNK_SECTIONS is types.LenPrefixed[types.Rest[level.LevelChunkSection]], and level.Chunk.PutSections checks the number of sections against the dimension's height.
<!-- mc26 end -->

### string

**On the wire.** Identical bytes to the primitive token STRING — a VarInt BYTE count then that many UTF-8 bytes. The cap is never on the wire and is never a second length field. Verified enforcement, both directions: Utf8String.read rejects the declared byte length above ByteBufUtil.utf8MaxBytes(max) BEFORE reading, then rejects a negative length, then rejects a length above readableBytes(), and after decoding rejects String.length() > max; Utf8String.write rejects CharSequence.length() > max, encodes, and rejects the encoded byte count above utf8MaxBytes(max) before writing the VarInt. (utf8MaxBytes is netty's 3*max.)

**Keys.** max only, and it is optional: the 53 nodes carry exactly {k, max} (27) or {k} (26). max values: 255 x5, 384 x4, 16 x3, 256 x3, 1024 x3, 32 x3, 128 x2, 4096, 40, 20, 32500. An absent max means 32767, not unbounded, because readUtf() is readUtf(32767): ClientboundTransferPacket, ServerboundRenameItemPacket, ServerboundChatCommandPacket, ServerboundChatCommandSignedPacket, ClientboundSetDisplayObjectivePacket and ServerboundSetJigsawBlockPacket all call the no-argument readUtf(). One caveat: node() drops null values (GenPacketSchema.java:75-79) and constOf returns null for a non-constant, so an absent max could in principle also mean "stringUtf8(x) with a cap this interpreter could not fold"; every max-less node I traced was a genuine readUtf().

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:675 (ByteBufCodecs.stringUtf8(max)) and :2023 (buf.readUtf([max])); generator gen_packets.go:846-847.

**In Go.** pk.String — the max is dropped entirely, so the generated Go neither enforces nor reports it. Type at gen/src/net/packet/types.go:46, ReadFrom at :120 (VarInt length, then io.ReadFull of that many bytes, no cap check and no negative-length check of its own beyond make([]byte, l) panicking).
<!-- mc26 end -->

### stringenum

**On the wire.** A STRING holding the constant's serialized name — VarInt byte count then that many UTF-8 bytes. Not an ordinal, not an index, no NBT or JSON wrapper. The single 26.2 site reads buf.readUtf() with no argument, so the Java cap is 32767 characters, and writes buf.writeUtf(getSerializedName()).

**Keys.** name, values, names — the node carries exactly {k, name, names, values}; names is positionally paired with values and is what travels. The generator errors unless len(names) == len(values); the extractor emits nothing (stringEnumNode returns null, leaving a hole) unless both lists were readable. One node in 26.2: JigsawBlockEntity$JointType, values [ROLLABLE, ALIGNED], names ["rollable","aligned"], the "joint" field of serverbound:set_jigsaw_block. Note the schema does not record the 32767 cap here (the string node it replaces is deleted), nor the Java-side DEFAULT constant that an unknown name decodes to.

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:384-389 (stringEnumNode), pushed at :593-596 (StringRepresentable.fromEnum / fromEnumWithMapping) and consumed at :1965-1972, where EnumCodec.byName(buf.readUtf(), DEFAULT) replaces the string node the readUtf had produced; generator gen_packets.go:945-951 (goType), gs.stringEnum at :1030-1053, renderStringEnum at :1834-1842.

**In Go.** A generated named type `type X pk.String` whose constants ARE the serialized names (renderStringEnum writes `XRollable X = "rollable"`), ReadFrom/WriteTo delegating to pk.String (types.go:120). Deliberately a string type so an unknown name still decodes. gs.stringEnum never merges with an ordinal enum of the same Java class — it appends "Name" (gen_packets.go:1048) — precisely because the wire forms differ. (The reverse guard is weaker: gs.enum at :1010-1022 would reuse an existing entry whose Values match, even one registered as a stringenum; no 26.2 pair collides, so nothing is wrong today.)
<!-- mc26 end -->

### struct

**On the wire.** The concatenation of its fields in the order listed, and nothing else — BUT only for the array-`when` form. A field carrying an array `when` is on the wire only when that condition over EARLIER fields of the same struct holds; a field carrying a string `when` is on the wire only when the named enum constant is a member of an EnumSet field in the ENCLOSING packet, not of this struct. `conditional: true` means the sentence does not hold at all. And it can silently fail to hold with no flag: `structs/PalettedContainer.read` is recorded {byte BYTE, long LONG} with coverage "full" while the real reader is readByte, then Palette.read (a var-int-counted palette that vanished entirely), then readFixedSizeLongArray (N longs, N out of band) collapsed to one LONG.

**Keys.** `name` = the Java class or a synthesised name ("RGBColor" at :451, "<Packet>Entry" at :1519), naming only, never on the wire — NOT unique and not always a real class. `fields` = ordered array of {name, type, when?}. A field's `name` is, in order of preference: the record component or constructor parameter the value is handed to (Mojang's jars keep MethodParameters), the local variable a read is stored in (they keep LocalVariableTable too: `slotId`, `min`, `background`), the field a constructor assigns it to, and only then the read method's own name (`varInt`, `byte`). A value read under a condition and then handed to another class's constructor stays a field of the reader, named by the constructor's parameter, because its `when` has to name a sibling; when every value of such a constructor call is read under one and the same condition, the constructed struct is the field and carries the `when`. Field names are NOT unique: since the names come from the bytecode's own variables and parameters (2026-09-08) only 1 of the 1019 structs of 26.3-pre-2 repeats one (the LevelChunkSection reader in the structs section, "short" twice and "biomes" twice), but nothing guarantees it; the Go generator disambiguates with uniqueField, any other generator must too. `when` form (a): array of GROUPS, each group an array of tests — groups ANDed, tests inside a group ORed. Test = {field, test, value?, not?, op?, mask?}, test in bit|true|eq|maskeq|cmp|in; `value` may be a string (an enum constant: one `eq` uses "OPENED_TAB"). `when` form (b): a bare string, an enum constant, used only with the struct's `guard`. `conditional: true` = the extractor saw branches it could not turn into per-field `when`. None in 26.1, 26.2 or 26.3-pre-2 since 2026-09-08 (the last, a switch on an enum in FilterMask.read, is a dispatch now); the key stays defined for a reader that meets a new shape. `guard: "<java enum class>"` (1, ClientboundPlayerInfoUpdatePacket$EntryBuilder).

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:451, 644, 707, 1125, 1382, 1385, 1401, 1403, 1519, 1881, 2153; guard form expandEnumReaders :1894-1932 (`builder.put("guard", enumClass)` at :1931); `conditional` at :1424-1425; attachGuards :1718-1760 (`f.put("when", out)` at :1758); the guard map is filled at :1338-1340 (`for (Region r : pending) when.add(new ArrayList<>(r.alts))` — one GROUP per enclosing region, the group's members being that region's OR-ed alts) and at :2108; Region.alts declared :1433. generator gen_packets.go:971 (goType case struct) -> structType :1126; fieldsOf :552; guardExpr :601 (outer joined with &&, inner with ||); array-guard rendering renderConditionalFields :1575; string-guard rendering renderGuarded :1616 via guardHook :441/:579.

**In Go.** A named Go struct, one field per schema field in order. No guards: `func (v *T) ReadFrom(r io.Reader) (int64, error) { return pk.Tuple{&v.A, ...}.ReadFrom(r) }` (gen_packets.go:1879-1881; pk.Tuple at gen/src/net/packet/util.go:251, ReadFrom :266, WriteTo :254). Array-`when` guards: renderConditionalFields (:1575) emits one `if <guard> { pk.Tuple{...} }` per run of fields sharing a guard. The string-`when`+`guard` struct goes through guardHook (:441, :579) to renderGuarded (:1616), which gives the entry type a `func (e *T) fields(guard EnumSetType) pk.Tuple` selector built with `if guard.Has(...)`, and the ENCLOSING packet's hand-written ReadFrom reads the list's pk.VarInt count itself and calls `p.Entries[i].fields(p.Actions)`. Names diverted to gs.hand/wireStructs at :960, :976, :982.
<!-- mc26 end -->

### unit

**On the wire.** Zero bytes. Nothing is read and nothing is written.

**Keys.** None: the node is literally {"k":"unit"}, 457 occurrences (454 packets, 0 structs, 3 components), every one with no other key. Where it appears: 431 as a dispatch case's `type`; 19 as a whole packet's or component's `type` (16 packets incl. the bundle delimiter, 3 components: unbreakable, creative_slot_lock, glider); 6 as an `optional`'s `elem` (clientbound:debug/{block,chunk,entity}_value, cases dedicated_server_tick_time and village_sections) — one lone boolean byte; 1 as the `type` of an enum-dispatch case (clientbound:waypoint, TrackedWaypoint$Type EMPTY). ALSO dispatch cases come in two shapes — {k:"case", id, type} (925) and, from :1082-1085, {id, num, type} with NO `k` at all (7 nodes).

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:444 (a STREAM_CODEC on a class with no fields), :603 (StreamCodec.unit(value)), :831 (a registry registration whose codec argument could not be found), :898 (a registry element whose reader read nothing), :1081 (an enum-dispatch case whose reader read nothing). generator gen_packets.go:848-849 (goType), :482 (a packet whose whole type is unit gets no fields), :1244 (a union case with no payload contributes no field).

**In Go.** wire.Empty — gen/src/wire/wire.go:382; ReadFrom :384 returns (0, nil) and WriteTo :385 returns (0, nil), so it touches the stream not at all.
<!-- mc26 end -->

### when

**On the wire.** Not a node kind — a key ON A FIELD of a struct node (26.2 field key-sets are exactly {name,type}x2489 and {name,type,when}x40). It never adds bytes; it says whether that field's bytes are present at all. When the condition is false the field contributes zero bytes and the next field follows immediately, so a reader MUST evaluate it. Two forms, distinguished by JSON type: a LIST form (31 occurrences) `"when": [[test,…],[test,…]]` where the OUTER list is ANDed (one entry per open branch region) and each INNER list is ORed (that region's alternatives); and a STRING form (9 occurrences) `"when": "CONSTANT"`, used only on a struct that also carries a `guard` key.

**Keys.** Every test is an object with `field` and `test`, plus per-kind extras and an optional `not:true`. In 26.2 the key-sets are exactly {field,test,value}x25, {field,mask,test,value}x4, {field,op,test,value}x4, {field,test}x2, and no test anywhere carries `not`. `field` names another field OF THE SAME STRUCT NODE, or one named run of bits of such a field as "flags.stepCount" (see `bits`),, as the schema names it BEFORE any naming_overrides rename (gen_packets.go:576 keeps `schemaName`, :589 looks the guard up in that map), and always one that appears EARLIER — the map is filled incrementally, so a forward or self reference errors `guard on unknown field` (:614). CAUTION: schema field names are not unique inside a struct (see problems), so `when.field` means 'the nearest preceding field with that name'. When the guarding value was never a Java field, attachGuards synthesises one at its read position, named `flags` for a bit test and otherwise from a read hint or literally `guard` (:1742) — so it is always a real field on the wire. THE SIX TEST KINDS (26.2 counts: in 10, eq 10, bit 5, maskeq 4, cmp 4, true 2): `bit` {field,value} — `$.F & value != 0`, and `value` is a MASK not a bit index (with not, `== 0`); `maskeq` {field,mask,value} — `$.F & mask == value` (with not, `!=`); `eq` {field,value} — `$.F == value`, where `value` is a number or a STRING when an enum constant was compared (seen_advancements: `{field:"action",test:"eq",value:"OPENED_TAB"}`), rendered as that enum's Go constant by guardValue (:732-741); `cmp` {field,op,value} with op one of >, >=, <, <= (map_item_data `width > 0`), where `not` inverts the operator rather than the expression — and negate() at :1601-1609 does the same in the extractor, so `not` can never appear on a cmp; `in` {field,value:[…]} — an OR of equalities, produced when a static predicate was evaluated over a byte's whole domain, which the generator collapses back to `$.F & mask != 0` when the 128-value set is exactly one bit of a pk.Byte (oneBitOf, :703-730); `true` {field} — `bool($.F)` for a BOOL field (player_look_at's `atEntity`).

<!-- mc26 internal -->
**Where it comes from.** Extractor: the list form is assembled at GenPacketSchema.java:1338-1340 (`for (Region r : pending) when.add(new ArrayList<>(r.alts)); guards.put(v, when);` — one outer entry per open region, each entry that region's alts), individual tests are built by branchTest at :1616-1704 with helpers test() :1594, negate() :1601, cmp() :1706, and it is written onto the field — with the guarding node replaced by its field NAME — by attachGuards at :1718-1762 (`f.put("when", out)` at :1758; the javadoc is at :1712-1717 and is wrong, see problems). Regions come from the forward-branch tracking at :1250-1298; a region whose condition the walker cannot read makes the struct `"conditional": true` instead (:1425 — 9 such structs in 26.2: eight `Path` inside the debug packets and one `FilterMask` in player_chat). The string form is emitted at :1919 by expandEnumReaders. Generator: read at gen_packets.go:568 (string form → goField.When) and :588 (list form → guardExpr :601-699 → goField.Guard); rendered by renderConditionalFields :1575-1611 and renderGuarded :1616-1690.

**In Go.** No type of its own — the field keeps its type and the guard becomes control flow. renderConditionalFields (:1575-1611) splits the struct's fields into CONSECUTIVE runs sharing the same guard expression and emits, in field order, `if <expr> { m, err = pk.Tuple{&p.A, &p.B}.ReadFrom(r); … }` per run, with `$.` replaced by the receiver, so an absent field is skipped and its Go value left zero. pk.Tuple.ReadFrom is gen/src/net/packet/util.go:266. For the string form see `guard`.
<!-- mc26 end -->

### whilelist

**On the wire.** Entries repeated with NO count anywhere: each entry carries a continuation bit inside one of its own fields, and reading ends at the first entry for which the `while` condition holds. The 26.2 instance is ClientboundSetEquipmentPacket: {signed BYTE, OPTIONAL_ITEM_STACK} repeated while bit 0x80 of the byte is set, the low 7 bits being the equipment-slot ordinal. Note: that is true of the READER (a do-while) but NOT of the writer — write() is a plain `for i < size` loop, so an empty slots list produces a packet with the entity var int and zero entries, which Mojang's own reader would then misparse. The Go generator's WriteTo refuses to emit that.

**Keys.** `elem` = the entry node (a struct built from one turn of the loop, or the single value if the body read only one). `while` = {field, test, value, not?} — actual key order in the file is {test, value, not, field}. `field` names the field of `elem` the test reads; "" would mean the entry itself is the tested value (collapseTerminated :1564 can produce that, and the generator cannot render it). `test` is "bit" and only "bit" (the extractor refuses anything else at :1542; the generator refuses anything else at :891). `value` is the mask as a SIGNED number (-128 = 0x80). The recorded condition is the branch's FALL-THROUGH, i.e. the LOOP-EXIT condition: plain `bit` = "(field & mask) != 0", `not:true` = "== 0". So {test:"bit", value:-128, not:true} reads "this entry is the last because its 0x80 bit is clear".

<!-- mc26 internal -->
**Where it comes from.** extractor GenPacketSchema.java:1571, inside collapseTerminated (:1528-1576), gated on a `bit` test on a value of the loop body (:1542); polarity from branchTest :1627-1628 (IFEQ/IFLE -> not=false, IFNE/IFGT -> not=true). generator gen_packets.go:884-904 (goType), whileList registration :1076, renderWhileLists :1100-1124.

**In Go.** A generated named slice type, `type <Elem>List []<Elem>` (ClientboundSetEquipmentEntryList), with ReadFrom/WriteTo written inline by renderWhileLists (gen_packets.go:1114-1122) rather than living in wire or packet. ReadFrom loops: read one entry, `last := e.Byte&-128 == 0`, `e.Byte &^= -128`, append, stop when last — so the Go value holds only the slot number. WriteTo errors on an empty slice ("the wire form has at least one entry") and sets the bit on every entry but the last. `not:true` maps to More=true (gen_packets.go:903, 1108-1113); the generator handles More=false symmetrically (stop "!= 0", set/clear swapped), so a terminator-bit polarity would render correctly if one ever appeared.
<!-- mc26 end -->
