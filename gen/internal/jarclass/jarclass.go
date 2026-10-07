// Package jarclass finds classes in a jar by name, the way the reading tools
// (javap, source) let a person name one: by its simple name or a suffix of it.
package jarclass

import (
	"archive/zip"
	"sort"
	"strings"
)

// Names reads the jar's index: the class files it holds, in Java form.
func Names(jar string) ([]string, error) {
	zr, err := zip.OpenReader(jar)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var out []string
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".class") {
			continue
		}
		out = append(out, strings.ReplaceAll(strings.TrimSuffix(f.Name, ".class"), "/", "."))
	}
	sort.Strings(out)
	return out, nil
}

// Match returns the classes whose name ends in want: an exact fully qualified
// name, a simple name (SetJigsawBlock matches …ServerboundSetJigsawBlockPacket
// only when nothing matches exactly), or a nested one (Foo$Bar). Listing wants
// them all — a class and the nested classes named after it — while reading one
// wants the closest.
func Match(classes []string, want string, all bool) []string {
	var exact, tail, contains []string
	for _, c := range classes {
		simple := c[strings.LastIndex(c, ".")+1:]
		switch {
		case c == want || simple == want:
			exact = append(exact, c)
		case strings.HasSuffix(c, "."+want) || strings.HasSuffix(simple, want):
			tail = append(tail, c)
		case strings.Contains(simple, want):
			contains = append(contains, c)
		}
	}
	if all {
		return append(append(exact, tail...), contains...)
	}
	for _, s := range [][]string{exact, tail, contains} {
		if len(s) > 0 {
			return s
		}
	}
	return nil
}
