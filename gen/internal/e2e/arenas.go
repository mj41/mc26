package e2e

// Walled arenas (see sharedArenaWorld in world.go).

type arenaRCON interface {
	command(string) (string, error)
}

const arenaHalf = 32

func arenaCommands(k, ox int) []string { return nil }

func arenaRecipeHash(n int) string { return "" }

func buildArenaWorld(n int, rc arenaRCON, stop func() error) error { return nil }

func arenaWorld(workdir, from, recipeHash string, n int, build func(dir string) error) (string, error) {
	return "", nil
}
