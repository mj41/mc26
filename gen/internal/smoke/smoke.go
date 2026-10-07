// Package smoke runs a vanilla Minecraft server of one version — in a JDK 25
// container by default, on the host's Java with Runtime "host" — and the smoke
// tests against it: the kit's `bot` and the library's `management`, run from
// the assembled kit tree (`go test -run TestSmoke`) with MC26_SMOKE_ADDR,
// MC26_SMOKE_RCON, MC26_SMOKE_RCON_PASSWORD, MC26_SMOKE_MGMT and
// MC26_SMOKE_MGMT_SECRET set. Package e2e reuses the server for the example bots.
package smoke

import (
	"fmt"
	"github.com/mj41/mc26/gen/internal/limits"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mj41/mc26/gen/internal/extract"
	"github.com/mj41/mc26/gen/internal/kit"
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
	// MgmtPort and MgmtSecret are the management protocol's (JSON-RPC over a
	// WebSocket, plain text here); the secret is generated when empty.
	MgmtPort   int
	MgmtSecret string
	Runtime    string // "podman" or "docker" (detected when empty), or "host" for the host's java
	Log        func(format string, args ...any)
	// LevelType is the world's generator (minecraft:normal); empty is the
	// flat world every scenario but one plays on. Seed is its seed.
	LevelType string
	Seed      string

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

// MgmtAddr is where the management protocol answers.
func (s *Server) MgmtAddr() string { return fmt.Sprintf("127.0.0.1:%d", s.MgmtPort) }

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
	if s.MgmtPort == 0 {
		s.MgmtPort = s.Port + 2
	}
	if s.MgmtSecret == "" {
		// the server insists on exactly 40 alphanumeric characters
		const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		b := make([]byte, 40)
		for i := range b {
			b[i] = alnum[rand.Intn(len(alnum))]
		}
		s.MgmtSecret = string(b)
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
	port, rcon, mgmt, mgmtHost := s.Port, s.RCONPort, s.MgmtPort, "127.0.0.1"
	if s.Runtime != "host" {
		port, rcon, mgmt, mgmtHost = 25565, 25575, 25585, "0.0.0.0"
	}
	// the flat world without villages: each version puts its own elsewhere,
	// and a scenario would meet a dirt path or a house in one version only
	levelType, structures := s.LevelType, true
	if levelType == "" {
		levelType, structures = "minecraft:flat", false
	}
	levelType = strings.ReplaceAll(levelType, ":", `\:`)
	props := fmt.Sprintf(`online-mode=false
white-list=false
enforce-whitelist=false
server-port=%d
enable-rcon=true
rcon.port=%d
rcon.password=%s
management-server-enabled=true
management-server-host=%s
management-server-port=%d
management-server-secret=%s
management-server-tls-enabled=false
level-type=%s
level-seed=%s
generate-structures=%t
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
`, port, rcon, s.RCONPassword, mgmtHost, mgmt, s.MgmtSecret, levelType, s.Seed, structures)
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
			"--memory", limits.ServerContainerMemory,
			"--security-opt", "label=disable",
			"-p", fmt.Sprintf("127.0.0.1:%d:25565", s.Port),
			"-p", fmt.Sprintf("127.0.0.1:%d:25575", s.RCONPort),
			"-p", fmt.Sprintf("127.0.0.1:%d:25585", s.MgmtPort),
			"-v", work + ":/data", "-w", "/data",
		}
		// MC26_PLAY_ADDR: the game's port also on this address (a LAN one),
		// for a person to join and watch the robots from their own client —
		// the game only; RCON and the management protocol stay local
		if a := os.Getenv("MC26_PLAY_ADDR"); a != "" {
			args = append(args, "-p", fmt.Sprintf("%s:%d:25565", a, s.Port))
			s.Log("players can join at %s:%d", a, s.Port)
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
	s.Log("server %s: %s, :%d (rcon :%d, management :%d), log %s", s.Version, where, s.Port, s.RCONPort, s.MgmtPort, s.LogPath())
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
	KitDir  string // the kit tree assembled against the built library of the same version
	JarPath string // the vanilla server jar of the same version
	WorkDir string // scratch directory for the server
	Port    int
	Runtime string // see Server.Runtime
	Timeout time.Duration
	Log     func(format string, args ...any)
	// TestRun selects which tests run (the -run pattern); empty means TestSmoke.
	TestRun string
	// Env is added to the test's environment, for a test that needs more than
	// the server's address (the capture file, say).
	Env []string
	// ClientAddr is what the test connects to when that is not the server
	// itself: a recording proxy in front of it.
	ClientAddr string
}

// Run starts the server, runs the smoke tests from the kit tree, stops the server.
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
	run := o.TestRun
	if run == "" {
		run = "TestSmoke"
	}
	o.Log("smoke: server ready, running go test ./bot %s/management -run %s in the kit tree", kit.LibModule, run)
	test := exec.Command("go", "test", "./bot", kit.LibModule+"/management", "-run", run, "-count=1", "-v")
	test.Dir = o.KitDir
	addr := srv.Addr()
	if o.ClientAddr != "" {
		addr = o.ClientAddr
	}
	test.Env = kit.Env(o.KitDir,
		"MC26_SMOKE_ADDR="+addr,
		"MC26_SMOKE_RCON="+srv.RCONAddr(),
		"MC26_SMOKE_RCON_PASSWORD="+srv.RCONPassword,
		"MC26_SMOKE_MGMT="+srv.MgmtAddr(),
		"MC26_SMOKE_MGMT_SECRET="+srv.MgmtSecret,
	)
	test.Env = append(test.Env, o.Env...)
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
