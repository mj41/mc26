// Package mcver orders Minecraft version ids the way Mojang's manifest does:
// 26.1 < 26.3-snapshot-4 < 26.3-pre-1 < 26.3-pre-2 < 26.3-rc-1 < 26.3 < 26.10.
package mcver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var idRe = regexp.MustCompile(`^(\d+)\.(\d+)(?:-([a-z]+)-?(\d+))?$`)

// Key is what a version id sorts by: year, number, kind (snapshot 1, pre 2,
// rc 3, release 9) and the kind's counter.
func Key(v string) ([4]int, bool) {
	m := idRe.FindStringSubmatch(v)
	if m == nil {
		return [4]int{}, false
	}
	yy, _ := strconv.Atoi(m[1])
	n, _ := strconv.Atoi(m[2])
	if m[3] == "" {
		return [4]int{yy, n, 9, 0}, true
	}
	kind := map[string]int{"snapshot": 1, "pre": 2, "rc": 3}[m[3]]
	k, _ := strconv.Atoi(m[4])
	return [4]int{yy, n, kind, k}, true
}

// Less says whether a sorts before b; ids that are not versions sort by text.
func Less(a, b string) bool {
	ka, okA := Key(a)
	kb, okB := Key(b)
	if !okA || !okB {
		return a < b
	}
	for i := range ka {
		if ka[i] != kb[i] {
			return ka[i] < kb[i]
		}
	}
	return a < b
}

// Compare is -1, 0 or 1.
func Compare(a, b string) int {
	switch {
	case a == b:
		return 0
	case Less(a, b):
		return -1
	}
	return 1
}

// Matches evaluates a version condition: a list of ids ("26.1 26.2"), or one
// comparison ("< 26.3", ">= 26.3-pre-1", "== 26.2"). A release sorts after its
// pre-releases, so ">= 26.3" excludes 26.3-pre-3; name the pre-release to
// include it.
func Matches(version, cond string) (bool, error) {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return false, fmt.Errorf("empty version condition")
	}
	for _, op := range []string{">=", "<=", "==", ">", "<"} {
		if strings.HasPrefix(cond, op) {
			target := strings.TrimSpace(cond[len(op):])
			if _, ok := Key(target); !ok {
				return false, fmt.Errorf("%q is not a Minecraft version id", target)
			}
			c := Compare(version, target)
			switch op {
			case ">=":
				return c >= 0, nil
			case "<=":
				return c <= 0, nil
			case "==":
				return c == 0, nil
			case ">":
				return c > 0, nil
			default:
				return c < 0, nil
			}
		}
	}
	for _, id := range strings.Fields(cond) {
		if _, ok := Key(id); !ok {
			return false, fmt.Errorf("%q is not a Minecraft version id", id)
		}
		if id == version {
			return true, nil
		}
	}
	return false, nil
}
