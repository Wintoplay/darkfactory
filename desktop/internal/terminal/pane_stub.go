//go:build !linux || !cgo

package terminal

import (
	"errors"
	"unsafe"
)

// Bounds is the web terminal rectangle in window (logical, CSS) pixels.
type Bounds struct {
	X int
	Y int
	W int
	H int
}

func Open(_ unsafe.Pointer, _, _, _, _ string, _, _ int, _ Bounds) error {
	return errors.New("the in-app terminal needs Linux, cgo and VTE")
}

func Place(_ Bounds) {}

// SetVisible is a no-op without a native toolkit.
func SetVisible(_ bool) {}

// Prepare is a no-op without a native toolkit.
func Prepare(_ unsafe.Pointer) error {
	return errors.New("the in-app terminal needs Linux, cgo and VTE")
}

func Close() {}
