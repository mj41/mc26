package mcver

import (
	"sort"
	"strings"
	"testing"
)

func TestOrder(t *testing.T) {
	vs := []string{"26.3", "26.1", "26.3-pre-2", "26.10", "26.3-rc-1", "26.3-pre-1", "26.2", "26.3-snapshot-4"}
	sort.Slice(vs, func(i, j int) bool { return Less(vs[i], vs[j]) })
	want := "26.1 26.2 26.3-snapshot-4 26.3-pre-1 26.3-pre-2 26.3-rc-1 26.3 26.10"
	if got := strings.Join(vs, " "); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestMatches(t *testing.T) {
	cases := []struct {
		v, cond string
		want    bool
	}{
		{"26.2", "26.1 26.2", true}, {"26.3", "26.1 26.2", false},
		{"26.3-pre-3", ">= 26.3", false}, {"26.3-pre-3", ">= 26.3-pre-1", true},
		{"26.1", "< 26.3", true}, {"26.3", "< 26.3", false}, {"26.2", "== 26.2", true},
	}
	for _, c := range cases {
		got, err := Matches(c.v, c.cond)
		if err != nil || got != c.want {
			t.Errorf("Matches(%q, %q) = %v, %v; want %v", c.v, c.cond, got, err, c.want)
		}
	}
	if _, err := Matches("26.2", ">= soon"); err == nil {
		t.Error("a non-version target must be an error")
	}
}
