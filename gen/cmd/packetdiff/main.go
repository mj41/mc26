// packetdiff compares the packet wire schemas of two extracted MC versions.
//
//	go run ./gen/cmd/packetdiff 26.1 26.2             # temp/data/<version> (or MC26_DATA)
//	go run ./gen/cmd/packetdiff ../mc26-data-a ../mc26-data-b   # any two data directories
//
// It reads packet_schema.json (from GenPacketSchema) and packets.json (numeric
// IDs per state) from each directory and prints: packets added / removed per
// state and flow, ID moves, and per-packet wire-layout changes as a token diff.
// A token is one codec or buffer read in wire order; nested codecs appear
// inline as Owner.FIELD{...}, so a change inside a shared type is reported on
// every packet that carries it.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mj41/mc26/gen/internal/paths"
)

type schemaEntry struct {
	State  string   `json:"state"`
	Class  string   `json:"class"`
	Tokens []string `json:"tokens"`
}

type schema struct {
	Packets map[string]schemaEntry `json:"packets"`
}

// packets.json: state -> flow -> name -> {protocol_id}
type packetsReport map[string]map[string]map[string]struct {
	ProtocolID int `json:"protocol_id"`
}

type version struct {
	name   string
	schema schema
	ids    packetsReport
}

func main() {
	var args []string
	verbose := false
	for _, s := range os.Args[1:] {
		if s == "-v" {
			verbose = true
		} else {
			args = append(args, s)
		}
	}
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: packetdiff [-v] <versionA|dirA> <versionB|dirB>   (-v lists renumbered packets)")
		os.Exit(2)
	}
	a, err := load(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "packetdiff:", err)
		os.Exit(1)
	}
	b, err := load(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "packetdiff:", err)
		os.Exit(1)
	}
	fmt.Printf("packetdiff: %s (%d packets) -> %s (%d packets)\n\n", a.name, len(a.schema.Packets), b.name, len(b.schema.Packets))
	idChanges(a, b, verbose)
	layoutChanges(a, b)
}

func load(arg string) (*version, error) {
	dir := paths.Data(arg)
	v := &version{name: filepath.Base(dir)}
	if err := readJSON(filepath.Join(dir, "packet_schema.json"), &v.schema); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, "packets.json"), &v.ids); err != nil {
		fmt.Fprintf(os.Stderr, "packetdiff: %s: no packets.json, IDs skipped (%v)\n", v.name, err)
		v.ids = packetsReport{}
	}
	return v, nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// idChanges prints packets added and removed per state/flow; renumbered
// packets (the usual consequence of an insertion) are counted unless verbose.
func idChanges(a, b *version, verbose bool) {
	states := union(keys(a.ids), keys(b.ids))
	printed := false
	for _, st := range states {
		for _, flow := range []string{"clientbound", "serverbound"} {
			am, bm := a.ids[st][flow], b.ids[st][flow]
			names := union(keys(am), keys(bm))
			var lines, moved []string
			for _, n := range names {
				ai, aok := am[n]
				bi, bok := bm[n]
				switch {
				case aok && !bok:
					lines = append(lines, fmt.Sprintf("  - %-45s (was 0x%02X)", n, ai.ProtocolID))
				case !aok && bok:
					lines = append(lines, fmt.Sprintf("  + %-45s 0x%02X", n, bi.ProtocolID))
				case ai.ProtocolID != bi.ProtocolID:
					moved = append(moved, fmt.Sprintf("  ~ %-45s 0x%02X -> 0x%02X", n, ai.ProtocolID, bi.ProtocolID))
				}
			}
			if len(lines) == 0 && len(moved) == 0 {
				continue
			}
			printed = true
			fmt.Printf("## %s %s: %d added/removed, %d renumbered\n", st, flow, len(lines), len(moved))
			for _, l := range lines {
				fmt.Println(l)
			}
			if verbose {
				for _, l := range moved {
					fmt.Println(l)
				}
			}
			fmt.Println()
		}
	}
	if !printed {
		fmt.Println("## packet IDs: no changes")
		fmt.Println()
	}
}

