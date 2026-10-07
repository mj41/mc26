package paths

import (
	"path/filepath"
	"testing"
)

// A relative directory in the environment comes back absolute: given to
// podman -v as it is, it would be a named volume.
func TestEnvDirsAbsolute(t *testing.T) {
	t.Setenv("MC26_TEMP", "rel/temp")
	t.Setenv("MC26_CACHE", "rel/cache")
	t.Setenv("MC26_DATA", "rel/data")
	for name, got := range map[string]string{"Temp": Temp(), "Cache": Cache(), "DataRoot": DataRoot()} {
		if !filepath.IsAbs(got) {
			t.Errorf("%s() = %q, not absolute", name, got)
		}
	}
}
