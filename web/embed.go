package web

import (
	"embed"
	"regexp"
)

// Assets is the browser shell served by the Go binary. xterm.js and its fit
// addon are vendored under vendor/; see third_party/README.md.
//
//go:embed index.html app.js style.css vendor
var Assets embed.FS

// Version is the UI version embedded in this binary. The daemon reports it in
// the state response so a page running a shell cached before an update can
// reload itself instead of staying on the previous generation. Parsing app.js
// keeps the constant and the asset queries on one source of truth.
var Version = readVersion()

var uiVersionPattern = regexp.MustCompile(`const UI_VERSION = '([^']+)'`)

func readVersion() string {
	raw, err := Assets.ReadFile("app.js")
	if err != nil {
		return ""
	}
	match := uiVersionPattern.FindSubmatch(raw)
	if match == nil {
		return ""
	}
	return string(match[1])
}
