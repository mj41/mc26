// Package paths locates the mc26 checkout and its scratch directories, so
// every command agrees on where extracted data and cached jars live:
//
//	<root>/temp/data/<version>   extracted JSON (or MC26_DATA/<version>)
//	<root>/temp/cache            server jars
//	<root>/temp/lib/<version>    build output of pipeline
//	<root>/temp/smoke/<version>  the smoke test's server
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// Root returns the mc26 checkout: MC26_ROOT, or the nearest ancestor of the
// working directory — or of the running binary, for a binary built under
// <root>/temp — that contains data-gen/java/ExtractAll.java.
func Root() (string, error) {
	if r := os.Getenv("MC26_ROOT"); r != "" {
		return r, nil
	}
	starts := []string{}
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, dir := range starts {
		for {
			if _, err := os.Stat(filepath.Join(dir, "data-gen", "java", "ExtractAll.java")); err == nil {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", fmt.Errorf("not inside an mc26 checkout (set MC26_ROOT or run from the repository)")
}

// MustRoot is Root or exit.
func MustRoot() string {
	r, err := Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return r
}

// Temp is the scratch directory (gitignored).
func Temp() string { return filepath.Join(MustRoot(), "temp") }

// DataRoot holds one extracted directory per version: MC26_DATA or temp/data.
func DataRoot() string {
	if d := os.Getenv("MC26_DATA"); d != "" {
		return d
	}
	return filepath.Join(Temp(), "data")
}

// Data resolves a version id or an existing directory to a data directory.
func Data(versionOrDir string) string {
	if fi, err := os.Stat(versionOrDir); err == nil && fi.IsDir() {
		return versionOrDir
	}
	return filepath.Join(DataRoot(), versionOrDir)
}

// Cache holds the downloaded server jars.
func Cache() string { return filepath.Join(Temp(), "cache") }

// Lib is the built library tree of a version.
func Lib(version string) string { return filepath.Join(Temp(), "lib", version) }

// Kit is the kit tree assembled against the built library of a version.
func Kit(version string) string { return filepath.Join(Temp(), "kit", version) }

// Fixtures is the small world the save package's tests read for a version
// (`mc26 fixtures`); the build copies it into the library as save/testdata/world.
func Fixtures(version string) string {
	return filepath.Join(MustRoot(), "gen", "src", "save", "testdata", version)
}
