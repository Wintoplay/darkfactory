//go:build linux && cgo

package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const page = `<!doctype html><html><body style="margin:0;font:18px system-ui;background:#0d1116;color:#d6dee8">
<div style="padding:24px">
<h1 style="color:#7ee787">WebView pane</h1>
<p>This is the normal web UI side of a GtkPaned.</p>
<p>The right side is a native VTE session fed by the DarkFactory attach client.</p>
<p>If both are visible, type into the terminal, resize the window and check the menu.</p>
</div></body></html>`

// A throwaway spike, not part of the shipped app. S2 feeds the VTE pane from a
// disposable DarkFactory session through the existing native-attach client.
func main() {
	daemon := flag.String("daemon", "", "daemon host:port (spike only)")
	tokenFile := flag.String("token-file", "", "spike-only credential file mapped to fd 3")
	attachBin := flag.String("attach-bin", "", "darkfactory binary with native-attach")
	session := flag.String("session", "", "session id to attach")
	flag.Parse()
	if *daemon == "" || *tokenFile == "" || *attachBin == "" || *session == "" {
		log.Fatal("spike: --daemon, --token-file, --attach-bin and --session are required")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, page)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()

	app := application.New(application.Options{Name: "DarkFactory VTE spike"})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "DarkFactory in-app terminal spike",
		Width:  1280,
		Height: 800,
		URL:    "http://" + ln.Addr().String() + "/",
	})
	window.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) {
		application.InvokeAsync(func() {
			if err := attachPane(window.NativeWindow(), *daemon, *tokenFile, *attachBin, *session); err != nil {
				log.Printf("spike: %v", err)
				return
			}
			log.Print("spike: VTE pane attached")
		})
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
