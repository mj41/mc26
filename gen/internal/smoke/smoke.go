// Package smoke runs a vanilla Minecraft server of one version — in a JDK 25
// container by default, on the host's Java with Runtime "host" — and the
// library's smoke test against it: `go test ./bot -run TestSmoke` with
// MC26_SMOKE_ADDR set. Package e2e reuses the server for the example bots.
package smoke

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mj41/mc26/gen/internal/extract"
)

// Server is one vanilla server instance in a scratch directory (offline
// mode, flat world, RCON enabled).
type Server struct {
	Version      string
	JarPath      string // Mojang's server jar of Version
	WorkDir      string // world, logs, server.properties
	Port         int
	RCONPort     int
	RCONPassword string
	Runtime      string // "podman" or "docker" (detected when empty), or "host" for the host's java
	Log          func(format string, args ...any)

	cmd       *exec.Cmd
	container string
	logFile   *os.File
}

// LogPath is the server's console output.
func (s *Server) LogPath() string { return filepath.Join(s.WorkDir, "server.log") }

// Addr is the address a bot connects to.
func (s *Server) Addr() string { return fmt.Sprintf("127.0.0.1:%d", s.Port) }

// RCONAddr is the RCON address.
func (s *Server) RCONAddr() string { return fmt.Sprintf("127.0.0.1:%d", s.RCONPort) }

// Start writes the configuration and launches the server; WaitReady blocks
// until it accepts players.
func (s *Server) Start() error {
	if s.Log == nil {
		s.Log = func(string, ...any) {}
	}
	if s.Port == 0 {
		s.Port = 25599
	}
	if s.RCONPort == 0 {
		s.RCONPort = s.Port + 1
	}
	if s.RCONPassword == "" {
		s.RCONPassword = "mc26"
	}
	if s.Runtime == "" {
		s.Runtime = extract.DetectRuntime()
		if s.Runtime == "" {
			s.Runtime = "host"
		}
	}
	if err := os.MkdirAll(s.WorkDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.WorkDir, "eula.txt"), []byte("eula=true\n"), 0o644); err != nil {
		return err
	}
	// Inside a container the server listens on the image's default ports and
	// the host ports are published onto them.
	port, rcon := s.Port, s.RCONPort
	if s.Runtime != "host" {
		port, rcon = 25565, 25575
	}
	props := fmt.Sprintf(`online-mode=false
server-port=%d
enable-rcon=true
rcon.port=%d
rcon.password=%s
level-type=minecraft\:flat
level-name=world
spawn-protection=0
max-players=20
view-distance=6
simulation-distance=4
gamemode=survival
difficulty=peaceful
enforce-secure-profile=false
network-compression-threshold=256
sync-chunk-writes=false
`, port, rcon, s.RCONPassword)
	if err := os.WriteFile(filepath.Join(s.WorkDir, "server.properties"), []byte(props), 0o644); err != nil {
		return err
	}
	logFile, err := os.Create(s.LogPath())
	if err != nil {
		return err
	}
	s.logFile = logFile

	javaArgs := []string{"-Xms512M", "-Xmx1536M", "-jar", "", "--nogui"}
	if s.Runtime == "host" {
		if err := checkHostJava(); err != nil {
			return err
		}
		jar, _ := filepath.Abs(s.JarPath)
		javaArgs[3] = jar
		s.cmd = exec.Command("java", javaArgs...)
		s.cmd.Dir = s.WorkDir
	} else {
		// The jar must be inside the mounted directory.
		if err := linkOrCopy(s.JarPath, filepath.Join(s.WorkDir, "server.jar")); err != nil {
			return err
		}
		javaArgs[3] = "/data/server.jar"
		s.container = fmt.Sprintf("mc26-%s-%d", strings.ReplaceAll(s.Version, ".", "-"), s.Port)
		_ = exec.Command(s.Runtime, "rm", "-f", s.container).Run() // a leftover of an aborted run
		work, _ := filepath.Abs(s.WorkDir)
		args := []string{
			"run", "--rm", "--name", s.container,
			"--security-opt", "label=disable",
			"-p", fmt.Sprintf("127.0.0.1:%d:25565", s.Port),
			"-p", fmt.Sprintf("127.0.0.1:%d:25575", s.RCONPort),
			"-v", work + ":/data", "-w", "/data",
		}
		if s.Runtime != "podman" {
			if uid := os.Getuid(); uid > 0 {
				args = append(args, "--user", fmt.Sprintf("%d:%d", uid, os.Getgid()))
			}
		}
		args = append(args, extract.JDKImage, "java")
		args = append(args, javaArgs...)
		s.cmd = exec.Command(s.Runtime, args...)
	}
	s.cmd.Stdout, s.cmd.Stderr = logFile, logFile
	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("starting the server: %w", err)
	}
	where := "host java"
	if s.container != "" {
		where = s.Runtime + " " + s.container
	}
	s.Log("server %s: %s, :%d (rcon :%d), log %s", s.Version, where, s.Port, s.RCONPort, s.LogPath())
	return nil
}