// layoutChanges prints, per packet present in both versions, the token diff.
func layoutChanges(a, b *version) {
	names := union(keys(a.schema.Packets), keys(b.schema.Packets))
	changed := 0
	for _, n := range names {
		ae, aok := a.schema.Packets[n]
		be, bok := b.schema.Packets[n]
		if !aok || !bok {
			continue
		}
		at, bt := normalizeAll(flatten(ae.Tokens)), normalizeAll(flatten(be.Tokens))
		if strings.Join(at, "\x00") == strings.Join(bt, "\x00") {
			continue
		}
		changed++
		fmt.Printf("## %s (%s, %s)\n", n, be.State, shortClass(be.Class))
		for _, l := range diffTokens(at, bt) {
			fmt.Println(l)
		}
		fmt.Println()
	}
	both := 0
	for n := range a.schema.Packets {
		if _, ok := b.schema.Packets[n]; ok {
			both++
		}
	}
	fmt.Printf("packetdiff: %d of %d shared packets changed their wire layout\n", changed, both)
}

// diffTokens is an LCS diff over token lists; nested-only changes show as
// one removed and one added token with the differing nested part.
func diffTokens(a, b []string) []string {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, "    "+clip(a[i]))
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, "  - "+clip(a[i]))
			i++
		default:
			out = append(out, "  + "+clip(b[j]))
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, "  - "+clip(a[i]))
	}
	for ; j < m; j++ {
		out = append(out, "  + "+clip(b[j]))
	}
	return out
}

// flatten opens a single top-level "λX.<init>{a, b, c}" (packets read by a
// buffer constructor) into its members so the diff points at the field that
// changed instead of reporting the whole constructor.
func flatten(tokens []string) []string {
	if len(tokens) != 1 || !strings.HasPrefix(tokens[0], "λ") {
		return tokens
	}
	t := tokens[0]
	open := strings.Index(t, "{")
	if open < 0 || !strings.HasSuffix(t, "}") {
		return tokens
	}
	inner := t[open+1 : len(t)-1]
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(inner[start:i]))
				start = i + 1
			}
		}
	}
	if rest := strings.TrimSpace(inner[start:]); rest != "" {
		out = append(out, rest)
	}
	return out
}

