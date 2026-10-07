package extract

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// A jar that matches the sha1 kept beside it is used without asking Mojang
// (the version does not exist, so any fetch would fail).
func TestServerJarCachedOffline(t *testing.T) {
	dir := t.TempDir()
	jar := filepath.Join(dir, "0.0-test-server.jar")
	os.WriteFile(jar, []byte("a jar"), 0o644)
	sum := sha1.Sum([]byte("a jar"))
	os.WriteFile(jar+".sha1", []byte(hex.EncodeToString(sum[:])+"\n"), 0o644)
	got, err := ServerJar(dir, "0.0-test", func(string, ...any) {})
	if err != nil || got != jar {
		t.Fatalf("ServerJar: %q, %v", got, err)
	}
}
