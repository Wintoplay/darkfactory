package httpapi

import (
	"errors"
	"os/exec"
	"runtime"
)

var errNoFileManager = errors.New("no file manager")

// revealFolder is replaceable in tests: starting a desktop file manager from a
// test suite would open a window on the developer's screen.
var revealFolder = openInFileManager

// openInFileManager asks the desktop to show a directory. It starts the helper
// and releases it instead of waiting: a file manager stays open, so Wait would
// hold the HTTP request (and the audited outcome row) open for as long as the
// window lives.
func openInFileManager(path string) error {
	candidates := [][]string{
		{"xdg-open", path},
		{"gio", "open", path},
	}
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates, []string{"open", path})
	case "windows":
		candidates = append(candidates, []string{"explorer", path})
	}
	for _, candidate := range candidates {
		bin, err := exec.LookPath(candidate[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(bin, candidate[1:]...)
		if err := cmd.Start(); err != nil {
			continue
		}
		_ = cmd.Process.Release()
		return nil
	}
	return errNoFileManager
}
