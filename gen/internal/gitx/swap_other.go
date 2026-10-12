//go:build !linux

package gitx

import "errors"

func exchange(a, b string) error { return errors.New("no exchange of two directories here") }
