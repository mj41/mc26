// gen_nbt generates the Go structs of NBT-shaped data from nbt_schema.json
// (GenNbtSchema: the DataFixerUpper codec chains read from the jar's bytecode):
//
//   - registry/elements_gen.go — one struct per registry the server sends in
//     the configuration phase (dimension types, biomes, chat types, damage
//     types, …), the nbt tags carrying the keys, plus the enums, records and
//     unions they use;
//   - registry/registries_gen.go — the Registries struct listing them and
//     NewNetworkCodec;
//   - chat/style_gen.go — Style, ClickEvent, HoverEvent and the chat type
//     Decoration, with json and nbt tags (text components travel both ways).
//
// A codec the walker cannot type (a dispatch on a registry, an either, an
// unknown combinator) becomes a raw field (nbt.RawMessage, any in chat) and is
// listed in the file header, so the hole is visible and the rest of the
// element is still typed.
package generate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

type nbtSchemaFile struct {
	Version    int                 `json:"version"`
	Registries map[string]nbtEntry `json:"registries"`
	Types      map[string]nbtEntry `json:"types"`
}

type nbtEntry struct {
	Class    string     `json:"class"`
	Field    string     `json:"field"`
	Coverage string     `json:"coverage"`
	Type     schemaNode `json:"type"`
}

// regField is one registry of the Registries struct.
type regField struct{ ID, Name, Elem, Comment string }

// nbtGen collects the types to emit, per target package.
type nbtGen struct {
	pkgs   map[string]*nbtPkg
	byJava map[string]string // Java internal name → "pkg.GoName" of an emitted type
	pin    string            // Java name of a registry element: stays in registry even when it is a chat class (ChatType)
}

type nbtPkg struct {
	name  string
	types []*nbtType
	taken map[string]*nbtType
}

type nbtType struct {
	GoName string
	Java   string // internal name
	Kind   string // struct, enum, union
	Doc    string
	Fields []nbtField
	Values []string // enum: constant names
	IDs    []string // enum: serialized names
}

type nbtField struct {
	Name, Type, Tag, Comment string
	Embedded                 bool
}

// nbtPrimTypes maps schema primitives to Go types; RGB_COLOR is a hand-written
// type in registry (int or "#rrggbb").
var nbtPrimTypes = map[string]string{
	"BOOL": "bool", "BYTE": "int8", "SHORT": "int16", "INT": "int32", "LONG": "int64",
	"FLOAT": "float32", "DOUBLE": "float64", "STRING": "string", "IDENTIFIER": "string",
	"UUID": "[]int32", "INT_ARRAY": "[]int32", "LONG_ARRAY": "[]int64", "BYTE_ARRAY": "[]byte",
}

