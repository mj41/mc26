//go:build linux

package gitx

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"
)

// renameat2's number, by architecture (the standard library does not name it).
var sysRenameat2 = map[string]uintptr{"amd64": 316, "arm64": 276, "386": 353, "arm": 382}

// exchange is renameat2(AT_FDCWD, a, AT_FDCWD, b, RENAME_EXCHANGE).
func exchange(a, b string) error {
	nr, ok := sysRenameat2[runtime.GOARCH]
	if !ok {
		return errors.New("no renameat2 number for " + runtime.GOARCH)
	}
	const renameExchange = 2
	fdcwd := ^uintptr(99) // AT_FDCWD, -100
	pa, err := syscall.BytePtrFromString(a)
	if err != nil {
		return err
	}
	pb, err := syscall.BytePtrFromString(b)
	if err != nil {
		return err
	}
	if _, _, e := syscall.Syscall6(nr, fdcwd, uintptr(unsafe.Pointer(pa)), fdcwd, uintptr(unsafe.Pointer(pb)), renameExchange, 0); e != 0 {
		return e
	}
	return nil
}
