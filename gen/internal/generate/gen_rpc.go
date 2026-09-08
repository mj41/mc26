// gen_rpc generates management/types_gen.go and management/methods_gen.go
// from json-rpc-api-schema.json: the OpenRPC document the server's own data
// generator writes for its management protocol ("Minecraft Server JSON-RPC",
// served over a WebSocket by the dedicated server when
// management-server-enabled is set). Every schema becomes a Go type, every
// method a typed call on management.Client, and every notification a typed
// value the client hands out; the transport is the hand-written client.go.
package generate

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type rpcSchema struct {
	OpenRPC string `json:"openrpc"`
	Info    struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"info"`
	Methods    []rpcMethod `json:"methods"`
	Components struct {
		Schemas map[string]json.RawMessage `json:"schemas"`
	} `json:"components"`
}

type rpcMethod struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Params      []rpcParam `json:"params"`
	Result      *rpcResult `json:"result"`
}

type rpcParam struct {
	Name     string          `json:"name"`
	Required bool            `json:"required"`
	Schema   json.RawMessage `json:"schema"`
}

type rpcResult struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
}

// rpcType is one JSON schema node as the document uses them: a $ref, a scalar
// (whose type may be a list of alternatives), an array, or an object.
type rpcType struct {
	Ref        string                     `json:"$ref"`
	Type       any                        `json:"type"`
	Enum       []string                   `json:"enum"`
	Items      json.RawMessage            `json:"items"`
	Properties map[string]json.RawMessage `json:"properties"`
}

const rpcNotificationPrefix = "minecraft:notification/"

func genRPC(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "json-rpc-api-schema.json")
	var s rpcSchema
	if err := readJSON(jsonPath, &s); err != nil {
		return fmt.Errorf("genRPC: %w", err)
	}
	g := &rpcGen{schema: s}

	types, err := g.types()
	if err != nil {
		return fmt.Errorf("genRPC: %w", err)
	}
	if err := writeFile(filepath.Join(goMCRoot, "management", "types_gen.go"), types); err != nil {
		return fmt.Errorf("genRPC: %w", err)
	}
	methods, err := g.methods()
	if err != nil {
		return fmt.Errorf("genRPC: %w", err)
	}
	if err := writeFile(filepath.Join(goMCRoot, "management", "methods_gen.go"), methods); err != nil {
		return fmt.Errorf("genRPC: %w", err)
	}
	logf("genRPC: %s %s: %d schemas, %d methods, %d notifications", s.Info.Title, s.Info.Version,
		len(s.Components.Schemas), g.nMethods, g.nNotifications)
	return nil
}

type rpcGen struct {
	schema         rpcSchema
	nMethods       int
	nNotifications int
}

// schemaName is the Go type of a components/schemas entry: user_ban → UserBan,
// ip_ban → IPBan.
func (g *rpcGen) schemaName(key string) string {
	parts := strings.Split(key, "_")
	for i, p := range parts {
		switch p {
		case "ip", "id":
			parts[i] = strings.ToUpper(p)
		default:
			parts[i] = snakeToCamel(p)
		}
	}
	return strings.Join(parts, "")
}

// refName resolves "#/components/schemas/player" to the Go type Player.
func (g *rpcGen) refName(ref string) (string, error) {
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("$ref %q is not a components/schemas reference", ref)
	}
	key := strings.TrimPrefix(ref, prefix)
	if _, ok := g.schema.Components.Schemas[key]; !ok {
		return "", fmt.Errorf("$ref %q names no schema", ref)
	}
	return g.schemaName(key), nil
}

// goType is the Go type of a schema node used as a field, parameter or result.
// An object schema that is not a named component has no Go struct: it is
// map[string]any, and a type that lists alternatives (["boolean","integer"],
// a game rule's value) is any.
func (g *rpcGen) goType(raw json.RawMessage) (string, error) {
	var t rpcType
	if err := json.Unmarshal(raw, &t); err != nil {
		return "", err
	}
	if t.Ref != "" {
		return g.refName(t.Ref)
	}
	switch tt := t.Type.(type) {
	case string:
		switch tt {
		case "string":
			return "string", nil
		case "integer":
			return "int", nil
		case "number":
			return "float64", nil
		case "boolean":
			return "bool", nil
		case "null":
			return "", nil
		case "array":
			if t.Items == nil {
				return "", fmt.Errorf("array schema without items")
			}
			elem, err := g.goType(t.Items)
			if err != nil {
				return "", err
			}
			return "[]" + elem, nil
		case "object":
			return "map[string]any", nil
		}
		return "", fmt.Errorf("schema type %q", tt)
	case []any:
		return "any", nil
	case nil:
		return "any", nil
	}
	return "", fmt.Errorf("schema type %v", t.Type)
}

// fieldName is the Go field of a JSON property: bypassesPlayerLimit →
// BypassesPlayerLimit, id → ID, ip → IP.
func rpcFieldName(prop string) string {
	switch prop {
	case "id":
		return "ID"
	case "ip":
		return "IP"
	}
	return snakeToCamel(strings.ToUpper(prop[:1]) + prop[1:])
}

// methodName is the Go name of an RPC method, derived from the wire name and
// nothing else: minecraft:allowlist/add → AllowlistAdd, minecraft:ip_bans/add
// → IPBansAdd, minecraft:serversettings/motd/set → ServersettingsMotdSet (the
// document writes serversettings as one word, so the Go name does).
func (g *rpcGen) methodName(name string) string {
	name = strings.TrimPrefix(name, rpcNotificationPrefix)
	name = stripMinecraftPrefix(name)
	var b strings.Builder
	for _, seg := range strings.Split(name, "/") {
		b.WriteString(g.schemaName(seg))
	}
	return b.String()
}

func (g *rpcGen) types() ([]byte, error) {
	var b strings.Builder
	b.WriteString(generatedHeader("gen_rpc.go", "json-rpc-api-schema.json"))
	fmt.Fprintf(&b, "// The types of the %s API, version %s (OpenRPC %s): every entry of the\n// document's components/schemas.\n\n",
		g.schema.Info.Title, g.schema.Info.Version, g.schema.OpenRPC)
	b.WriteString("package management\n\n")
	fmt.Fprintf(&b, "// APIVersion is the version the server's document declares for itself.\nconst APIVersion = %q\n\n", g.schema.Info.Version)

	keys := make([]string, 0, len(g.schema.Components.Schemas))
	for k := range g.schema.Components.Schemas {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var t rpcType
		if err := json.Unmarshal(g.schema.Components.Schemas[key], &t); err != nil {
			return nil, fmt.Errorf("schema %s: %w", key, err)
		}
		name := g.schemaName(key)
		typ, _ := t.Type.(string)
		switch {
		case typ == "string" && len(t.Enum) > 0:
			fmt.Fprintf(&b, "// %s is the schema %s: one of its constants.\ntype %s string\n\nconst (\n", name, key, name)
			for _, v := range t.Enum {
				fmt.Fprintf(&b, "\t%s%s %s = %q\n", name, snakeToCamel(v), name, v)
			}
			b.WriteString(")\n\n")
		case typ == "object":
			fmt.Fprintf(&b, "// %s is the schema %s.\ntype %s struct {\n", name, key, name)
			props := make([]string, 0, len(t.Properties))
			for p := range t.Properties {
				props = append(props, p)
			}
			sort.Strings(props)
			for _, p := range props {
				gt, err := g.goType(t.Properties[p])
				if err != nil {
					return nil, fmt.Errorf("schema %s.%s: %w", key, p, err)
				}
				// The document marks nothing as required; a string, a list or a
				// free value left empty is left out of the request (an absent
				// expiry is a permanent ban), a number or a boolean is always sent.
				tag := p
				if gt == "string" || gt == "any" || strings.HasPrefix(gt, "[]") || strings.HasPrefix(gt, "map[") {
					tag += ",omitempty"
				}
				fmt.Fprintf(&b, "\t%s %s `json:%q`\n", rpcFieldName(p), gt, tag)
			}
			b.WriteString("}\n\n")
		default:
			return nil, fmt.Errorf("schema %s: unsupported shape %s", key, string(g.schema.Components.Schemas[key]))
		}
	}
	return []byte(b.String()), nil
}

func (g *rpcGen) methods() ([]byte, error) {
	var b strings.Builder
	b.WriteString(generatedHeader("gen_rpc.go", "json-rpc-api-schema.json"))
	fmt.Fprintf(&b, "// The methods of the %s API, version %s: a typed call on Client for every\n// method a client may invoke, and a typed value for every notification the\n// server sends on its own.\n\n",
		g.schema.Info.Title, g.schema.Info.Version)
	b.WriteString("package management\n\nimport (\n\t\"context\"\n\t\"encoding/json\"\n)\n\n")

	var calls, notes []rpcMethod
	for _, m := range g.schema.Methods {
		if strings.HasPrefix(m.Name, rpcNotificationPrefix) {
			notes = append(notes, m)
		} else {
			calls = append(calls, m)
		}
	}
	g.nMethods, g.nNotifications = len(calls), len(notes)

	b.WriteString("// The method names as the wire carries them.\nconst (\n")
	for _, m := range calls {
		fmt.Fprintf(&b, "\tMethod%s = %q\n", g.methodName(m.Name), m.Name)
	}
	b.WriteString(")\n\n")

	for _, m := range calls {
		name := g.methodName(m.Name)
		if len(m.Params) > 1 {
			return nil, fmt.Errorf("method %s has %d params; the server accepts one", m.Name, len(m.Params))
		}
		if m.Result == nil {
			return nil, fmt.Errorf("method %s has no result", m.Name)
		}
		res, err := g.goType(m.Result.Schema)
		if err != nil {
			return nil, fmt.Errorf("method %s result: %w", m.Name, err)
		}
		if res == "" {
			return nil, fmt.Errorf("method %s: a null result on a method a client calls", m.Name)
		}
		doc := m.Description
		if doc == "" {
			doc = "calls " + m.Name
		}
		fmt.Fprintf(&b, "// %s: %s (%s).\n", name, doc, m.Name)
		if len(m.Params) == 0 {
			fmt.Fprintf(&b, "func (c *Client) %s(ctx context.Context) (%s, error) {\n\tvar out %s\n\terr := c.Call(ctx, Method%s, nil, &out)\n\treturn out, err\n}\n\n", name, res, res, name)
			continue
		}
		p := m.Params[0]
		pt, err := g.goType(p.Schema)
		if err != nil {
			return nil, fmt.Errorf("method %s param %s: %w", m.Name, p.Name, err)
		}
		arg := snakeToCamel(p.Name)
		arg = strings.ToLower(arg[:1]) + arg[1:]
		fmt.Fprintf(&b, "func (c *Client) %s(ctx context.Context, %s %s) (%s, error) {\n\tvar out %s\n\terr := c.Call(ctx, Method%s, []any{%s}, &out)\n\treturn out, err\n}\n\n",
			name, arg, pt, res, res, name, arg)
	}

	b.WriteString(`// Notification is a message the server sends without being asked: one of the
// types below, or an UnknownNotification for a method this version's document
// does not list.
type Notification interface {
	// NotificationMethod is the method name the wire carried.
	NotificationMethod() string
}

// UnknownNotification is a notification whose method the document does not
// list, with its params as they arrived.
type UnknownNotification struct {
	Method string
	Params json.RawMessage
}

func (n UnknownNotification) NotificationMethod() string { return n.Method }

`)
	for _, m := range notes {
		name := g.methodName(m.Name) + "Notification"
		if len(m.Params) > 1 {
			return nil, fmt.Errorf("notification %s has %d params; the server sends one", m.Name, len(m.Params))
		}
		doc := m.Description
		if doc == "" {
			doc = "is " + m.Name
		}
		fmt.Fprintf(&b, "// %s: %s (%s).\n", name, doc, m.Name)
		if len(m.Params) == 0 {
			fmt.Fprintf(&b, "type %s struct{}\n\n", name)
		} else {
			pt, err := g.goType(m.Params[0].Schema)
			if err != nil {
				return nil, fmt.Errorf("notification %s: %w", m.Name, err)
			}
			fmt.Fprintf(&b, "type %s struct {\n\t%s %s\n}\n\n", name, rpcFieldName(m.Params[0].Name), pt)
		}
		fmt.Fprintf(&b, "func (%s) NotificationMethod() string { return %q }\n\n", name, m.Name)
	}

	b.WriteString("// decodeNotification turns a method name and its params into the typed value.\nfunc decodeNotification(method string, params json.RawMessage) (Notification, error) {\n\tswitch method {\n")
	for _, m := range notes {
		name := g.methodName(m.Name) + "Notification"
		fmt.Fprintf(&b, "\tcase %q:\n", m.Name)
		if len(m.Params) == 0 {
			fmt.Fprintf(&b, "\t\treturn %s{}, nil\n", name)
			continue
		}
		fmt.Fprintf(&b, "\t\tvar n %s\n\t\tif err := decodeParam(params, %q, &n.%s); err != nil {\n\t\t\treturn nil, err\n\t\t}\n\t\treturn n, nil\n",
			name, m.Params[0].Name, rpcFieldName(m.Params[0].Name))
	}
	b.WriteString("\t}\n\treturn UnknownNotification{Method: method, Params: params}, nil\n}\n")
	return []byte(b.String()), nil
}
