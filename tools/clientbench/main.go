// clientbench serves a synthetic terminal workload, never a user's daemon.
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed fixture.html fixture.js compare.js
var fixture embed.FS

func main() {
	assets := flag.String("assets", "web/vendor", "vendored xterm asset directory")
	ghostty := flag.String("ghostty", "", "directory with ghostty-web.js and ghostty-vt.wasm")
	flag.Parse()
	for _, name := range []string{"xterm.js", "xterm.css"} {
		if _, err := os.Stat(filepath.Join(*assets, name)); err != nil {
			log.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	var mu sync.Mutex
	var result json.RawMessage
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// /webgl is the query-free form the desktop shell can load: its URL
		// guard refuses query strings, and the fixture wants no credentials.
		if r.URL.Path != "/" && r.URL.Path != "/webgl" && r.URL.Path != "/webgl/" && r.URL.Path != "/compare" && r.URL.Path != "/compare/" && r.URL.Path != "/compare.js" && r.URL.Path != "/ghostty" && r.URL.Path != "/ghostty/" && r.URL.Path != "/fixture.js" {
			http.NotFound(w, r)
			return
		}
		name := "fixture.html"
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/fixture.js" || r.URL.Path == "/compare.js" {
			name = strings.TrimPrefix(r.URL.Path, "/")
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}
		data, _ := fixture.ReadFile(name)
		if r.URL.Path == "/compare" || r.URL.Path == "/compare/" || r.URL.Path == "/ghostty" || r.URL.Path == "/ghostty/" {
			data = []byte(strings.Replace(string(data), "/fixture.js", "/compare.js", 1))
		}
		_, _ = w.Write(data)
	})
	for _, name := range []string{"ghostty-web.js", "ghostty-vt.wasm", "__vite-browser-external-2447137e.js"} {
		mux.HandleFunc("GET /ghostty-assets/"+name, func(w http.ResponseWriter, r *http.Request) {
			if *ghostty == "" {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, filepath.Join(*ghostty, name))
		})
	}
	for _, name := range []string{"xterm.js", "xterm.css", "addon-webgl.js"} {
		mux.HandleFunc("GET /vendor/"+name, func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join(*assets, name))
		})
	}
	mux.HandleFunc("POST /result", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil || !json.Valid(data) {
			http.Error(w, "invalid result", http.StatusBadRequest)
			return
		}
		mu.Lock()
		result = append(json.RawMessage(nil), data...)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /result", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		if result == nil {
			_, _ = io.WriteString(w, "null")
			return
		}
		_, _ = w.Write(result)
	})
	fmt.Printf("http://%s/\n", listener.Addr())
	log.Fatal(http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != listener.Addr().String() || (r.Method == http.MethodPost && r.Header.Get("Origin") != "http://"+listener.Addr().String()) {
			http.Error(w, "wrong origin", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})))
}