func genNBT(jsonDir, outRoot string) error {
	var schema nbtSchemaFile
	if err := readJSON(filepath.Join(jsonDir, "nbt_schema.json"), &schema); err != nil {
		return fmt.Errorf("genNBT: %w (re-run extraction; the data must include GenNbtSchema's output)", err)
	}
	g := &nbtGen{pkgs: map[string]*nbtPkg{}, byJava: map[string]string{}}
	for _, p := range []string{"chat", "registry"} {
		g.pkgs[p] = &nbtPkg{name: p, taken: map[string]*nbtType{}}
	}

	// The chat structures first: a record reached from both a chat root and a
	// registry root lands in chat (registry imports chat, never the reverse).
	var chatHoles []string
	for _, key := range sortedKeys(schema.Types) {
		e := schema.Types[key]
		if !strings.HasPrefix(e.Class, "net.minecraft.network.chat.") {
			continue // save-format shapes: recorded in the schema, not consumed yet
		}
		if _, _, err := g.typeOf(e.Type, "chat", "chat structure "+e.Class); err != nil {
			return fmt.Errorf("genNBT: %s: %w", key, err)
		}
		chatHoles = append(chatHoles, holePaths(e.Type, shortJava(e.Class))...)
	}

	// The synchronized registries.
	var regs []regField
	var regHoles []string
	for _, id := range sortedKeys(schema.Registries) {
		e := schema.Registries[id]
		f := regField{ID: id, Name: registryFieldName(id)}
		g.pin = str(e.Type["java"])
		switch e.Type["k"] {
		case "struct":
			t, _, err := g.typeOf(e.Type, "registry", "the registry "+id)
			if err != nil {
				return fmt.Errorf("genNBT: %s: %w", id, err)
			}
			f.Elem = t
		case "dispatch":
			if e.Type["cases"] != nil {
				t, _, err := g.typeOf(e.Type, "registry", "the registry "+id)
				if err != nil {
					return fmt.Errorf("genNBT: %s: %w", id, err)
				}
				f.Elem = t
			}
		case "unit":
			java := strings.ReplaceAll(e.Class, ".", "/")
			t := g.newType("registry", java, "struct")
			t.Doc = fmt.Sprintf("%s is an element of the registry %s: Java %s, a codec without fields.", t.GoName, id, shortJava(e.Class))
			f.Elem = "registry." + t.GoName
		}
		g.pin = ""
		if f.Elem == "" {
			f.Elem = "nbt.RawMessage"
			f.Comment = shortJava(e.Class) + ": " + holeSummaryNBT(e.Type)
		}
		regs = append(regs, f)
		regHoles = append(regHoles, holePaths(e.Type, id)...)
	}

	// registry/elements_gen.go
	var hdr strings.Builder
	hdr.WriteString("// The elements of the registries a server sends in the configuration phase\n")
	hdr.WriteString("// (RegistryDataLoader.SYNCHRONIZED_REGISTRIES), typed from their codecs.\n")
	if len(regHoles) > 0 {
		hdr.WriteString("//\n// Fields the schema cannot type, kept as raw NBT:\n")
		for _, h := range regHoles {
			hdr.WriteString("//   " + h + "\n")
		}
	}
	if err := writeGo(filepath.Join(outRoot, "registry", "elements_gen.go"), g.render(g.pkgs["registry"], hdr.String())); err != nil {
		return fmt.Errorf("genNBT: %w", err)
	}

	// registry/elements_gen_test.go: every element type encodes and decodes
	if err := writeGo(filepath.Join(outRoot, "registry", "elements_gen_test.go"), g.renderElementsTest(regs)); err != nil {
		return fmt.Errorf("genNBT: %w", err)
	}

	// registry/registries_gen.go
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_nbt.go", "nbt_schema.json"))
	sb.WriteString("package registry\n\nimport \"github.com/mj41/go-mc26/nbt\"\n\n")
	sb.WriteString("// Registries holds the registries a server sends in the configuration phase\n")
	sb.WriteString("// (registry_data packets; RegistryDataLoader.SYNCHRONIZED_REGISTRIES of Minecraft " + jsonVersion + "),\n")
	sb.WriteString("// decoded into the element types of elements_gen.go. A registry whose element the\n")
	sb.WriteString("// schema cannot type is kept as raw NBT; a registry this build does not know goes\n")
	sb.WriteString("// to ExtraRegistries.\n")
	sb.WriteString("type Registries struct {\n")
	for _, r := range regs {
		fmt.Fprintf(&sb, "\t%s Registry[%s] `registry:\"%s\"`", r.Name, g.local("registry", r.Elem), r.ID)
		if r.Comment != "" {
			sb.WriteString(" // " + r.Comment)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n\tExtraRegistries map[string]*Registry[nbt.RawMessage]\n}\n\n")
	sb.WriteString("// NewNetworkCodec returns empty registries ready to receive registry_data.\nfunc NewNetworkCodec() Registries {\n\treturn Registries{\n")
	for _, r := range regs {
		fmt.Fprintf(&sb, "\t\t%s: NewRegistry[%s](),\n", r.Name, g.local("registry", r.Elem))
	}
	sb.WriteString("\t\tExtraRegistries: make(map[string]*Registry[nbt.RawMessage]),\n\t}\n}\n")
	if err := writeGo(filepath.Join(outRoot, "registry", "registries_gen.go"), sb.String()); err != nil {
		return fmt.Errorf("genNBT: %w", err)
	}

	// chat/style_gen.go
	hdr.Reset()
	hdr.WriteString("// The style of text components, click and hover events and the chat type\n")
	hdr.WriteString("// decoration, typed from their codecs; json tags for text components, nbt tags\n")
	hdr.WriteString("// for the registry data and the wire.\n")
	if len(chatHoles) > 0 {
		hdr.WriteString("//\n// Fields the schema cannot type, kept as any:\n")
		for _, h := range chatHoles {
			hdr.WriteString("//   " + h + "\n")
		}
	}
	if err := writeGo(filepath.Join(outRoot, "chat", "style_gen.go"), g.render(g.pkgs["chat"], hdr.String())); err != nil {
		return fmt.Errorf("genNBT: %w", err)
	}

	logf("genNBT: %d registries (%d raw), %d types in registry, %d in chat",
		len(regs), countRaw(regs, func(r regField) bool { return r.Elem == "nbt.RawMessage" }), len(g.pkgs["registry"].types), len(g.pkgs["chat"].types))
	return nil
}

// renderElementsTest writes a test that decodes an empty compound into every
// element type: the tags, the embedded records and the bridge types must all
// be decodable (a zero raw field cannot be encoded, so no round trip).
func (g *nbtGen) renderElementsTest(regs []regField) string {
	var sb strings.Builder
	sb.WriteString(generatedHeader("gen_nbt.go", "nbt_schema.json"))
	sb.WriteString("package registry\n\nimport (\n\t\"testing\"\n\n\t\"github.com/mj41/go-mc26/nbt\"\n)\n\n")
	sb.WriteString("// emptyCompound is {} as an unnamed root tag.\nvar emptyCompound = []byte{nbt.TagCompound, 0, 0, nbt.TagEnd}\n\n")
	sb.WriteString("func decodeElement[T any](t *testing.T, name string) {\n\tt.Helper()\n\tvar v T\n")
	sb.WriteString("\tif err := nbt.Unmarshal(emptyCompound, &v); err != nil {\n\t\tt.Fatalf(\"%s: %v\", name, err)\n\t}\n}\n\n")
	sb.WriteString("func TestElementsDecode(t *testing.T) {\n")
	for _, r := range regs {
		if r.Elem == "nbt.RawMessage" {
			continue
		}
		fmt.Fprintf(&sb, "\tdecodeElement[%s](t, %q)\n", g.local("registry", r.Elem), r.ID)
	}
	sb.WriteString("}\n")
	return sb.String()
}

func countRaw[T any](xs []T, f func(T) bool) int {
	n := 0
	for _, x := range xs {
		if f(x) {
			n++
		}
	}
	return n
}

// ---- types ------------------------------------------------------------------------

// typeOf returns the qualified Go type ("registry.DimensionType") of a struct,
// union or enum node, emitting it in pkg (or in chat when its Java class is a
// chat class); doc names what refers to it.
func (g *nbtGen) typeOf(n schemaNode, pkg, doc string) (string, string, error) {
	switch n["k"] {
	case "struct":
		return g.structType(n, pkg, doc), "", nil
	case "dispatch":
		if n["cases"] == nil {
			return "", "", fmt.Errorf("dispatch %v without cases", n["name"])
		}
		return g.unionType(n, pkg, doc), "", nil
	case "enum":
		return g.enumType(n, pkg), "", nil
	}
	return "", "", fmt.Errorf("node %v is not a type", n["k"])
}

// placement is the package a Java class lands in: chat classes in chat, the
// rest where they were reached from.
func (g *nbtGen) placement(java, pkg string) string {
	if strings.HasPrefix(java, "net/minecraft/network/chat/") && java != g.pin {
		return "chat"
	}
	return pkg
}

// newType allocates a Go name for java in pkg and registers it.
func (g *nbtGen) newType(pkg, java, kind string) *nbtType {
	p := g.pkgs[pkg]
	short := java[strings.LastIndex(java, "/")+1:]
	name := nbtTypeName(short)
	base := name
	for i := 2; ; i++ {
		if t, ok := p.taken[name]; !ok || t.Java == java {
			break
		}
		name = fmt.Sprintf("%s%d", base, i)
	}
	t := &nbtType{GoName: name, Java: java, Kind: kind}
	p.taken[name] = t
	p.types = append(p.types, t)
	g.byJava[java] = pkg + "." + name
	return t
}

// nbtTypeName turns a Java short name into a Go type name: Biome$ClimateSettings
// → BiomeClimateSettings, Enchantment$EnchantmentDefinition → EnchantmentDefinition.
func nbtTypeName(short string) string {
	parts := strings.Split(short, "$")
	if len(parts) == 2 && strings.HasPrefix(parts[1], parts[0]) {
		return parts[1]
	}
	return strings.Join(parts, "")
}

func (g *nbtGen) structType(n schemaNode, pkg, doc string) string {
	java := str(n["java"])
	if t, ok := g.byJava[java]; ok {
		return t
	}
	p := g.placement(java, pkg)
	t := g.newType(p, java, "struct")
	t.Doc = fmt.Sprintf("%s is Java %s (%s).", t.GoName, shortJava(java), doc)
	t.Fields = g.fields(n, p, t.GoName)
	return p + "." + t.GoName
}

// fields renders the fields of a struct node for code in package p.
func (g *nbtGen) fields(n schemaNode, p, owner string) []nbtField {
	var out []nbtField
	seen := map[string]bool{}
	fs, _ := n["fields"].([]any)
	for _, fa := range fs {
		f, _ := fa.(map[string]any)
		ft, _ := f["type"].(map[string]any)
		if f["inline"] == true {
			if ft["k"] == "struct" {
				inner := g.structType(ft, p, "inlined into "+owner)
				out = append(out, nbtField{Type: g.local(p, inner), Embedded: true, Comment: "keys at this level"})
				continue
			}
			// an inline codec that is not a record: nothing to name it by
			typ, comment := g.goType(ft, p)
			name := goFieldName(str(f["name"]))
			out = append(out, nbtField{Name: name, Type: typ, Tag: "`json:\"-\" nbt:\"-\"`", Comment: "inline codec, not decoded: " + comment})
			continue
		}
		key := str(f["key"])
		name := nbtFieldName(key)
		for seen[name] {
			name += "_"
		}
		seen[name] = true
		typ, comment := g.goType(ft, p)
		optional := f["optional"] == true
		if optional && pointerKind(ft) && typ != g.raw(p) {
			typ = "*" + typ
		}
		omit := ""
		if optional {
			omit = ",omitempty"
		}
		if d, ok := f["default"].(string); ok && d != "" {
			comment = strings.TrimSpace(comment + " default " + d)
		}
		out = append(out, nbtField{Name: name, Type: typ, Tag: fmt.Sprintf("`json:\"%s%s\" nbt:\"%s%s\"`", key, omit, key, omit), Comment: comment})
	}
	return out
}

// pointerKind says whether an optional field of this node is a pointer (nil =
// absent) rather than a value with omitempty.
func pointerKind(n map[string]any) bool {
	switch n["k"] {
	case "struct", "text":
		return true
	case "dispatch":
		return n["cases"] != nil
	case "holder":
		return n["direct"] != nil
	}
	return false
}

func (g *nbtGen) enumType(n schemaNode, pkg string) string {
	java := str(n["java"])
	if t, ok := g.byJava[java]; ok {
		return t
	}
	p := g.placement(java, pkg)
	t := g.newType(p, java, "enum")
	for _, v := range n["values"].([]any) {
		t.Values = append(t.Values, v.(string))
	}
	if ids, ok := n["ids"].([]any); ok {
		for _, v := range ids {
			t.IDs = append(t.IDs, v.(string))
		}
	}
	if len(t.IDs) != len(t.Values) {
		t.IDs = nil
		for _, v := range t.Values {
			t.IDs = append(t.IDs, strings.ToLower(v))
		}
	}
	t.Doc = fmt.Sprintf("%s is Java %s, stored as its serialized name.", t.GoName, shortJava(java))
	return p + "." + t.GoName
}

// unionType renders a dispatch with resolved cases as one struct: the key
// field, then the fields of every case (all optional); only those of the case
// named by the key are set.
func (g *nbtGen) unionType(n schemaNode, pkg, doc string) string {
	java := str(n["java"])
	if t, ok := g.byJava[java]; ok {
		return t
	}
	p := g.placement(java, pkg)
	t := g.newType(p, java, "union")
	key := str(n["key"])
	keyType := "string"
	if kt, ok := n["keyType"].(map[string]any); ok && kt["k"] == "enum" {
		keyType = g.local(p, g.enumType(kt, p))
	}
	t.Fields = append(t.Fields, nbtField{Name: nbtFieldName(key), Type: keyType, Tag: fmt.Sprintf("`json:\"%s\" nbt:\"%s\"`", key, key), Comment: "selects the case"})
	type leaf struct {
		key  string
		node map[string]any
	}
	var caseDocs []string
	index := map[string]int{}
	cases, _ := n["cases"].([]any)
	for _, ca := range cases {
		c, _ := ca.(map[string]any)
		id := str(c["id"])
		var leaves []leaf
		var collect func(s map[string]any)
		collect = func(s map[string]any) {
			fs, _ := s["fields"].([]any)
			for _, fa := range fs {
				f, _ := fa.(map[string]any)
				ft, _ := f["type"].(map[string]any)
				if f["inline"] == true && ft["k"] == "struct" {
					collect(ft)
					continue
				}
				leaves = append(leaves, leaf{str(f["key"]), ft})
			}
		}
		ct, _ := c["type"].(map[string]any)
		if ct["k"] == "struct" {
			collect(ct)
		}
		var keys []string
		for _, l := range leaves {
			keys = append(keys, l.key)
			typ, comment := g.goType(l.node, p)
			if pointerKind(l.node) && typ != g.raw(p) {
				typ = "*" + typ
			}
			if i, ok := index[l.key]; ok {
				f := &t.Fields[i]
				if f.Type != typ {
					f.Type = g.raw(p)
					f.Comment += "; " + id + ": " + typ + " (differs per case)"
				} else {
					f.Comment += ", " + id
				}
				continue
			}
			index[l.key] = len(t.Fields)
			t.Fields = append(t.Fields, nbtField{Name: nbtFieldName(l.key), Type: typ,
				Tag: fmt.Sprintf("`json:\"%s,omitempty\" nbt:\"%s,omitempty\"`", l.key, l.key), Comment: strings.TrimSpace(id + " " + comment)})
		}
		caseDocs = append(caseDocs, id+": "+strings.Join(keys, ", "))
	}
	t.Doc = fmt.Sprintf("%s is Java %s (%s): a union keyed by %q, rendered as the fields of every\n// case; only those of the case in %s are set (%s).",
		t.GoName, shortJava(java), doc, key, nbtFieldName(key), strings.Join(caseDocs, "; "))
	return p + "." + t.GoName
}

// goType returns the Go type of a node for code in package p, and a comment.
func (g *nbtGen) goType(n map[string]any, p string) (string, string) {
	switch n["k"] {
	case "prim":
		t := str(n["t"])
		switch t {
		case "RGB_COLOR":
			if p == "registry" {
				return "Color", "an int or \"#rrggbb\""
			}
			return g.raw(p), "an int or \"#rrggbb\""
		case "UUID_LENIENT":
			return g.raw(p), "a uuid as an int array or a string"
		}
		if gt, ok := nbtPrimTypes[t]; ok {
			return gt, ""
		}
		return g.raw(p), "unknown primitive " + t
	case "text":
		if p == "chat" {
			return "Message", ""
		}
		return "chat.Message", ""
	case "nbt":
		return g.raw(p), ""
	case "unit":
		return "struct{}", ""
	case "list":
		et, c := g.goType(n["elem"].(map[string]any), p)
		return "[]" + et, c
	case "map":
		vt, c := g.goType(n["val"].(map[string]any), p)
		kc := ""
		if k, ok := n["key"].(map[string]any); ok && k["registry"] != nil {
			kc = "keys: ids in " + str(k["registry"]) + " "
		}
		return "map[string]" + vt, strings.TrimSpace(kc + c)
	case "enum":
		return g.local(p, g.enumType(n, p)), ""
	case "registry", "resourcekey":
		return "string", "id in " + str(n["registry"])
	case "holderset":
		if p == "registry" {
			return "HolderSet", "elements of " + str(n["registry"])
		}
		return g.raw(p), "a tag, an id or a list of ids in " + str(n["registry"])
	case "holder":
		d, ok := n["direct"].(map[string]any)
		if !ok {
			return "string", "id in " + str(n["registry"])
		}
		if p != "registry" {
			return g.raw(p), "an id in " + str(n["registry"]) + " or the inline element"
		}
		dt, _ := g.goType(d, p)
		return "Holder[" + dt + "]", "an id in " + str(n["registry"]) + " or the inline element"
	case "either":
		return g.raw(p), "either " + nodeSummary(n["left"].(map[string]any)) + " or " + nodeSummary(n["right"].(map[string]any))
	case "dispatch":
		if n["cases"] == nil {
			return g.raw(p), fmt.Sprintf("a %s (dispatch on %q, not typed)", str(n["name"]), str(n["key"]))
		}
		return g.local(p, g.unionType(n, p, "")), ""
	case "struct":
		return g.local(p, g.structType(n, p, "")), ""
	case "opaque":
		return g.raw(p), "opaque: " + str(n["java"])
	}
	return g.raw(p), "unknown node " + str(n["k"])
}

// raw is the catch-all type of a package: nbt.RawMessage keeps the bytes for
// re-encoding; chat structures also travel as JSON, where any works for both.
func (g *nbtGen) raw(p string) string {
	if p == "chat" {
		return "any"
	}
	return "nbt.RawMessage"
}

// local strips p's own qualifier from a qualified type expression.
func (g *nbtGen) local(p, t string) string {
	return strings.ReplaceAll(t, p+".", "")
}

func nodeSummary(n map[string]any) string {
	switch n["k"] {
	case "prim":
		return str(n["t"])
	case "struct", "enum", "dispatch":
		return str(n["k"]) + " " + str(n["name"])
	case "list":
		return "list of " + nodeSummary(n["elem"].(map[string]any))
	}
	return str(n["k"])
}

func shortJava(java string) string {
	java = java[strings.LastIndex(java, "/")+1:]
	return java[strings.LastIndex(java, ".")+1:]
}

// nbtFieldName exports an NBT key: has_skylight → HasSkylight, message_id →
// MessageID, xPos → XPos, DataPacks → DataPacks.
func nbtFieldName(key string) string {
	var sb strings.Builder
	for _, w := range strings.FieldsFunc(key, func(r rune) bool { return r == '_' || r == '-' || r == '/' || r == ':' || r == '.' || r == ' ' }) {
		if v, ok := initialisms[strings.ToLower(w)]; ok {
			sb.WriteString(v)
			continue
		}
		sb.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	name := sb.String()
	if name == "" || !unicode.IsLetter(rune(name[0])) {
		name = "F" + name
	}
	return name
}

// registryFieldName names the Registries field of a registry id: the last
// path segment, exported (minecraft:worldgen/biome → Biome).
func registryFieldName(id string) string {
	id = stripMinecraftPrefix(id)
	id = id[strings.LastIndex(id, "/")+1:]
	return nbtFieldName(id)
}

// ---- holes ------------------------------------------------------------------------

// holePaths lists the untyped parts of a node with their key paths.
func holePaths(n schemaNode, root string) []string {
	var out []string
	var walk func(n map[string]any, path string)
	walk = func(n map[string]any, path string) {
		switch n["k"] {
		case "opaque":
			out = append(out, path+": "+str(n["java"]))
			return
		case "either":
			out = append(out, path+": either "+nodeSummary(n["left"].(map[string]any))+" or "+nodeSummary(n["right"].(map[string]any)))
			return
		case "dispatch":
			if n["cases"] == nil {
				out = append(out, path+": "+str(n["name"])+" (dispatch on \""+str(n["key"])+"\")")
				return
			}
			for _, ca := range n["cases"].([]any) {
				c := ca.(map[string]any)
				walk(c["type"].(map[string]any), path+"["+str(c["id"])+"]")
			}
		case "struct":
			fs, _ := n["fields"].([]any)
			for _, fa := range fs {
				f := fa.(map[string]any)
				k := str(f["key"])
				if f["inline"] == true {
					k = "(" + str(f["name"]) + ")"
				}
				walk(f["type"].(map[string]any), path+"."+k)
			}
		}
		for _, k := range []string{"elem", "val", "direct"} {
			if sub, ok := n[k].(map[string]any); ok {
				walk(sub, path+"/"+k)
			}
		}
	}
	walk(n, root)
	return out
}

func holeSummaryNBT(n schemaNode) string {
	hs := holePaths(n, "")
	if len(hs) == 0 {
		return "not a record"
	}
	return strings.TrimPrefix(hs[0], ": ")
}

// ---- rendering --------------------------------------------------------------------

func (g *nbtGen) render(p *nbtPkg, doc string) string {
	var sb strings.Builder
	sort.SliceStable(p.types, func(i, j int) bool {
		if p.types[i].Kind != p.types[j].Kind {
			return kindOrder(p.types[i].Kind) < kindOrder(p.types[j].Kind)
		}
		return p.types[i].GoName < p.types[j].GoName
	})
	for _, t := range p.types {
		switch t.Kind {
		case "enum":
			fmt.Fprintf(&sb, "// %s\ntype %s string\n\nconst (\n", t.Doc, t.GoName)
			for i, v := range t.Values {
				fmt.Fprintf(&sb, "\t%s%s %s = %q\n", t.GoName, nbtEnumConst(v), t.GoName, t.IDs[i])
			}
			sb.WriteString(")\n\n")
			fmt.Fprintf(&sb, "// %sValues lists the constants in declaration order (the ordinals).\nvar %sValues = []%s{", t.GoName, t.GoName, t.GoName)
			for i, v := range t.Values {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(t.GoName + nbtEnumConst(v))
			}
			sb.WriteString("}\n\n")
		default:
			fmt.Fprintf(&sb, "// %s\ntype %s struct {\n", t.Doc, t.GoName)
			for _, f := range t.Fields {
				if f.Embedded {
					fmt.Fprintf(&sb, "\t%s // %s\n", f.Type, f.Comment)
					continue
				}
				fmt.Fprintf(&sb, "\t%s %s %s", f.Name, f.Type, f.Tag)
				if f.Comment != "" {
					sb.WriteString(" // " + f.Comment)
				}
				sb.WriteString("\n")
			}
			sb.WriteString("}\n\n")
		}
	}
	body := sb.String()
	var imports []string
	if strings.Contains(body, "nbt.") {
		imports = append(imports, "\t\"github.com/mj41/go-mc26/nbt\"")
	}
	if p.name != "chat" && strings.Contains(body, "chat.") {
		imports = append(imports, "\t\"github.com/mj41/go-mc26/chat\"")
	}
	var out strings.Builder
	out.WriteString(generatedHeader("gen_nbt.go", "nbt_schema.json"))
	out.WriteString("\n" + doc + "package " + p.name + "\n\n")
	if len(imports) > 0 {
		sort.Strings(imports)
		out.WriteString("import (\n" + strings.Join(imports, "\n") + "\n)\n\n")
	}
	out.WriteString(body)
	return out.String()
}

// nbtEnumConst names an enum constant: OPEN_URL → OpenURL.
func nbtEnumConst(v string) string {
	return nbtFieldName(strings.ToLower(v))
}

func kindOrder(k string) int {
	switch k {
	case "enum":
		return 0
	case "struct":
		return 1
	}
	return 2
}