// wireNames maps the two spellings of the same wire primitive — a FriendlyByteBuf
// read call and a ByteBufCodecs constant — onto one canonical name, so a Java-side
// refactor from a buffer constructor to a StreamCodec (26.3 did this for dozens of
// packets) is not reported as a wire change.
var wireNames = map[string]string{
	"buf.readBoolean": "BOOL", "ByteBufCodecs.BOOL": "BOOL",
	"buf.readByte": "BYTE", "buf.readUnsignedByte": "BYTE", "ByteBufCodecs.BYTE": "BYTE",
	"buf.readShort": "SHORT", "buf.readUnsignedShort": "SHORT", "ByteBufCodecs.SHORT": "SHORT",
	"buf.readInt": "INT", "ByteBufCodecs.INT": "INT",
	"buf.readLong": "LONG", "ByteBufCodecs.LONG": "LONG",
	"buf.readFloat": "FLOAT", "ByteBufCodecs.FLOAT": "FLOAT",
	"buf.readDouble": "DOUBLE", "ByteBufCodecs.DOUBLE": "DOUBLE",
	"buf.readVarInt": "VAR_INT", "ByteBufCodecs.VAR_INT": "VAR_INT", "VarInt.read{buf.readByte}": "VAR_INT",
	"buf.readVarLong": "VAR_LONG", "ByteBufCodecs.VAR_LONG": "VAR_LONG",
	"buf.readUtf": "STRING", "ByteBufCodecs.STRING_UTF8": "STRING",
	"buf.readUUID": "UUID", "UUIDUtil.STREAM_CODEC": "UUID",
	"buf.readIdentifier": "IDENTIFIER", "buf.readResourceLocation": "IDENTIFIER", "Identifier.STREAM_CODEC": "IDENTIFIER", "ResourceLocation.STREAM_CODEC": "IDENTIFIER",
	"buf.readByteArray": "BYTE_ARRAY", "ByteBufCodecs.byteArray": "BYTE_ARRAY", "ByteBufCodecs.BYTE_ARRAY": "BYTE_ARRAY",
	"buf.readVarIntArray": "VAR_INT_ARRAY", "ByteBufCodecs.VAR_INT_ARRAY": "VAR_INT_ARRAY",
	"buf.readLongArray": "LONG_ARRAY", "ByteBufCodecs.LONG_ARRAY": "LONG_ARRAY",
	"buf.readBitSet": "BIT_SET", "ByteBufCodecs.BIT_SET": "BIT_SET",
	"buf.readBlockPos": "BLOCK_POS", "BlockPos.STREAM_CODEC": "BLOCK_POS",
	"buf.readChunkPos": "CHUNK_POS", "ChunkPos.STREAM_CODEC": "CHUNK_POS",
	"buf.readVec3": "VEC3", "Vec3.STREAM_CODEC": "VEC3",
	"buf.readLpVec3": "LP_VEC3", "Vec3.LP_STREAM_CODEC": "LP_VEC3",
	"buf.readInstant": "INSTANT", "ByteBufCodecs.INSTANT": "INSTANT",
	"buf.readEnum": "ENUM", "ByteBufCodecs.idMapper": "ENUM",
	"buf.readList": "LIST", "buf.readCollection": "LIST", "ByteBufCodecs.list": "LIST", "ByteBufCodecs.collection": "LIST",
	"buf.readNullable": "OPTIONAL", "buf.readOptional": "OPTIONAL", "ByteBufCodecs.optional": "OPTIONAL",
	"buf.readMap": "MAP", "ByteBufCodecs.map": "MAP",
	"buf.readNbt": "NBT", "ByteBufCodecs.TRUSTED_TAG": "NBT", "ByteBufCodecs.COMPOUND_TAG": "NBT",
	"buf.readJsonWithCodec": "JSON", "buf.readWithCodec": "CODEC",
	"buf.readResourceKey": "RESOURCE_KEY", "ResourceKey.STREAM_CODEC": "RESOURCE_KEY",
	"buf.readGameProfile": "GAME_PROFILE", "ByteBufCodecs.GAME_PROFILE": "GAME_PROFILE",
	"StreamCodec.apply": "", "StreamCodec.map": "", "StreamCodec.dispatch": "DISPATCH",
}

var ownerSuffix = regexp.MustCompile(`^([A-Za-z0-9_$]+)\.(<init>|read|STREAM_CODEC|CODEC|[A-Z_]*_STREAM_CODEC)(\{|$)`)

// normalize canonicalises one token: wire primitives by name, nested codecs by
// owner (X.<init>{…}, X.read{…} and X.STREAM_CODEC{…} are the same layout), and
// applies the same rules inside nested braces.
func normalize(t string) string {
	t = strings.TrimPrefix(t, "λ")
	if v, ok := wireNames[t]; ok {
		return v
	}
	// nested: canonicalise the inner list first
	if open := strings.Index(t, "{"); open >= 0 && strings.HasSuffix(t, "}") {
		head := t[:open]
		inner := normalizeAll(splitTop(t[open+1 : len(t)-1]))
		head = ownerSuffix.ReplaceAllString(head+"{", "$1{")
		head = strings.TrimSuffix(head, "{")
		return head + "{" + strings.Join(inner, ", ") + "}"
	}
	return t
}

func normalizeAll(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		n := normalize(t)
		if n == "" {
			continue
		}
		out = append(out, n)
	}
	return out
}

func splitTop(inner string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(inner); i++ {
		switch inner[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(inner[start:i]))
				start = i + 1
			}
		}
	}
	if rest := strings.TrimSpace(inner[start:]); rest != "" {
		out = append(out, rest)
	}
	return out
}

func clip(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func shortClass(c string) string {
	return c[strings.LastIndex(c, ".")+1:]
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func union(a, b []string) []string {
	set := map[string]bool{}
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		set[s] = true
	}
	return keys(set)
}
