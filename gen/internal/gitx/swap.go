package gitx

import "os"

// SwapDir puts the directory stage in the place of final in one step: a reader
// of final sees the old tree or the new one, never a half-made one. With both
// there it exchanges them (renameat2 with RENAME_EXCHANGE, the old tree is then
// at stage, for the caller to remove); where the kernel has no exchange it
// moves final aside and stage in, which leaves a moment without final. With no
// final yet it is a rename.
func SwapDir(final, stage string) error {
	if _, err := os.Stat(final); os.IsNotExist(err) {
		return os.Rename(stage, final)
	}
	if err := exchange(final, stage); err == nil {
		return nil
	}
	old := final + ".old"
	if err := os.RemoveAll(old); err != nil {
		return err
	}
	if err := os.Rename(final, old); err != nil {
		return err
	}
	if err := os.Rename(stage, final); err != nil {
		_ = os.Rename(old, final) // (put back what was there)
		return err
	}
	return os.Rename(old, stage) // (the old tree where the exchange would have left it)
}
