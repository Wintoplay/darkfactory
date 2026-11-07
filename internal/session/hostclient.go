//go:build !windows

package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wintoplay/darkfactory/internal/model"
)

const hostSocketName = "session-host.sock"

func HostSocketPath(stateDir string) string {
	return filepath.Join(stateDir, hostSocketName)
}

type HostClient struct {
	socket string
	http   *http.Client
}

func NewHostClient(socket string) *HostClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	}
	return &HostClient{socket: socket, http: &http.Client{Transport: transport}}
}

// EnsureHost reuses the long-lived session host or starts one. The host is
// started with Setsid so a daemon restart does not take the PTYs down with it.
func EnsureHost(stateDir string, ringBytes int, log *slog.Logger) (*HostClient, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	socket := HostSocketPath(stateDir)
	client := NewHostClient(socket)
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	err := client.Health(checkCtx)
	checkCancel()
	if err == nil {
		return client, nil
	}

	_ = os.Remove(socket)
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate binary: %w", err)
	}
	logPath := filepath.Join(stateDir, "session-host.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open session host log: %w", err)
	}
	cmd := exec.Command(exe, "session-host", "--socket", socket, "--ring-bytes", strconv.Itoa(ringBytes))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start session host: %w", err)
	}
	_ = logFile.Close()
	go func() { _ = cmd.Wait() }()
	log.Info("started session host", "socket", socket, "log", logPath)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		err := client.Health(ctx)
		cancel()
		if err == nil {
			return client, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("session host did not start; see %s", logPath)
}

func (c *HostClient) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

func hostError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	text := strings.TrimSpace(string(raw))
	if text == "" {
		text = resp.Status
	}
	return fmt.Errorf("session host: %s", text)
}

func (c *HostClient) Health(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/v1/health", nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return hostError(resp)
	}
	_ = resp.Body.Close()
	return nil
}

func (c *HostClient) List(ctx context.Context) ([]model.Session, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/sessions", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, hostError(resp)
	}
	defer resp.Body.Close()
	var items []model.Session
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *HostClient) Start(ctx context.Context, spec Spec) (model.Session, error) {
	body, err := json.Marshal(spec)
	if err != nil {
		return model.Session{}, err
	}
	resp, err := c.do(ctx, http.MethodPost, "/v1/sessions", body)
	if err != nil {
		return model.Session{}, err
	}
	if resp.StatusCode != http.StatusCreated {
		return model.Session{}, hostError(resp)
	}
	defer resp.Body.Close()
	var item model.Session
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return model.Session{}, err
	}
	return item, nil
}

// Attach consumes the initial snapshot frame and leaves the body positioned at
// the first live data frame. The caller owns the returned body.
func (c *HostClient) Attach(ctx context.Context, id string) ([]byte, io.ReadCloser, error) {
	// The stream outlives the short-lived context used to start the session.
	// WithoutCancel keeps the body alive until remoteLink closes it.
	streamCtx := context.WithoutCancel(ctx)
	resp, err := c.do(streamCtx, http.MethodGet, "/v1/sessions/"+url.PathEscape(id)+"/stream", nil)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, hostError(resp)
	}
	kind, payload, err := readFrame(resp.Body)
	if err != nil {
		_ = resp.Body.Close()
		return nil, nil, err
	}
	if kind != frameSnapshot {
		_ = resp.Body.Close()
		return nil, nil, fmt.Errorf("session host: first frame is %q, want snapshot", kind)
	}
	return payload, resp.Body, nil
}

func (c *HostClient) Input(ctx context.Context, id string, data []byte) error {
	resp, err := c.do(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(id)+"/input", data)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent {
		return hostError(resp)
	}
	_ = resp.Body.Close()
	return nil
}

func (c *HostClient) Resize(ctx context.Context, id string, cols, rows uint16) error {
	body, err := json.Marshal(map[string]uint16{"cols": cols, "rows": rows})
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(id)+"/resize", body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent {
		return hostError(resp)
	}
	_ = resp.Body.Close()
	return nil
}

func (c *HostClient) Stop(ctx context.Context, id, reason string) (StopResult, error) {
	path := "/v1/sessions/" + url.PathEscape(id)
	if reason != "" {
		path += "?reason=" + url.QueryEscape(reason)
	}
	resp, err := c.do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return StopUnverifiable, err
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return StopUnverifiable, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return StopUnverifiable, hostError(resp)
	}
	defer resp.Body.Close()
	var body struct {
		Verdict StopResult `json:"verdict"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return StopUnverifiable, err
	}
	if body.Verdict == "" {
		return StopUnverifiable, errors.New("session host: empty verdict")
	}
	return body.Verdict, nil
}

func (c *HostClient) Shutdown(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodPost, "/v1/shutdown", nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent {
		return hostError(resp)
	}
	_ = resp.Body.Close()
	return nil
}
