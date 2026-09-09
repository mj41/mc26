// Package limits keeps what the harness starts within the memory of a laptop.
// A build compiles a library of 1.2 million lines (the translation tables),
// the smoke and e2e servers and the extractor are JVMs in containers, and a
// run over every version does all of it in turn; unbounded, a build compiles
// as many packages at once as there are cores and a JVM takes a quarter of
// the machine. Every go command the harness runs compiles a few packages at a
// time, every container has a memory limit, every JVM a heap limit.
package limits

import (
	"os"
	"strconv"
	"strings"
)

// GoParallel is how many packages a go build, vet or test compiles at once
// (the -p flag): the generated library's packages are large, and one compile
// of a translation table takes hundreds of megabytes.
const GoParallel = 4

// ServerContainerMemory bounds a vanilla server's container: the JVM heap is
// 1536M (smoke.Server), the rest is netty's direct buffers and the runtime.
const ServerContainerMemory = "2500m"

// ExtractContainerMemory bounds the extraction container, and ExtractJavaHeap
// every JVM in it (Mojang's data generator and the extractors) through
// JAVA_TOOL_OPTIONS.
const (
	ExtractContainerMemory = "6g"
	ExtractJavaHeap        = "-Xmx4g"
)

// GoEnv is the environment for a go command: the caller's, with -p added to
// GOFLAGS (after whatever GOFLAGS already held), plus the extra variables.
func GoEnv(extra ...string) []string {
	env := os.Environ()
	flags := "-p=" + strconv.Itoa(GoParallel)
	for i, kv := range env {
		if strings.HasPrefix(kv, "GOFLAGS=") {
			env[i] = kv + " " + flags
			return append(env, extra...)
		}
	}
	env = append(env, "GOFLAGS="+flags)
	return append(env, extra...)
}
