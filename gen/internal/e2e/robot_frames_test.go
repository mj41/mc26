package e2e

import "testing"

func TestRobotFrames(t *testing.T) {
	dump := "goroutine 7 [select]:\nmain.(*robot).descend(...)\n\t/work/temp/kit/26.3/examples/robot/mine.go:174 +0x11c5\nruntime.gopark()\n\t/usr/lib/go/src/runtime/proc.go:1\nmain.runOrder()\n\t/work/temp/kit/26.3/examples/robot/orders.go:136 +0x190\n\t/work/temp/kit/26.3/examples/robot/mine.go:174 +0x11c5\n"
	got := robotFrames(dump, 20)
	want := "  examples/robot/mine.go:174 +0x11c5\n  examples/robot/orders.go:136 +0x190"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
