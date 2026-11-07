//go:build !linux || !cgo

package terminal

import (
	"errors"
	"unsafe"
)

// OnMessage is a no-op without the native WebView.
func OnMessage(_ func(string)) {}

// AttachBridge is a no-op without the native WebView.
func AttachBridge(_ unsafe.Pointer) error {
	return errors.New("the page bridge needs Linux, cgo and WebKitGTK")
}
