package e2e

// shelterShell lists the cells, as offsets from the middle of a shelter's room, that must hold something a
// monster does not pass for the room to be closed: every cell that touches (by a face) the room, the way in
// or, in a room dug into a hill, the first cell of the tunnel that leaves it.
//
// The room is three by three and two high (offsets -1..1 across, heights 0 and 1). how is "dug" or "built";
// (fdx, fdz) is the way going in, and a dug room's tunnel leaves the far wall, two cells on that way.
//
// Only those cells are listed, not the whole box round the room: a cell that touches the room only along an
// edge or at a corner (the roof's edge over the way in, say) is no way in for anything. The scenario once
// checked the whole box, and failed on a leaf block there that was part of a tree standing on the hill and
// decayed after the robot cut the tree's logs (26.3 dry run of 2026-10-12): the room was closed all the time.
func shelterShell(how string, fdx, fdz int) [][3]int {
	inside := func(dx, dz, dy int) bool {
		return dx >= -1 && dx <= 1 && dz >= -1 && dz <= 1 && dy >= 0 && dy <= 1
	}
	tunnel := func(dx, dz, dy int) bool {
		return how == "dug" && dx == 2*fdx && dz == 2*fdz && dy >= 0 && dy <= 1
	}
	open := func(dx, dz, dy int) bool { return inside(dx, dz, dy) || tunnel(dx, dz, dy) }
	var out [][3]int
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			for dy := -1; dy <= 2; dy++ {
				if open(dx, dz, dy) {
					continue
				}
				for _, d := range [6][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
					if open(dx+d[0], dz+d[1], dy+d[2]) {
						out = append(out, [3]int{dx, dz, dy})
						break
					}
				}
			}
		}
	}
	return out
}
