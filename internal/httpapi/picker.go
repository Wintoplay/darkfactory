package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var errNoNativePicker = errors.New("no native folder picker")

// isLocalRequest keeps the native picker on the machine the daemon runs on.
// A Tailscale Serve proxy connects from loopback, so the Host check matters,
// and a forwarded request is not local even when its peer is: this is the same
// rule the local UI uses, in one place.
func isLocalRequest(r *http.Request) bool {
	return isLocalUIRequest(r)
}

func isLoopbackAddr(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// handlePickDir opens the desktop folder chooser. The browser cannot read an
// absolute path from <input webkitdirectory> or showDirectoryPicker, so the
// daemon asks the OS and returns the path. Remote/mobile callers get 409 and
// use the server-side browser instead.
func (s *Server) handlePickDir(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		s.refuse(w, r, codePickerRemote, "the native picker only runs on the daemon's machine")
		return
	}
	var body struct {
		Start string `json:"start"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&body)
	}
	path, ok, err := pickDirectory(r.Context(), body.Start)
	if errors.Is(err, errNoNativePicker) {
		s.refuse(w, r, codePickerUnavailable, "")
		return
	}
	if err != nil {
		s.log.Error("native picker failed", "error", err)
		s.refuse(w, r, codePickerFailed, "")
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func pickDirectory(ctx context.Context, start string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return "", false, errNoNativePicker
	}

	for _, pick := range []func(context.Context, string) (string, bool, error){
		pickWithZenity,
		pickWithKDialog,
		pickWithOSAScript,
	} {
		path, ok, err := pick(ctx, start)
		if errors.Is(err, errNoNativePicker) {
			continue
		}
		if err != nil || !ok {
			return path, ok, err
		}
		return cleanPicked(path)
	}
	return "", false, errNoNativePicker
}

func cleanPicked(path string) (string, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", false, fmt.Errorf("selected path: %w", err)
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("selected path is not a directory")
	}
	return abs, true, nil
}

func pickWithZenity(ctx context.Context, start string) (string, bool, error) {
	bin, err := exec.LookPath("zenity")
	if err != nil {
		return "", false, errNoNativePicker
	}
	args := []string{"--file-selection", "--directory", "--title=Choose a project directory"}
	if start != "" {
		args = append(args, "--filename="+strings.TrimRight(filepath.Clean(start), string(os.PathSeparator))+string(os.PathSeparator))
	}
	return runPicker(ctx, bin, args)
}

func pickWithKDialog(ctx context.Context, start string) (string, bool, error) {
	bin, err := exec.LookPath("kdialog")
	if err != nil {
		return "", false, errNoNativePicker
	}
	if start == "" {
		start = os.Getenv("HOME")
	}
	return runPicker(ctx, bin, []string{"--getexistingdirectory", start})
}

func pickWithOSAScript(ctx context.Context, _ string) (string, bool, error) {
	bin, err := exec.LookPath("osascript")
	if err != nil {
		return "", false, errNoNativePicker
	}
	script := `POSIX path of (choose folder with prompt "Choose a project directory")`
	return runPicker(ctx, bin, []string{"-e", script})
}

func runPicker(ctx context.Context, bin string, args []string) (string, bool, error) {
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			// Closing the dialog is not an error.
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(string(out)), true, nil
}
