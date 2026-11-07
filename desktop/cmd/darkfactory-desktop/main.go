package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"time"

	"github.com/Wintoplay/darkfactory/desktop/internal/daemon"
	"github.com/Wintoplay/darkfactory/desktop/internal/target"
	"github.com/Wintoplay/darkfactory/desktop/internal/terminal"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func main() {
	rawURL := flag.String("url", "http://127.0.0.1:8787/", "loopback daemon or comparison fixture URL")
	debug := flag.Bool("debug", false, "open the UI with the client diagnostics panel (?debug=1)")
	autostart := flag.Bool("autostart", true, "start the local daemon when it is not already running")
	// The in-app VTE pane is the default desktop terminal: WebKitGTK + xterm.js
	// lagged while a model streamed (2026-11-02 post-mortem,
	// docs/reviews/terminal-lag-2026-11-02.md), the pane streams the same
	// daemon feed smooth. --web-terminal opts back into the old engine.
	oldInApp := flag.Bool("in-app-terminal", true, "embed the terminal pane in the window (default; kept for launcher compatibility)")
	webTerm := flag.Bool("web-terminal", false, "use the web terminal instead of the in-app VTE pane")
	flag.Parse()
	inApp := *oldInApp && !*webTerm
	u, err := target.Parse(*rawURL)
	if err != nil {
		log.Fatal(err)
	}
	if *autostart {
		// The daemon is the authority; the window is only a client. Starting it
		// here means clicking the app-menu entry works from a cold boot.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := daemon.EnsureReachable(ctx, u)
		cancel()
		if err != nil {
			log.Printf("darkfactory: %v; opening the window anyway", err)
		}
	}
	if *debug {
		u = target.Debug(u)
	}
	if inApp {
		u = target.Native(u)
	}
	app := application.New(application.Options{
		Name:        "DarkFactory desktop evaluation",
		Description: "Optional client of the independent DarkFactory daemon",
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		// GNOME groups a window with its pinned launcher by matching the
		// window's app id (Wayland) or WM_CLASS (X11) against the desktop
		// file. Without either option GTK derives both from the executable's
		// name, "darkfactory-desktop", which matches no desktop file, so a
		// pinned DarkFactory icon opened a second, ungrouped icon instead of
		// using the pin. The program name inherits this id, so both backends
		// report the same string, and packaging/darkfactory.desktop names it
		// in StartupWMClass because the file itself is still
		// darkfactory.desktop (that is the id users have pinned).
		Linux: application.LinuxOptions{ApplicationID: "dev.darkfactory.Desktop"},
	})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "DarkFactory desktop evaluation",
		// Above the 900px phone breakpoint: a 1200px window used to open the
		// daemon UI in its phone layout, hiding the sidebar and Chat action.
		Width: 1280, Height: 900,
		URL: u.String(),
	})
	// Validation applies to the initial URL. Keep every later navigation on
	// that same loopback origin and refuse new windows in the native WebView.
	origin := u.Scheme + "://" + u.Host
	guardInstalled := installNavigationGuard(window, origin)

	if inApp {
		installInAppTerminal(window, u.Host)
	}
	if !guardInstalled || inApp {
		// NativeWindow is not available until Wails has shown the GTK tree.
		window.OnWindowEvent(events.Common.WindowShow, func(_ *application.WindowEvent) {
			if !guardInstalled {
				guardInstalled = installNavigationGuard(window, origin)
			}
			if inApp {
				application.InvokeAsync(func() {
					if err := terminal.Prepare(window.NativeWindow()); err != nil {
						log.Printf("darkfactory: in-app terminal: %v", err)
						return
					}
					if err := terminal.AttachBridge(window.NativeWindow()); err != nil {
						log.Printf("darkfactory: in-app terminal bridge: %v", err)
					}
				})
			}
		})
	}
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// installInAppTerminal wires the page's terminal messages to a VTE overlay.
//
// The page mints a single-use pane capability for one session and posts it with
// the terminal rectangle through the WebView message channel. The shell runs the
// existing attach client on a local PTY; it never receives the master token.
// Geometry updates, visibility and close are separate messages so resizing or
// opening a dialog does not reattach. Messages arrive on the GTK main thread,
// which is where the widget calls must run.
func installInAppTerminal(window *application.WebviewWindow, daemonHost string) {
	attachBin, err := daemon.Binary()
	if err != nil {
		log.Printf("darkfactory: in-app terminal unavailable: %v", err)
		return
	}
	terminal.OnMessage(func(raw string) {
		var message struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			Pane      string `json:"pane"`
			X         int    `json:"x"`
			Y         int    `json:"y"`
			W         int    `json:"w"`
			H         int    `json:"h"`
		}
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			log.Printf("darkfactory: in-app terminal: bad message: %v", err)
			return
		}
		box := terminal.Bounds{X: message.X, Y: message.Y, W: message.W, H: message.H}
		switch message.Type {
		case "open":
			if message.SessionID == "" || message.Pane == "" {
				log.Printf("darkfactory: in-app terminal: incomplete open message")
				return
			}
			if err := terminal.Open(window.NativeWindow(), daemonHost, attachBin, message.SessionID, message.Pane, 100, 30, box); err != nil {
				log.Printf("darkfactory: in-app terminal: %v", err)
				window.ExecJS(`window.__dfTerminalFailed && window.__dfTerminalFailed("the native terminal could not start")`)
				return
			}
			window.ExecJS(`window.__dfTerminalReady && window.__dfTerminalReady()`)
		case "bounds":
			terminal.Place(box)
		case "hide":
			terminal.SetVisible(false)
		case "show":
			terminal.SetVisible(true)
		case "close":
			terminal.Close()
		}
	})
}
