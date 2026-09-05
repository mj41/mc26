// Package smoke runs the library's smoke test against a vanilla server of the
// same Minecraft version: it starts the server from Mojang's jar in a scratch
// directory (offline mode, flat world), waits until it is ready, runs
// `go test ./bot -run TestSmoke` with MC26_SMOKE_ADDR set, and stops the
// server.
package smoke

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Options configures one smoke run.
type Options struct {
	LibDir  string // a built library tree
	JarPath string // the vanilla server jar of the same version
	WorkDir string // scratch directory for the server (world, logs)
	Port    int
	Timeout time.Duration // server start-up budget
	Log     func(format string, args ...any)
}

// Run starts the server, runs the test, stops the server.
func Run(o Options) error {
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if o.Port == 0 {
		o.Port = 25599
	}
	if o.Timeout == 0 {
		o.Timeout = 4 * time.Minute
	}
	if err := checkJava(); err != nil {
		return err
	}
	if err := os.MkdirAll(o.WorkDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.WorkDir, "eula.txt"), []byte("eula=true\n"), 0o644); err != nil {
		return err
	}
	props := fmt.Sprintf(`online-mode=false
server-port=%d
enable-rcon=false
level-type=minecraft\:flat
level-name=world
spawn-protection=0
max-players=5
view-distance=6
simulation-distance=4
gamemode=survival
difficulty=peaceful
enforce-secure-profile=false
network-compression-threshold=256
sync-chunk-writes=false
`, o.Port)
	if err := os.WriteFile(filepath.Join(o.WorkDir, "server.properties"), []byte(props), 0o644); err != nil {
		return err
	}

	logPath := filepath.Join(o.WorkDir, "server.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logFile.Close()
	jar, _ := filepath.Abs(o.JarPath)
	server := exec.Command("java", "-Xms512M", "-Xmx1536M", "-jar", jar, "--nogui")
	server.Dir = o.WorkDir
	server.Stdout, server.Stderr = logFile, logFile
	if err := server.Start(); err != nil {
		return fmt.Errorf("starting the server: %w", err)
	}
	o.Log("smoke: vanilla server pid %d on :%d, log %s", server.Process.Pid, o.Port, logPath)
	defer stop(server, o.Log)

	if err := waitReady(logPath, o.Timeout, server); err != nil {
		return err
	}
	o.Log("smoke: server ready, running go test ./bot -run TestSmoke")

	test := exec.Command("go", "test", "./bot", "-run", "TestSmoke", "-count=1", "-v")
	test.Dir = o.LibDir
	test.Env = append(os.Environ(), fmt.Sprintf("MC26_SMOKE_ADDR=127.0.0.1:%d", o.Port))
	out, err := test.CombinedOutput()
	o.Log("%s", strings.TrimSpace(string(out)))
	if err != nil {
		return fmt.Errorf("smoke test failed: %w", err)
	}
	return nil
}

func checkJava() error {
	out, err := exec.Command("java", "-version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("java not found (Minecraft 26.x needs Java 25): %v", err)
	}
	s := string(out)
	for _, old := range []string{"\"1.", "\"17.", "\"21.", "\"22.", "\"23.", "\"24."} {
		if strings.Contains(s, "version "+old) {
			return fmt.Errorf("java too old for Minecraft 26.x (need 25): %s", strings.SplitN(s, "\n", 2)[0])
		}
	}
	return nil
}

func waitReady(logPath string, timeout time.Duration, server *exec.Cmd) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(logPath)
		if strings.Contains(string(data), "]: Done (") {
			return nil
		}
		if server.ProcessState != nil && server.ProcessState.Exited() {
			return fmt.Errorf("server exited before becoming ready; see %s", logPath)
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("server not ready after %s; see %s", timeout, logPath)
}

func stop(server *exec.Cmd, log func(string, ...any)) {
	if server.Process == nil {
		return
	}
	_ = server.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = server.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = server.Process.Kill()
		<-done
	}
	log("smoke: server stopped")
}
