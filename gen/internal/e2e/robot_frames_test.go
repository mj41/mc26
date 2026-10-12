package e2e

import "testing"

func TestRobotFrames(t *testing.T) {
	dump := "goroutine 7 [select]:\nmain.(*robot).keepMemory()\n\t/work/temp/kit/26.3/examples/robot/memory.go:129 +0x19e\n\n" +
		"goroutine 9 [select]:\nmain.(*robot).walkTo()\n\t/work/temp/kit/26.3/examples/robot/console.go:1373 +0xaf\nruntime.gopark()\n\t/usr/lib/go/src/runtime/proc.go:1\nmain.runOrder()\n\t/work/temp/kit/26.3/examples/robot/orders.go:136 +0x190\n"
	if got, want := robotFrames(dump, 20), "  examples/robot/console.go:1373 +0xaf\n  examples/robot/orders.go:136 +0x190"; got != want {
		t.Fatalf("the order's goroutine: got %q, want %q", got, want)
	}
	if got, want := robotFrames("goroutine 7:\n\t/x/examples/robot/memory.go:129 +0x1\n", 20), "  examples/robot/memory.go:129 +0x1"; got != want {
		t.Fatalf("no order: got %q, want %q", got, want)
	}
}
