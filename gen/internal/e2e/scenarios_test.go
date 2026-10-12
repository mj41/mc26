package e2e

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/mj41/mc26/gen/internal/smoke"
)

// TestListIsTheKnownScenarios: --list prints a JSON object a line for each scenario --only knows, in the harness's
// order, each name one a bench takes, each with a one-line description.
func TestListIsTheKnownScenarios(t *testing.T) {
	var out bytes.Buffer
	if err := List(&out); err != nil {
		t.Fatal(err)
	}
	scenarios, after := scenarioTable(Options{}, nil, new(string), new(*smoke.Server))
	known := scenarioNames(scenarios, after)
	var got []string
	seen := map[string]bool{}
	for _, l := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		var e listEntry
		dec := json.NewDecoder(strings.NewReader(l))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil {
			t.Fatalf("%q: %v", l, err)
		}
		if !scenarioRE.MatchString(e.Name) {
			t.Errorf("%q is not a name a bench takes", e.Name)
		}
		if e.What == "" || len(e.What) > 120 || seen[e.Name] {
			t.Errorf("%q: description %q, repeated %v", e.Name, e.What, seen[e.Name])
		}
		seen[e.Name] = true
		got = append(got, e.Name)
	}
	if !slices.Equal(got, known) {
		t.Errorf("listed %v, known %v", got, known)
	}
	for _, n := range []string{"robot", "botday1off", "botdays5script", "botbrick8"} {
		if !seen[n] {
			t.Errorf("no %s in the list", n)
		}
	}
}
