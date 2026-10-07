// javap disassembles a class of a cached server jar, so a rule the packet
// extractor still needs can be read from the bytecode it has to interpret.
// The class is found by suffix in the jar's own index, and the disassembly is
// cached under temp/javap/<version>/, so asking for the same class twice costs
// nothing.
//
//	go run ./gen/cmd/javap 26.2 SetJigsawBlock          # the whole class, with code
//	go run ./gen/cmd/javap -m read 26.2 ClientboundWaypointPacket   # one member
//	go run ./gen/cmd/javap -l 26.2 Waypoint             # just the matching class names
//	go run ./gen/cmd/javap -s 26.2 SetJigsawBlock       # signatures only, no code
//	go run ./gen/cmd/javap -client 26.2 ClientPacketListener   # the client jar instead
//	go run ./gen/cmd/javap -v 26.2 ByteBufCodecs               # with BootstrapMethods, to resolve a lambda
//
// The client jar is downloaded on first use and is there for reading only: it
// says what the other end of the wire does with a packet, which is how a rule
// the server code leaves ambiguous gets settled.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mj41/mc26/gen/internal/extract"
	"github.com/mj41/mc26/gen/internal/jarclass"
	"github.com/mj41/mc26/gen/internal/paths"
)

func main() {
	member := flag.String("m", "", "print only the members whose signature contains this")
	list := flag.Bool("l", false, "list the matching class names and stop")
	sigs := flag.Bool("s", false, "signatures only: no -c disassembly")
	force := flag.Bool("f", false, "disassemble again even if it is cached")
	client := flag.Bool("client", false, "read the client jar instead of the server's")
	verbose := flag.Bool("v", false, "also the constant pool and BootstrapMethods, which name what an invokedynamic calls")
	flag.Parse()
	if flag.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: javap [-m member] [-l] [-s] [-v] [-f] [-client] <version> <class-suffix>...")
		os.Exit(2)
	}
	version := flag.Arg(0)
	jar := filepath.Join(paths.Cache(), version+"-inner.jar")
	if *client {
		var err error
		jar, err = extract.ClientJar(paths.Cache(), version, func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) })
		if err != nil {
			fail("%v", err)
		}
	}
	if _, err := os.Stat(jar); err != nil {
		fail("%s not found; run `mc26 extract -version %s` first", jar, version)
	}
	classes, err := jarclass.Names(jar)
	if err != nil {
		fail("%s: %v", jar, err)
	}

	for _, want := range flag.Args()[1:] {
		found := jarclass.Match(classes, want, *list)
		if len(found) == 0 {
			fail("no class of %s ends in %q", version, want)
		}
		if *list {
			for _, c := range found {
				fmt.Println(c)
			}
			continue
		}
		if len(found) > 1 {
			fail("%q matches %d classes; name one of them:\n  %s", want, len(found), strings.Join(found, "\n  "))
		}
		out, err := disassemble(version, jar, found[0], *sigs, *verbose, *force)
		if err != nil {
			fail("%v", err)
		}
		if *member != "" {
			out = members(out, *member)
		}
		fmt.Print(out)
	}
}

// disassemble runs javap in the JDK container and caches what it printed.
func disassemble(version, jar, class string, sigs, verbose, force bool) (string, error) {
	dir := filepath.Join(paths.Temp(), "javap", version)
	if strings.HasSuffix(jar, "-client.jar") {
		dir = filepath.Join(dir, "client")
	}
	name := class
	if sigs {
		name += ".sigs"
	}
	if verbose {
		name += ".v"
	}
	cached := filepath.Join(dir, name+".txt")
	if !force {
		if b, err := os.ReadFile(cached); err == nil {
			return string(b), nil
		}
	}
	runtime := extract.DetectRuntime()
	if runtime == "" {
		return "", fmt.Errorf("neither podman nor docker found in PATH")
	}
	args := []string{
		"run", "--rm",
		"--security-opt", "label=disable",
		"-v", filepath.Dir(jar) + ":/cache:ro",
		extract.JDKImage,
		"javap", "-p",
	}
	if !sigs {
		args = append(args, "-c")
	}
	// A lambda shows up only as `InvokeDynamic #n`; -v prints the BootstrapMethods
	// table that says which method it really calls.
	if verbose {
		args = append(args, "-v")
	}
	args = append(args, "-cp", "/cache/"+filepath.Base(jar), class)
	cmd := exec.Command(runtime, args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("javap %s: %w", class, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(cached, out, 0o644); err != nil {
		return "", err
	}
	return string(out), nil
}

// members keeps the blocks of a javap listing whose signature line mentions
// want. javap separates members with a blank line and indents them, so a block
// is what follows an indented signature until the next one.
func members(out, want string) string {
	var kept []string
	var cur []string
	keep := false
	for _, line := range strings.Split(out, "\n") {
		isSignature := strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.TrimSpace(line) != ""
		if isSignature {
			if keep {
				kept = append(kept, strings.Join(cur, "\n"))
			}
			cur = nil
			keep = strings.Contains(line, want)
		}
		if keep {
			cur = append(cur, line)
		}
	}
	if keep {
		kept = append(kept, strings.Join(cur, "\n"))
	}
	if len(kept) == 0 {
		return fmt.Sprintf("(no member of the class mentions %q)\n", want)
	}
	return strings.Join(kept, "\n") + "\n"
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "javap: "+format+"\n", args...)
	os.Exit(1)
}
