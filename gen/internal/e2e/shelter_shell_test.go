package e2e

import "testing"

func TestShelterShell(t *testing.T) {
	has := func(cells [][3]int, c [3]int) bool {
		for _, x := range cells {
			if x == c {
				return true
			}
		}
		return false
	}
	// a room dug into a hill, going in west (fdx -1): the way in at +2, the tunnel on at -2
	dug := shelterShell("dug", -1, 0)
	for _, c := range [][3]int{
		{2, 0, 0}, {2, 0, 1}, // the way in
		{0, 0, -1}, {0, 0, 2}, {1, 1, -1}, {-1, -1, 2}, // floor and roof
		{0, 2, 0}, {0, -2, 1}, // the side walls
		{-2, 1, 0}, {-2, -1, 1}, // the walls of the tunnel's first cell
		{-2, 0, 2}, {-2, 0, -1}, // its roof and floor
		{-3, 0, 0},
	} {
		if c[0] == -3 { // (a cell beyond the box: not listed, the tunnel goes on)
			if has(dug, c) {
				t.Errorf("dug: %v listed, it is beyond the room", c)
			}
			continue
		}
		if !has(dug, c) {
			t.Errorf("dug: %v is a wall, floor, roof or way in and is not checked", c)
		}
	}
	// the cells the 26.3 dry run failed on: the roof's edge over the way in, touching the room only along an edge
	for _, c := range [][3]int{{2, 1, 2}, {2, -1, 2}, {2, 1, -1}, {2, -1, -1}, {1, 2, 2}, {-1, 2, -1}, {2, 2, 0}, {-2, 2, 1}} {
		if has(dug, c) {
			t.Errorf("dug: %v is checked and touches the room only at an edge or a corner", c)
		}
	}
	if has(dug, [3]int{-2, 0, 0}) || has(dug, [3]int{-2, 0, 1}) {
		t.Error("dug: the tunnel's own cells are checked, they are open")
	}
	// every cell that touches the room by a face is in the list, for either kind
	for _, how := range []string{"dug", "built"} {
		cells := shelterShell(how, 1, 0)
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				for dy := 0; dy <= 1; dy++ {
					for _, d := range [6][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
						n := [3]int{dx + d[0], dz + d[1], dy + d[2]}
						inRoom := n[0] >= -1 && n[0] <= 1 && n[1] >= -1 && n[1] <= 1 && n[2] >= 0 && n[2] <= 1
						tunnel := how == "dug" && n[0] == 2 && n[1] == 0 && n[2] >= 0 && n[2] <= 1
						if !inRoom && !tunnel && !has(cells, n) {
							t.Errorf("%s: %v touches the room and is not checked", how, n)
						}
					}
				}
			}
		}
	}
	// a built hut has no tunnel: its far wall is a wall
	if built := shelterShell("built", -1, 0); !has(built, [3]int{-2, 0, 0}) || !has(built, [3]int{-2, 0, 1}) {
		t.Error("built: the far wall of a hut must be checked")
	}
}