// WaitReady returns once the log says the server is done starting.
func (s *Server) WaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(s.LogPath())
		if strings.Contains(string(data), "]: Done (") {
			return nil
		}
		if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
			return fmt.Errorf("server exited before becoming ready; see %s", s.LogPath())
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("server not ready after %s; see %s", timeout, s.LogPath())
}

// Stop shuts the server down and waits for it; calling it again is a no-op.
func (s *Server) Stop() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	defer func() { s.cmd = nil }()
	if s.container != "" {
		_ = exec.Command(s.Runtime, "stop", "-t", "30", s.container).Run()
	} else {
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
	}
	done := make(chan struct{})
	go func() { _ = s.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(45 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
	if s.logFile != nil {
		_ = s.logFile.Close()
	}
	s.Log("server %s stopped", s.Version)
}

// Options configures one smoke run.
type Options struct {
	Version string
	LibDir  string // a built library tree
	JarPath string // the vanilla server jar of the same version
	WorkDir string // scratch directory for the server
	Port    int
	Runtime string // see Server.Runtime
	Timeout time.Duration
	Log     func(format string, args ...any)
}

// Run starts the server, runs the library's smoke test, stops the server.
func Run(o Options) error {
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if o.Timeout == 0 {
		o.Timeout = 4 * time.Minute
	}
	srv := &Server{Version: o.Version, JarPath: o.JarPath, WorkDir: o.WorkDir, Port: o.Port, Runtime: o.Runtime, Log: o.Log}
	if err := srv.Start(); err != nil {
		return err
	}
	defer srv.Stop()
	if err := srv.WaitReady(o.Timeout); err != nil {
		return err
	}
	o.Log("smoke: server ready, running go test ./bot -run TestSmoke")
	test := exec.Command("go", "test", "./bot", "-run", "TestSmoke", "-count=1", "-v")
	test.Dir = o.LibDir
	test.Env = append(os.Environ(), "MC26_SMOKE_ADDR="+srv.Addr())
	out, err := test.CombinedOutput()
	o.Log("%s", strings.TrimSpace(string(out)))
	if err != nil {
		return fmt.Errorf("smoke test failed: %w", err)
	}
	return nil
}

func checkHostJava() error {
	out, err := exec.Command("java", "-version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("java not found (Minecraft 26.x needs Java 25; use a container runtime instead): %v", err)
	}
	first := strings.SplitN(string(out), "\n", 2)[0]
	if i := strings.Index(first, "\""); i >= 0 {
		major, _, _ := strings.Cut(first[i+1:], ".")
		if n, err := strconv.Atoi(major); err == nil && n < 25 {
			return fmt.Errorf("java too old for Minecraft 26.x (need 25): %s", first)
		}
	}
	return nil
}

func linkOrCopy(src, dst string) error {
	_ = os.Remove(dst)
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
