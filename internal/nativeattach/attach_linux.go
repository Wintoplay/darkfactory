//go:build linux

package nativeattach

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

var sessionID = regexp.MustCompile(`^[a-f0-9]{32}$`)

// Run attaches the terminal to the daemon using a caller-scoped credential.
// The calling process supplies the credential through an anonymous fd, never
// through an argument, environment variable, URL, or persistent file.
func Run(listen, id string, credentialFD, readyFD int) error {
	var ready *os.File
	if readyFD >= 3 && readyFD != credentialFD {
		ready = os.NewFile(uintptr(readyFD), "ready")
		if ready == nil {
			return errors.New("readiness descriptor unavailable")
		}
		defer ready.Close()
	}
	if !sessionID.MatchString(id) {
		return errors.New("invalid session id")
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" || (!strings.EqualFold(host, "localhost") && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback())) {
		return errors.New("native attach requires a loopback daemon address")
	}
	if credentialFD < 3 {
		return errors.New("native attach requires a credential descriptor")
	}
	credential := os.NewFile(uintptr(credentialFD), "credential")
	if credential == nil {
		return errors.New("credential descriptor unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(credential, 4097))
	_ = credential.Close()
	if err != nil || len(raw) == 0 || len(raw) > 4096 {
		return errors.New("credential unavailable")
	}
	token := strings.TrimSpace(string(raw))
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return errors.New("invalid credential")
	}

	fd := int(os.Stdin.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return errors.New("native attach requires a terminal")
	}
	rawTerm := *old
	rawTerm.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	rawTerm.Oflag &^= unix.OPOST
	rawTerm.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	rawTerm.Cflag &^= unix.CSIZE | unix.PARENB
	rawTerm.Cflag |= unix.CS8
	rawTerm.Cc[unix.VMIN], rawTerm.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &rawTerm); err != nil {
		return errors.New("could not configure terminal")
	}
	defer unix.IoctlSetTermios(fd, unix.TCSETS, old)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	base := "http://" + net.JoinHostPort(host, port) + "/api/v1/sessions/" + id
	streamReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/stream", nil)
	streamReq.Header.Set("Authorization", "Bearer "+token)
	stream, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		return errors.New("could not open terminal stream")
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		return fmt.Errorf("terminal stream refused (HTTP %d)", stream.StatusCode)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	post := func(path, contentType string, body []byte) error {
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-DF-Request", "1")
		req.Header.Set("Content-Type", contentType)
		resp, e := client.Do(req)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode != http.StatusNoContent {
			return fmt.Errorf("%s refused (HTTP %d)", path, resp.StatusCode)
		}
		return nil
	}
	resize := func() error {
		size, e := pty.GetsizeFull(os.Stdin)
		if e != nil || size.Cols == 0 || size.Rows == 0 {
			return errors.New("could not determine terminal size")
		}
		data, _ := json.Marshal(struct {
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}{size.Cols, size.Rows})
		return post("/resize", "application/json", data)
	}
	// Register SIGWINCH before the first resize. VTE can reflow and signal a
	// new size between that request and a later Notify call; the old order lost
	// exactly that signal, leaving the daemon PTY at the pre-layout grid while
	// the widget painted the new one.
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	if err := resize(); err != nil {
		return err
	}
	if stream.Header.Get("X-DF-Redraw") == "1" {
		if err := post("/redraw", "text/plain", nil); err != nil {
			return err
		}
	}
	if ready != nil {
		if _, err := ready.Write([]byte{1}); err != nil {
			return errors.New("could not report native readiness")
		}
		_ = ready.Close()
	}
	// A resize may have landed while the first request was in flight. Re-read
	// the PTY size once after the handler is live, so the PTY and the widget
	// agree before the first frame.
	if err := resize(); err != nil {
		return err
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-winch:
				_ = resize()
			}
		}
	}()
	inputErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 16*1024)
		for {
			n, e := os.Stdin.Read(buf)
			if n > 0 {
				if e2 := post("/input", "text/plain", buf[:n]); e2 != nil {
					inputErr <- e2
					cancel()
					return
				}
			}
			if e != nil {
				inputErr <- e
				cancel()
				return
			}
		}
	}()
	_, err = io.Copy(NewFilter(os.Stdout), stream.Body)
	select {
	case e := <-inputErr:
		if !errors.Is(e, io.EOF) {
			return e
		}
	default:
	}
	if err != nil && ctx.Err() == nil {
		return errors.New("terminal stream interrupted")
	}
	return nil
}
