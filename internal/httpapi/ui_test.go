package httpapi

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/Wintoplay/darkfactory/web"
)

var (
	uiVersionPattern  = regexp.MustCompile(`const UI_VERSION = '([^']+)'`)
	assetQueryPattern = regexp.MustCompile(`<(?:script|link)[^>]*(?:src|href)="([^"]+)"`)
	htmlIDPattern     = regexp.MustCompile(`id="([^"]+)"`)
	uiRefPattern      = regexp.MustCompile(`\$\('([^']+)'\)`)
	uiGetByIDPattern  = regexp.MustCompile(`getElementById\('([^']+)'\)`)
)

func readUI(t *testing.T, name string) string {
	t.Helper()
	raw, err := web.Assets.ReadFile(name)
	if err != nil {
		t.Fatalf("read web/%s: %v", name, err)
	}
	return string(raw)
}

// A manual version string is only useful if the cache-busting query moves with
// it. They drifted before: four app.js builds all served as 2026-11-01.1.
func TestUIVersionMatchesAssetQueries(t *testing.T) {
	js := readUI(t, "app.js")
	match := uiVersionPattern.FindStringSubmatch(js)
	if match == nil {
		t.Fatal("app.js has no `const UI_VERSION = '...'`")
	}
	want := match[1]
	if web.Version != want {
		t.Errorf("web.Version = %q, want %q (the daemon and the shell must agree)", web.Version, want)
	}

	html := readUI(t, "index.html")
	assets := assetQueryPattern.FindAllStringSubmatch(html, -1)
	if len(assets) == 0 {
		t.Fatal("index.html references no local scripts or styles")
	}
	for _, asset := range assets {
		ref := asset[1]
		if strings.HasPrefix(ref, "/vendor/") && !strings.Contains(ref, "?v=") {
			continue // vendored xterm assets are not part of the client version
		}
		if !strings.Contains(ref, "?v="+want) {
			t.Errorf("index.html references %q, want ?v=%s", ref, want)
		}
	}
}

// Every element app.js looks up must exist, either in the static shell or in a
// template string app.js writes. A typo here fails at runtime as a null
// dereference, which a syntax check cannot see.
func TestUIElementIDsResolve(t *testing.T) {
	js := readUI(t, "app.js")
	defined := map[string]bool{}
	for _, m := range htmlIDPattern.FindAllStringSubmatch(readUI(t, "index.html"), -1) {
		defined[m[1]] = true
	}
	for _, m := range htmlIDPattern.FindAllStringSubmatch(js, -1) {
		defined[m[1]] = true
	}

	refs := map[string]bool{}
	for _, re := range []*regexp.Regexp{uiRefPattern, uiGetByIDPattern} {
		for _, m := range re.FindAllStringSubmatch(js, -1) {
			refs[m[1]] = true
		}
	}
	if len(refs) == 0 {
		t.Fatal("no element lookups found in app.js")
	}
	for id := range refs {
		if !defined[id] {
			t.Errorf("app.js reads #%s but no element defines id=%q", id, id)
		}
	}
}

// The shipped UI has no build step and no CDN. A new remote asset or CSS
// @import would silently break the CSP and the offline/no-telemetry promise.
func TestUIAssetsStayLocal(t *testing.T) {
	for _, name := range []string{"index.html", "style.css", "app.js"} {
		source := readUI(t, name)
		// The SVG namespace is an identifier passed to createElementNS, not a
		// fetched asset. Everything else stays local.
		source = strings.ReplaceAll(source, "http://www.w3.org/2000/svg", "")
		if strings.Contains(source, "http://") || strings.Contains(source, "https://") {
			t.Errorf("web/%s contains an absolute URL; the UI must stay self-contained", name)
		}
	}
	for _, m := range assetQueryPattern.FindAllStringSubmatch(readUI(t, "index.html"), -1) {
		ref := m[1]
		if !strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "//") {
			t.Errorf("index.html loads %q, want a root-relative local path", ref)
		}
	}
}

// A stale binary is the failure this field exists to expose. Keep the JSON name
// stable so the browser and `darkfactory status` can read it.
func TestStateResponseCarriesBuild(t *testing.T) {
	old := Build
	Build = "0.1.0-dev (test)"
	defer func() { Build = old }()

	body, err := json.Marshal(stateResponse{Build: Build, UI: web.Version})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"build":"0.1.0-dev (test)"`) {
		t.Fatalf("state response lost the build identity: %s", body)
	}
	// The client reloads itself when this differs from its own UI_VERSION, so
	// an empty value would silently disable stale-shell recovery.
	if web.Version == "" {
		t.Fatal("web.Version is empty; the daemon cannot report its UI")
	}
	if !strings.Contains(string(body), `"ui":"`+web.Version+`"`) {
		t.Fatalf("state response lost the UI version: %s", body)
	}
}
