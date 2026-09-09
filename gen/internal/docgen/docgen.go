// Package docgen renders the documentation templates (gen/docs/*.mc26tmpl.md)
// for one Minecraft version. A template is ordinary Markdown, readable as it
// is; the directives are HTML comments, invisible in a rendered preview:
//
//	<!-- mc26 version: 26.1 26.2 -->      the block that follows is for these versions only
//	<!-- mc26 version: >= 26.3-pre-1 -->  … or for the versions a comparison selects
//	<!-- mc26 internal -->                … or for the project's own rendering, not the data repository's
//	<!-- mc26 end -->                     ends the innermost block
//	<!-- mc26 include: prims -->          replaced by a table or tree generated from the version's JSON
//	<!-- mc26 value: id -->               replaced inline by a fact of the version (id, name, protocol, data-version, extracted, jar-sha1)
//
// The prose — what a node kind or the frame is on the wire — lives in the
// template; the JSON keeps what machines check. Check keeps the two together:
// every node kind of nodes.json has its section in the protocol template.
package docgen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mj41/mc26/gen/internal/mcver"
)

// Options say what to render for.
type Options struct {
	Version  string // the Minecraft version id
	DataDir  string // the version's extracted JSON
	HandDir  string // gen/hand-crafted: nodes.json
	Internal bool   // keep the <!-- mc26 internal --> blocks
}

var directive = regexp.MustCompile(`<!--\s*mc26\s+([a-z-]+)\s*:?\s*(.*?)\s*-->`)

// Render renders one template.
func Render(tmpl string, o Options) (string, error) {
	d, err := load(o)
	if err != nil {
		return "", err
	}
	return render(tmpl, o, d)
}

// RenderFile renders a template file.
func RenderFile(path string, o Options) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	out, err := Render(string(b), o)
	if err != nil {
		return "", fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return out, nil
}

// OutputName is the rendered file's name: protocol.mc26tmpl.md → protocol.md.
func OutputName(tmplPath string) string {
	return strings.TrimSuffix(filepath.Base(tmplPath), ".mc26tmpl.md") + ".md"
}

func render(tmpl string, o Options, d *data) (string, error) {
	var out strings.Builder
	type block struct {
		keep bool
		line int
	}
	var stack []block
	keeping := func() bool {
		for _, b := range stack {
			if !b.keep {
				return false
			}
		}
		return true
	}
	lines := strings.Split(tmpl, "\n")
	for i, line := range lines {
		m := directive.FindStringSubmatch(line)
		if m == nil || strings.TrimSpace(line) != m[0] {
			// inline values, then the line itself
			if keeping() {
				var err error
				line, err = replaceValues(line, d, i+1)
				if err != nil {
					return "", err
				}
				out.WriteString(line)
				if i < len(lines)-1 {
					out.WriteString("\n")
				}
			}
			continue
		}
		kind, arg := m[1], m[2]
		switch kind {
		case "version":
			ok, err := mcver.Matches(o.Version, arg)
			if err != nil {
				return "", fmt.Errorf("line %d: %w", i+1, err)
			}
			stack = append(stack, block{keep: ok, line: i + 1})
		case "internal":
			stack = append(stack, block{keep: o.Internal, line: i + 1})
		case "end":
			if len(stack) == 0 {
				return "", fmt.Errorf("line %d: end without a block", i+1)
			}
			stack = stack[:len(stack)-1]
		case "include":
			if !keeping() {
				continue
			}
			s, err := d.include(arg)
			if err != nil {
				return "", fmt.Errorf("line %d: include %q: %w", i+1, arg, err)
			}
			out.WriteString(s)
			if !strings.HasSuffix(s, "\n") {
				out.WriteString("\n")
			}
		case "value":
			// a value directive alone on a line: the value, on its line
			if keeping() {
				v, err := d.value(arg)
				if err != nil {
					return "", fmt.Errorf("line %d: %w", i+1, err)
				}
				out.WriteString(v + "\n")
			}
		default:
			return "", fmt.Errorf("line %d: unknown directive %q", i+1, kind)
		}
	}
	if len(stack) > 0 {
		return "", fmt.Errorf("line %d: block never ended", stack[len(stack)-1].line)
	}
	return out.String(), nil
}

func replaceValues(line string, d *data, lineNo int) (string, error) {
	var err error
	line = directive.ReplaceAllStringFunc(line, func(s string) string {
		m := directive.FindStringSubmatch(s)
		if m[1] != "value" {
			err = fmt.Errorf("line %d: %s must stand on a line of its own", lineNo, m[1])
			return s
		}
		v, e := d.value(m[2])
		if e != nil {
			err = fmt.Errorf("line %d: %w", lineNo, e)
			return s
		}
		return v
	})
	return line, err
}

// Check reads the templates and the hand-crafted files and reports what does
// not fit: a node kind with no "### <kind>" section in protocol.mc26tmpl.md,
// a directive that is not understood, a block never ended, an include or a
// value nobody renders.
func Check(docsDir, handDir string, kinds []string) []string {
	var problems []string
	entries, err := filepath.Glob(filepath.Join(docsDir, "*.mc26tmpl.md"))
	if err != nil || len(entries) == 0 {
		return []string{fmt.Sprintf("no templates in %s", docsDir)}
	}
	sort.Strings(entries)
	for _, path := range entries {
		b, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		name := filepath.Base(path)
		depth := 0
		for i, line := range strings.Split(string(b), "\n") {
			m := directive.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			switch m[1] {
			case "version":
				if _, err := mcver.Matches("26.1", m[2]); err != nil {
					problems = append(problems, fmt.Sprintf("%s:%d: %v", name, i+1, err))
				}
				depth++
			case "internal":
				depth++
			case "end":
				depth--
				if depth < 0 {
					problems = append(problems, fmt.Sprintf("%s:%d: end without a block", name, i+1))
					depth = 0
				}
			case "include":
				if _, ok := includes[strings.Fields(m[2])[0]]; !ok {
					problems = append(problems, fmt.Sprintf("%s:%d: unknown include %q", name, i+1, m[2]))
				}
			case "value":
				if _, ok := values[m[2]]; !ok {
					problems = append(problems, fmt.Sprintf("%s:%d: unknown value %q", name, i+1, m[2]))
				}
			default:
				problems = append(problems, fmt.Sprintf("%s:%d: unknown directive %q", name, i+1, m[1]))
			}
		}
		if depth != 0 {
			problems = append(problems, fmt.Sprintf("%s: %d block(s) never ended", name, depth))
		}
		if name == "protocol.mc26tmpl.md" {
			for _, k := range kinds {
				if !strings.Contains(string(b), "\n### "+k+"\n") {
					problems = append(problems, fmt.Sprintf("%s: node kind %q has no \"### %s\" section", name, k, k))
				}
			}
		}
	}
	return problems
}
