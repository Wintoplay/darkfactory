//go:build !linux

package nativeattach

import "errors"

func Run(string, string, int, int) error {
	return errors.New("native attach is available on Linux only")
}
