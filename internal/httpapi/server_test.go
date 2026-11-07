package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/config"
	"github.com/Wintoplay/darkfactory/internal/model"
	"github.com/Wintoplay/darkfactory/internal/session"
	"github.com/Wintoplay/darkfactory/internal/store"
)

func TestHealthAndAuth(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); csp == "" {
		t.Fatal("missing Content-Security-Policy")
	}
	if cache := resp.Header.Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cache)
	}

	resp, err = http.Get(srv.URL + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("state status = %d", resp.StatusCode)
	}
	var state struct {
		Repos     []model.Repo      `json:"repos"`
		Worktrees []model.Worktree  `json:"worktrees"`
		Sessions  []json.RawMessage `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Repos == nil || state.Worktrees == nil || state.Sessions == nil {
		t.Fatalf("empty state must encode as [], got %#v", state)
	}
}

func TestIndexCSPNonce(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("index status = %d", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	match := regexp.MustCompile(`style-src 'self' 'nonce-([0-9a-f]+)'`).FindStringSubmatch(csp)
	if match == nil {
		t.Fatalf("CSP has no style nonce: %q", csp)
	}
	if !strings.Contains(csp, "style-src-attr 'unsafe-inline'") {
		t.Fatalf("CSP must allow xterm's style attributes: %q", csp)
	}
	if strings.Contains(csp, "style-src 'self' 'nonce-"+match[1]+"' 'unsafe-inline'") {
		t.Fatalf("CSP must not allow unrestricted inline styles: %q", csp)
	}
	meta := `name="csp-nonce" content="` + match[1] + `"`
	if !strings.Contains(string(body), meta) {
		t.Fatalf("index body is missing %s", meta)
	}
	if strings.Contains(string(body), "{{CSP_NONCE}}") {
		t.Fatal("index body still contains the nonce placeholder")
	}
}

func TestClientDebug(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	var logs bytes.Buffer
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(&logs, nil))).Handler())
	defer srv.Close()

	body := strings.NewReader("click #btn-browse\nbrowse open\n")
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/debug/client", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("client debug status = %d, want 204", resp.StatusCode)
	}
	if !strings.Contains(logs.String(), "click #btn-browse") || !strings.Contains(logs.String(), "browse open") {
		t.Fatalf("client debug lines missing from log: %q", logs.String())
	}

	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/debug/client", strings.NewReader("nope"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated client debug status = %d, want 401", resp.StatusCode)
	}
}

func TestListDirs(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	root := t.TempDir()
	for _, dir := range []string{"alpha", "beta", ".hidden", filepath.Join("repo", ".git")} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/fs/dirs?path="+url.QueryEscape(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("list dirs status = %d: %s", resp.StatusCode, raw)
	}
	var body dirsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Path != root || body.Parent != filepath.Dir(root) || body.Git {
		t.Fatalf("unexpected dirs header: %#v", body)
	}
	if body.Truncated {
		t.Fatal("unexpected truncation")
	}
	if len(body.Entries) != 4 {
		t.Fatalf("entries = %#v, want 4 directories", body.Entries)
	}
	if body.Entries[0].Name != "repo" || !body.Entries[0].Git {
		t.Fatalf("git directories must sort first: %#v", body.Entries)
	}
	for _, entry := range body.Entries {
		if entry.Name == "note.txt" {
			t.Fatal("files must not be listed")
		}
	}

	unauth, err := http.Get(srv.URL + "/api/v1/fs/dirs?path=" + url.QueryEscape(root))
	if err != nil {
		t.Fatal(err)
	}
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized list dirs status = %d", unauth.StatusCode)
	}

	fileReq, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/fs/dirs?path="+url.QueryEscape(filepath.Join(root, "note.txt")), nil)
	if err != nil {
		t.Fatal(err)
	}
	fileReq.Header.Set("Authorization", "Bearer test-token")
	fileResp, err := http.DefaultClient.Do(fileReq)
	if err != nil {
		t.Fatal(err)
	}
	fileResp.Body.Close()
	if fileResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("file path status = %d, want 400", fileResp.StatusCode)
	}
}

func TestCreateRepo(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	repoPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(repoPath, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"path": repoPath, "name": "demo"})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/repos", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create repo status = %d: %s", resp.StatusCode, raw)
	}
	repos, err := st.ListRepos()
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Name != "demo" {
		t.Fatalf("repos = %#v", repos)
	}
}

func TestCreateRepoWorkspace(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	repoPath := t.TempDir()
	body, _ := json.Marshal(map[string]string{"path": repoPath, "name": "docs"})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/repos", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var repo model.Repo
	if err := json.NewDecoder(resp.Body).Decode(&repo); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create repo status = %d", resp.StatusCode)
	}
	if repo.Git {
		t.Fatal("non-git repo reported Git=true")
	}
	worktrees, err := st.ListWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 1 || !worktrees[0].Workspace || worktrees[0].Path != repoPath {
		t.Fatalf("worktrees = %#v, want one in-place workspace", worktrees)
	}

	body, _ = json.Marshal(map[string]string{"repoId": repo.ID, "name": "second"})
	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/worktrees", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("second worktree status = %d: %s", resp.StatusCode, raw)
	}
}

func TestCreateRepoRejectsDuplicatePath(t *testing.T) {
	srv, st, cfg := newAPITestServer(t)
	repoPath := t.TempDir()

	resp := postJSON(t, srv.URL+"/api/v1/repos", cfg.Token, map[string]string{
		"path": repoPath, "name": "demo",
	})
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("first repo status = %d: %s", resp.StatusCode, raw)
	}
	resp.Body.Close()

	resp = postJSON(t, srv.URL+"/api/v1/repos", cfg.Token, map[string]string{
		"path": repoPath, "name": "demo-again",
	})
	if resp.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("duplicate repo status = %d: %s", resp.StatusCode, raw)
	}
	resp.Body.Close()

	link := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(repoPath, link); err != nil {
		t.Fatal(err)
	}
	resp = postJSON(t, srv.URL+"/api/v1/repos", cfg.Token, map[string]string{
		"path": link, "name": "demo-link",
	})
	if resp.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("symlink duplicate status = %d: %s", resp.StatusCode, raw)
	}
	resp.Body.Close()

	repos, err := st.ListRepos()
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 {
		t.Fatalf("repos = %#v, want one row", repos)
	}
}

func TestCreateRepoRejectsTraversalName(t *testing.T) {
	srv, _, cfg := newAPITestServer(t)
	repoPath := t.TempDir()
	resp := postJSON(t, srv.URL+"/api/v1/repos", cfg.Token, map[string]string{
		"path": repoPath, "name": "..",
	})
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("traversal name status = %d: %s", resp.StatusCode, raw)
	}
	resp.Body.Close()
}

func TestCreateRepoCreatesGitWorkspace(t *testing.T) {
	srv, st, cfg := newAPITestServer(t)
	repoPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(repoPath, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}

	resp := postJSON(t, srv.URL+"/api/v1/repos", cfg.Token, map[string]string{
		"path": repoPath, "name": "demo",
	})
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create repo status = %d: %s", resp.StatusCode, raw)
	}
	var repo model.Repo
	if err := json.NewDecoder(resp.Body).Decode(&repo); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !repo.Git {
		t.Fatal("repo with .git reported Git=false")
	}
	worktrees, err := st.ListWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 1 {
		t.Fatalf("worktrees = %#v, want one checkout workspace", worktrees)
	}
	wt := worktrees[0]
	if !wt.Workspace || wt.Path != repoPath || wt.Branch != "" {
		t.Fatalf("workspace = %#v, want in-place row at %s", wt, repoPath)
	}

	resp = postJSON(t, srv.URL+"/api/v1/worktrees", cfg.Token, map[string]any{
		"repoId": repo.ID, "workspace": true,
	})
	if resp.StatusCode != http.StatusConflict {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("second workspace status = %d: %s", resp.StatusCode, raw)
	}
	resp.Body.Close()
}

func TestNewCreatesMissingWorkspace(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	repoPath := t.TempDir()
	repo, err := st.PutRepo(model.Repo{Name: "legacy", Path: repoPath, Git: true})
	if err != nil {
		t.Fatal(err)
	}
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	_ = New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))

	worktrees, err := st.ListWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 1 || !worktrees[0].Workspace || worktrees[0].RepoID != repo.ID || worktrees[0].Path != repoPath {
		t.Fatalf("worktrees = %#v, want one workspace for %s", worktrees, repo.ID)
	}
}

func TestCreateWorkspaceForLegacyRepo(t *testing.T) {
	srv, st, cfg := newAPITestServer(t)
	repoPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(repoPath, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	repo, err := st.PutRepo(model.Repo{Name: "legacy", Path: repoPath, Git: true})
	if err != nil {
		t.Fatal(err)
	}

	resp := postJSON(t, srv.URL+"/api/v1/worktrees", cfg.Token, map[string]any{
		"repoId": repo.ID, "workspace": true,
	})
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("workspace status = %d: %s", resp.StatusCode, raw)
	}
	var wt model.Worktree
	if err := json.NewDecoder(resp.Body).Decode(&wt); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !wt.Workspace || wt.Path != repoPath || wt.Branch != "" {
		t.Fatalf("workspace = %#v, want in-place row at %s", wt, repoPath)
	}
}

func TestCreateWorktreePathsDoNotCollide(t *testing.T) {
	srv, _, cfg := newAPITestServer(t)
	repoA := initGitRepo(t)
	repoB := initGitRepo(t)

	repos := make([]model.Repo, 0, 2)
	for _, repoPath := range []string{repoA, repoB} {
		resp := postJSON(t, srv.URL+"/api/v1/repos", cfg.Token, map[string]string{
			"path": repoPath, "name": "demo",
		})
		if resp.StatusCode != http.StatusCreated {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("create repo status = %d: %s", resp.StatusCode, raw)
		}
		var repo model.Repo
		if err := json.NewDecoder(resp.Body).Decode(&repo); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		repos = append(repos, repo)
	}

	paths := make([]string, 0, 2)
	for _, repo := range repos {
		resp := postJSON(t, srv.URL+"/api/v1/worktrees", cfg.Token, map[string]string{
			"repoId": repo.ID, "name": "agent2", "baseRef": "HEAD",
		})
		if resp.StatusCode != http.StatusCreated {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("create worktree status = %d: %s", resp.StatusCode, raw)
		}
		var wt model.Worktree
		if err := json.NewDecoder(resp.Body).Decode(&wt); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if !strings.HasPrefix(wt.Path, cfg.WorktreeRoot+string(os.PathSeparator)) {
			t.Fatalf("worktree path = %q, want under %q", wt.Path, cfg.WorktreeRoot)
		}
		if !strings.HasPrefix(filepath.Base(wt.Path), "agent2-") || !strings.HasSuffix(wt.Path, wt.ID[:8]) {
			t.Fatalf("worktree path = %q, want agent2-<short id>", wt.Path)
		}
		paths = append(paths, wt.Path)
	}
	if paths[0] == paths[1] {
		t.Fatalf("worktree paths collided: %q", paths[0])
	}
}

func TestDeleteWorktreeRefusesDirtyWorktree(t *testing.T) {
	srv, st, cfg := newAPITestServer(t)
	repoPath := initGitRepo(t)

	resp := postJSON(t, srv.URL+"/api/v1/repos", cfg.Token, map[string]string{
		"path": repoPath, "name": "demo",
	})
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create repo status = %d: %s", resp.StatusCode, raw)
	}
	var repo model.Repo
	if err := json.NewDecoder(resp.Body).Decode(&repo); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	resp = postJSON(t, srv.URL+"/api/v1/worktrees", cfg.Token, map[string]string{
		"repoId": repo.ID, "name": "agent", "baseRef": "HEAD",
	})
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create worktree status = %d: %s", resp.StatusCode, raw)
	}
	var wt model.Worktree
	if err := json.NewDecoder(resp.Body).Decode(&wt); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if want := worktreeBranchPrefix + "agent"; wt.Branch != want {
		t.Fatalf("branch = %q, want %q", wt.Branch, want)
	}

	// The agent's output is normally untracked. Deleting the worktree used to
	// force-remove exactly this, without asking.
	if err := os.WriteFile(filepath.Join(wt.Path, "agent-output.txt"), []byte("work\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	deleteWorktree := func(query string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/worktrees/"+wt.ID+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
		got, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// A live PTY is refused first: it names the session it would have stopped.
	sessResp := postJSON(t, srv.URL+"/api/v1/sessions", cfg.Token, map[string]any{
		"worktreeId": wt.ID, "agent": "shell", "kind": "shell",
	})
	if sessResp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(sessResp.Body)
		t.Fatalf("create session status = %d: %s", sessResp.StatusCode, raw)
	}
	var sess model.Session
	if err := json.NewDecoder(sessResp.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	sessResp.Body.Close()

	busy := deleteWorktree("")
	raw, _ := io.ReadAll(busy.Body)
	busy.Body.Close()
	if busy.StatusCode != http.StatusConflict {
		t.Fatalf("busy delete status = %d, want 409: %s", busy.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "live sessions") || !strings.Contains(string(raw), sess.ID[:8]) {
		t.Fatalf("refusal does not name the live session: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "agent-output.txt")); err != nil {
		t.Fatalf("a refused delete touched the worktree: %v", err)
	}

	stopResp, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/sessions/"+sess.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	stopResp.Header.Set("Authorization", "Bearer "+cfg.Token)
	stopped, err := http.DefaultClient.Do(stopResp)
	if err != nil {
		t.Fatal(err)
	}
	stopped.Body.Close()

	refused := deleteWorktree("")
	raw, _ = io.ReadAll(refused.Body)
	refused.Body.Close()
	if refused.StatusCode != http.StatusConflict {
		t.Fatalf("dirty delete status = %d, want 409: %s", refused.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "force=1") || !strings.Contains(string(raw), "agent-output.txt") {
		t.Fatalf("refusal does not name the files or the escape hatch: %s", raw)
	}
	if _, err := st.GetWorktree(wt.ID); err != nil {
		t.Fatalf("a refused delete dropped the worktree row: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "agent-output.txt")); err != nil {
		t.Fatalf("a refused delete lost the file: %v", err)
	}

	forced := deleteWorktree("?force=1")
	forced.Body.Close()
	if forced.StatusCode != http.StatusNoContent {
		t.Fatalf("forced delete status = %d, want 204", forced.StatusCode)
	}
	if _, err := st.GetWorktree(wt.ID); err == nil {
		t.Fatal("forced delete left the worktree row")
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("forced delete left the directory: %v", err)
	}
}

func TestCreateSessionIsIdempotent(t *testing.T) {
	srv, st, cfg := newAPITestServer(t)
	repoPath := t.TempDir()

	resp := postJSON(t, srv.URL+"/api/v1/repos", cfg.Token, map[string]string{
		"path": repoPath, "name": "demo",
	})
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create repo status = %d: %s", resp.StatusCode, raw)
	}
	resp.Body.Close()
	worktrees, err := st.ListWorktrees()
	if err != nil || len(worktrees) != 1 {
		t.Fatalf("worktrees = %#v, %v", worktrees, err)
	}
	wt := worktrees[0]

	create := func(key string) (*http.Response, model.Session) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{
			"worktreeId": wt.ID, "agent": "shell", "kind": "shell",
		})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		got, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer got.Body.Close()
		var view struct {
			model.Session
			Live bool `json:"live"`
		}
		if err := json.NewDecoder(got.Body).Decode(&view); err != nil {
			t.Fatal(err)
		}
		return got, view.Session
	}

	first, firstSess := create("launch-1")
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", first.StatusCode)
	}
	replay, replaySess := create("launch-1")
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("retried create status = %d, want 200", replay.StatusCode)
	}
	if replaySess.ID != firstSess.ID {
		t.Fatalf("retry started a twin session: %s then %s", firstSess.ID, replaySess.ID)
	}
	if replaySess.ClientRequestID != "launch-1" {
		t.Fatalf("replayed response lost the request id: %q", replaySess.ClientRequestID)
	}

	// A different intent is a different session.
	second, secondSess := create("launch-2")
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("second intent status = %d, want 201", second.StatusCode)
	}
	if secondSess.ID == firstSess.ID {
		t.Fatal("a new key returned the previous session")
	}

	sessions, err := st.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("stored sessions = %d, want 2 (the retry must not add a row)", len(sessions))
	}
}

func TestDeleteRepoRemovesWorkspace(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	repoPath := t.TempDir()
	body, _ := json.Marshal(map[string]string{"path": repoPath, "name": "docs"})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/repos", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var repo model.Repo
	if err := json.NewDecoder(resp.Body).Decode(&repo); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create repo status = %d", resp.StatusCode)
	}
	worktrees, err := st.ListWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 1 {
		t.Fatalf("worktrees = %#v", worktrees)
	}

	req, err = http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/repos/"+repo.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete repo status = %d", resp.StatusCode)
	}
	repos, err := st.ListRepos()
	if err != nil {
		t.Fatal(err)
	}
	worktrees, err = st.ListWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 0 || len(worktrees) != 0 {
		t.Fatalf("repos = %#v, worktrees = %#v", repos, worktrees)
	}
	if _, err := os.Stat(repoPath); err != nil {
		t.Fatalf("repo directory was removed: %v", err)
	}
}

func TestStateRedactsAgentEnv(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	cfg.Agents["secretive"] = config.AgentPreset{
		Command: "agent",
		Env:     map[string]string{"API_KEY": "sk-live-do-not-leak"},
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("sk-live-do-not-leak")) {
		t.Fatal("state leaked an agent env value")
	}
	if !bytes.Contains(raw, []byte("API_KEY")) {
		t.Fatal("state should expose the env key name")
	}
}

func TestJailAllowListsExemptAndCombine(t *testing.T) {
	t.Setenv("HOME", "/tmp/df-test-home")
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.ConfigDir = "/tmp/df-test-config"
	cfg.StateDir = "/tmp/df-test-state"
	cfg.JailHideCredentials = true
	cfg.JailHiddenPaths = []string{"/tmp/df-test-extra"}
	cfg.JailPathAllow = []string{"/tmp/df-test-extra", "/tmp/df-test-home/.config/opencode"}
	cfg.JailEnvAllow = []string{"SSH_AUTH_SOCK"}
	cfg.Agents["codex"] = config.AgentPreset{
		Command:       "codex",
		JailEnvAllow:  []string{"GH_TOKEN"},
		JailPathAllow: []string{"/tmp/df-test-codex"},
	}
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	srv := New(cfg, nil, mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))

	paths, err := srv.hiddenPathsFor("jail", "codex")
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(paths))
	for _, path := range paths {
		got[path] = true
	}
	for _, path := range []string{"/tmp/df-test-config", "/tmp/df-test-state", "/tmp/df-test-home/.ssh", "/tmp/df-test-home/.aws"} {
		if !got[path] {
			t.Fatalf("hiddenPathsFor missing %s: %#v", path, paths)
		}
	}
	for _, path := range []string{"/tmp/df-test-extra", "/tmp/df-test-home/.config/opencode"} {
		if got[path] {
			t.Fatalf("hiddenPathsFor should exempt %s: %#v", path, paths)
		}
	}

	names := srv.jailEnvAllow("codex")
	want := map[string]bool{"SSH_AUTH_SOCK": true, "GH_TOKEN": true}
	for _, name := range names {
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("jailEnvAllow missing %v: %#v", want, names)
	}

	srv.cfg.JailHiddenPaths = []string{"/tmp/df-test-home"}
	srv.cfg.JailPathAllow = []string{"/tmp/df-test-home/.config/opencode"}
	if _, err := srv.hiddenPathsFor("jail", "codex"); err == nil {
		t.Fatal("an allow inside a masked parent must be refused")
	}
}

func TestDeleteRepoWithWorktreeRefused(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	repo, err := st.PutRepo(model.Repo{Name: "demo", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutWorktree(model.Worktree{RepoID: repo.ID, Path: filepath.Join(t.TempDir(), "wt")}); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/repos/"+repo.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete repo status = %d, want 409", resp.StatusCode)
	}
}

func TestGlobalOffResetsLegacyJailRows(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	if cfg.Containment != "off" {
		t.Fatalf("default containment = %q", cfg.Containment)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	repo, err := st.PutRepo(model.Repo{Name: "demo", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := st.PutWorktree(model.Worktree{RepoID: repo.ID, Path: t.TempDir(), Containment: "jail"})
	if err != nil {
		t.Fatal(err)
	}
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	_ = New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))
	got, err := st.GetWorktree(wt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Containment != "off" {
		t.Fatalf("legacy worktree containment = %q, want off", got.Containment)
	}
}

func TestStateAndDeleteLiveSessionIdentity(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()

	sess, err := mgr.Start(session.Spec{
		WorktreeID: "wt",
		Dir:        t.TempDir(),
		Kind:       "shell",
		Command:    "/bin/sh",
		Args:       []string{"-c", "sleep 30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	started := sess.Model()
	if _, err := st.PutSession(started); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var state stateResponse
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(state.Sessions) != 1 || state.Sessions[0].PID != started.PID || state.Sessions[0].StartTicks == 0 {
		t.Fatalf("state sessions = %#v", state.Sessions)
	}

	req, err = http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/sessions/"+started.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete session status = %d", resp.StatusCode)
	}
	var verdict struct {
		Verdict session.StopResult `json:"verdict"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&verdict); err != nil {
		t.Fatal(err)
	}
	if verdict.Verdict != session.StopStopped {
		t.Fatalf("verdict = %q", verdict.Verdict)
	}
	if _, err := st.GetSession(started.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session row still exists: %v", err)
	}
	if session.ProcessMatches(started.PID, started.StartTicks) {
		t.Fatal("deleted session process is still alive")
	}
}

func TestStartupRestoresSessionAndScrollback(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.PutWorktree(model.Worktree{
		ID: "wt", RepoID: "repo", Path: dir, Workspace: true, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutSession(model.Session{
		ID: "restore-me", WorktreeID: "wt", Kind: "shell", Agent: "shell",
		Command: "/bin/sh", Args: []string{"-l"}, Title: "shell", Status: model.SessionRunning,
		Cols: 80, Rows: 24, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutTranscript("restore-me", []byte("old output\r\n")); err != nil {
		t.Fatal(err)
	}
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	_ = New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))

	sess, ok := mgr.Get("restore-me")
	if !ok {
		t.Fatal("session was not restored")
	}
	if sess.Status() != model.SessionRunning {
		t.Fatalf("restored status = %q", sess.Status())
	}
	if !bytes.Contains(sess.Snapshot(), []byte("old output")) {
		t.Fatalf("restored scrollback = %q", sess.Snapshot())
	}
	item, err := st.GetSession("restore-me")
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != model.SessionRunning || item.Origin != "user" {
		t.Fatalf("stored session = %#v", item)
	}
	if _, err := mgr.RemoveWithResult("restore-me", "test_done"); err != nil {
		t.Fatal(err)
	}
}

func TestStartupMarksUnrestorableSessionLost(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.PutSession(model.Session{
		ID: "stale", WorktreeID: "wt", Command: "/bin/sh", Status: model.SessionRunning,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	item, err := st.GetSession("stale")
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != model.SessionLost || item.EndedAt == nil || item.Reason != "restore_failed" {
		t.Fatalf("stored session = %#v", item)
	}

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/sessions/stale", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete stale status = %d", resp.StatusCode)
	}
	var verdict struct {
		Verdict session.StopResult `json:"verdict"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&verdict); err != nil {
		t.Fatal(err)
	}
	if verdict.Verdict != session.StopUnverifiable {
		t.Fatalf("verdict = %q, want unverifiable", verdict.Verdict)
	}
}

func TestRunAPIExecutesCommand(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.StateDir = t.TempDir()
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	repo, err := st.PutRepo(model.Repo{Name: "demo", Path: t.TempDir(), Git: true})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := st.PutWorktree(model.Worktree{RepoID: repo.ID, Path: t.TempDir(), Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{
		"worktreeId": wt.ID,
		"goal":       "run a command",
		"steps": []map[string]any{{
			"kind": "command", "name": "hello", "command": []string{"/bin/sh", "-c", "printf run-ok"},
		}},
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/runs", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var run model.Run
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create run status = %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/runs/"+run.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("start run status = %d", resp.StatusCode)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/runs/"+run.ID, nil)
		req.Header.Set("Authorization", "Bearer test-token")
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var detail struct {
			Run   model.Run    `json:"run"`
			Steps []model.Step `json:"steps"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if detail.Run.State == model.RunSucceeded {
			if len(detail.Steps) != 1 || detail.Steps[0].State != model.StepSucceeded || !strings.Contains(detail.Steps[0].Output, "run-ok") {
				t.Fatalf("detail = %#v", detail)
			}
			return
		}
		if detail.Run.State == model.RunFailed {
			t.Fatalf("run failed: %#v", detail)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run did not finish")
}

func TestOpenExchangesFragmentCodeForHttpOnlyCookie(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open/code", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var opened openCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&opened); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open code status = %d", resp.StatusCode)
	}
	parsed, err := url.Parse(opened.URL)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimPrefix(parsed.Fragment, "code=")
	if len(code) != 64 {
		t.Fatalf("fragment code = %q", parsed.Fragment)
	}

	client := newSessionClient()
	body, _ := json.Marshal(map[string]string{"code": code})
	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open exchange status = %d", resp.StatusCode)
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookieName {
		t.Fatalf("cookies = %#v", cookies)
	}
	if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie is not HttpOnly/SameSite=Strict: %#v", cookies[0])
	}

	req, err = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cookie state status = %d", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cookie mutation without X-DF-Request = %d, want 403", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("state after logout = %d, want 401", resp.StatusCode)
	}

	// The code is single-use.
	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused code status = %d, want 401", resp.StatusCode)
	}
}

func TestBrowserCookieSurvivesServerRestart(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	// A real restart reloads the same secret from disk; the two Servers here
	// share one Config, so pin it explicitly.
	cfg.InstanceSecret = "test-instance-secret"
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	first := httptest.NewServer(New(cfg, st, mgr, log).Handler())
	req, err := http.NewRequest(http.MethodPost, first.URL+"/api/v1/open/code", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var opened openCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&opened); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	parsed, err := url.Parse(opened.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := newSessionClient()
	body, _ := json.Marshal(map[string]string{"code": strings.TrimPrefix(parsed.Fragment, "code=")})
	req, err = http.NewRequest(http.MethodPost, first.URL+"/api/v1/open", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open exchange status = %d", resp.StatusCode)
	}
	first.Close()

	second := httptest.NewServer(New(cfg, st, mgr, log).Handler())
	defer second.Close()
	req, err = http.NewRequest(http.MethodGet, second.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cookie state after restart = %d, want 200", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodPost, second.URL+"/api/v1/open/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", resp.StatusCode)
	}
	browsers, err := st.ListBrowsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(browsers) != 0 {
		t.Fatalf("browser sessions after logout = %#v", browsers)
	}
}

func newPairServer(t *testing.T, cfg config.Config) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mgr := session.NewManager(1024)
	t.Cleanup(mgr.CloseAll)
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestPairCodeExchangesForCookieAndIsOneTime(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	srv := newPairServer(t, cfg)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/pair/code", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pair code status = %d", resp.StatusCode)
	}
	var issued pairCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(issued.Code) != 9 || issued.Code[4] != '-' {
		t.Fatalf("pair code = %q", issued.Code)
	}
	if !strings.Contains(issued.URL, "#pair=") {
		t.Fatalf("pair url = %q", issued.URL)
	}

	client := newSessionClient()
	body, _ := json.Marshal(map[string]string{"code": issued.Code})
	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/pair", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pair status = %d", resp.StatusCode)
	}
	if client.cookie == nil {
		t.Fatal("pair did not set a cookie")
	}
	req, err = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("state with paired cookie = %d", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/pair", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused pair code = %d, want 401", resp.StatusCode)
	}
}

func TestPairCodeRequiresAuthentication(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	srv := newPairServer(t, cfg)
	resp, err := http.Post(srv.URL+"/api/v1/pair/code", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated pair code = %d, want 401", resp.StatusCode)
	}
}

func TestPairCodeUsesPublicURL(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	cfg.PublicURL = "https://df.example.ts.net/"
	srv := newPairServer(t, cfg)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/pair/code", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pair code status = %d", resp.StatusCode)
	}
	var issued pairCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(issued.URL, "https://df.example.ts.net/#pair=") {
		t.Fatalf("pair url = %q, want publicURL origin", issued.URL)
	}
}

func TestHostAllowedPublicURL(t *testing.T) {
	s := &Server{cfg: config.Config{PublicURL: "https://df.example.ts.net"}}
	req := httptest.NewRequest(http.MethodGet, "https://df.example.ts.net/api/v1/state", nil)
	req.Host = "df.example.ts.net"
	if !s.hostAllowed(req) {
		t.Fatal("publicURL host was refused")
	}
	req.Host = "other.example"
	if s.hostAllowed(req) {
		t.Fatal("unrelated host was allowed")
	}
}

func TestPairRefusesPlainHTTPRemote(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	cfg.AllowedHosts = []string{"example.com"}
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	handler := New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()

	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/pair", strings.NewReader(`{"code":"AAAA-AAAA"}`))
	req.Host = "example.com"
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "pair_https_only") {
		t.Fatalf("plain remote pair = %d %q", rec.Code, rec.Body.String())
	}

	// A proxy on loopback is still remote when it forwards a plain request.
	req = httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/pair", strings.NewReader(`{"code":"AAAA-AAAA"}`))
	req.Host = "example.com"
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "pair_https_only") {
		t.Fatalf("forwarded plain pair = %d %q", rec.Code, rec.Body.String())
	}

	// A non-loopback plain-HTTP listener refuses pairing even if the client
	// spoofs X-Forwarded-Proto.
	remoteCfg := cfg
	remoteCfg.Listen = "0.0.0.0:8787"
	remoteHandler := New(remoteCfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	req = httptest.NewRequest(http.MethodPost, "http://example.com/api/v1/pair", strings.NewReader(`{"code":"AAAA-AAAA"}`))
	req.Host = "example.com"
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec = httptest.NewRecorder()
	remoteHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "pair_https_only") {
		t.Fatalf("non-loopback listener pair = %d %q", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "https://example.com/api/v1/pair", strings.NewReader(`{"code":"AAAA-AAAA"}`))
	req.Host = "example.com"
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("https remote pair with bad code = %d, want 401", rec.Code)
	}
}

func TestPairRateLimit(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	srv := newPairServer(t, cfg)
	for i := 0; i < pairSourceLimit; i++ {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/pair", strings.NewReader(`{"code":"AAAA-AAAA"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-DF-Request", "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, resp.StatusCode)
		}
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/pair", strings.NewReader(`{"code":"AAAA-AAAA"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rate limited pair = %d, want 429", resp.StatusCode)
	}
}

func TestCookieRejectsForeignHost(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	server := New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	// Mint and exchange directly against the handler so the test controls Host.
	client := newSessionClient()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open/code", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var opened openCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&opened); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	parsed, _ := url.Parse(opened.URL)
	body, _ := json.Marshal(map[string]string{"code": strings.TrimPrefix(parsed.Fragment, "code=")})
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/state", nil)
	req.Host = "evil.example"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Host status = %d, want 403", resp.StatusCode)
	}
}

func newAPITestServer(t *testing.T) (*httptest.Server, *store.Store, config.Config) {
	t.Helper()
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := session.NewManager(1024)
	t.Cleanup(func() {
		mgr.CloseAll()
		st.Close()
	})
	srv := httptest.NewServer(New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	t.Cleanup(srv.Close)
	return srv, st, cfg
}

func TestSessionCookieIsHostPrefixedAndSecretBound(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	cfg.InstanceSecret = "instance-alpha"
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(New(cfg, st, mgr, log).Handler())
	defer srv.Close()

	client := newSessionClient()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open/code", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var opened openCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&opened); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	parsed, err := url.Parse(opened.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": strings.TrimPrefix(parsed.Fragment, "code=")})
	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open exchange status = %d", resp.StatusCode)
	}

	cookie := client.cookie
	if cookie == nil {
		t.Fatal("open did not set a session cookie")
	}
	if cookie.Name != cookieName || !strings.HasPrefix(cookie.Name, "__Host-") {
		t.Fatalf("cookie name = %q, want a __Host- name", cookie.Name)
	}
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("cookie flags = %#v", cookie)
	}
	if cookie.Domain != "" {
		t.Fatalf("cookie has Domain %q; __Host- forbids it", cookie.Domain)
	}

	// The row is keyed with the instance secret, never with the bare token.
	rows, err := st.ListBrowsers()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows[secretHash(cookie.Value)]; ok {
		t.Fatal("browser row is keyed without the instance secret")
	}
	if _, ok := rows[secretHash("instance-alpha|"+cookie.Value)]; !ok {
		t.Fatal("browser row is not keyed with the instance secret")
	}

	// A daemon that lost or changed the secret must reject the old cookie.
	rotated := cfg
	rotated.InstanceSecret = "instance-beta"
	other := httptest.NewServer(New(rotated, st, mgr, log).Handler())
	defer other.Close()
	req, err = http.NewRequest(http.MethodGet, other.URL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cookie after the secret changed = %d, want 401", resp.StatusCode)
	}
}

func TestSecureEqualRejectsEmpty(t *testing.T) {
	if secureEqual("", "") {
		t.Fatal("two empty strings must not compare equal")
	}
	if secureEqual("", "x") || secureEqual("x", "") {
		t.Fatal("empty and non-empty must not compare equal")
	}
	if !secureEqual("same", "same") {
		t.Fatal("equal strings must compare equal")
	}
	if secureEqual("same", "diff") {
		t.Fatal("different strings must not compare equal")
	}
}

func TestLegacyBrowserRowsAreDroppedOnLoad(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	cfg.InstanceSecret = "instance-alpha"
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now().UTC()
	legacy := store.Browser{
		ID:         "legacy-device",
		Label:      "old browser",
		Scope:      scopeFull,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(time.Hour),
		// No KeyVersion: keyed with the bare cookie hash, before the salt.
	}
	if err := st.PutBrowser(secretHash("legacy-cookie-token"), legacy); err != nil {
		t.Fatal(err)
	}

	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	New(cfg, st, mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))

	rows, err := st.ListBrowsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("legacy rows survived the load: %#v", rows)
	}
}

func mintBrowserCookie(t *testing.T, srv *httptest.Server, bearer string) *sessionClient {
	t.Helper()
	client := newSessionClient()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open/code", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var opened openCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&opened); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	parsed, err := url.Parse(opened.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": strings.TrimPrefix(parsed.Fragment, "code=")})
	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open exchange status = %d", resp.StatusCode)
	}
	if client.cookie == nil {
		t.Fatal("open did not set a session cookie")
	}
	return client
}

func TestTerminalRoutes(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodPost, "/api/v1/sessions", true},
		{http.MethodGet, "/api/v1/sessions", false},
		{http.MethodGet, "/api/v1/sessions/ses_1/stream", true},
		{http.MethodPost, "/api/v1/sessions/ses_1/input", true},
		{http.MethodPost, "/api/v1/sessions/ses_1/resize", true},
		{http.MethodPost, "/api/v1/sessions/ses_1/redraw", true},
		{http.MethodDelete, "/api/v1/sessions/ses_1", false},
		{http.MethodGet, "/api/v1/state", false},
		{http.MethodGet, "/api/v1/opencode/sessions", false},
		{http.MethodPost, "/api/v1/worktrees", false},
	}
	for _, tc := range cases {
		r, err := http.NewRequest(tc.method, "http://127.0.0.1:8787"+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := isTerminalRoute(r); got != tc.want {
			t.Errorf("isTerminalRoute(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

// doer is what a test needs from a client; *http.Client and sessionClient both
// satisfy it.
type doer interface {
	Do(*http.Request) (*http.Response, error)
}

// sessionClient carries the session cookie by hand. httptest servers are plain
// HTTP and Go's cookiejar refuses to send a Secure cookie over http, while a
// browser accepts one from 127.0.0.1 because loopback is a trustworthy origin.
// The cookie is `__Host-` and therefore always Secure.
type sessionClient struct {
	client *http.Client
	cookie *http.Cookie
}

func newSessionClient() *sessionClient {
	return &sessionClient{client: http.DefaultClient}
}

func (c *sessionClient) Do(req *http.Request) (*http.Response, error) {
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return resp, err
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name != cookieName {
			continue
		}
		if cookie.MaxAge < 0 {
			c.cookie = nil
			continue
		}
		c.cookie = cookie
	}
	return resp, nil
}

func postJSON(t *testing.T, url, token string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-q", "-m", "init")
	return dir
}

// A browser fetch only hands the response to the page when the first body byte
// arrives. A TUI attach suppresses the replayed ring tail, and with no hello
// byte the page never read X-DF-Redraw, never asked for a repaint, and showed a
// black pane until the app next wrote something by itself. Android Firefox did
// exactly that on every attach to an idle OpenCode session.
func TestStreamAttachYieldsAByteWhenReplayIsSuppressed(t *testing.T) {
	srv, st, cfg := newAPITestServer(t)
	resp := postJSON(t, srv.URL+"/api/v1/repos", cfg.Token, map[string]string{"path": t.TempDir()})
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create repo status = %d: %s", resp.StatusCode, raw)
	}
	resp.Body.Close()
	worktrees, err := st.ListWorktrees()
	if err != nil || len(worktrees) != 1 {
		t.Fatalf("worktrees = %#v, %v", worktrees, err)
	}

	created := postJSON(t, srv.URL+"/api/v1/sessions", cfg.Token, map[string]any{
		"worktreeId": worktrees[0].ID, "agent": "shell", "kind": "shell",
		"cols": 80, "rows": 24,
	})
	if created.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(created.Body)
		t.Fatalf("create session status = %d: %s", created.StatusCode, raw)
	}
	var sess model.Session
	if err := json.NewDecoder(created.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	created.Body.Close()

	// An alt-screen switch is what browserSnapshot keys on, so this makes the
	// session's tail look like a full-screen TUI and suppresses the replay.
	// tput writes real terminfo sequences; typing escape bytes here goes through
	// readline and gets mangled by bracketed paste.
	input, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/"+sess.ID+"/input",
		strings.NewReader("tput civis; tput cnorm\n"))
	if err != nil {
		t.Fatal(err)
	}
	input.Header.Set("Authorization", "Bearer "+cfg.Token)
	input.Header.Set("Content-Type", "text/plain")
	sent, err := http.DefaultClient.Do(input)
	if err != nil {
		t.Fatal(err)
	}
	sent.Body.Close()

	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			srv.URL+"/api/v1/sessions/"+sess.ID+"/stream", nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
		stream, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if stream.Header.Get("X-DF-Redraw") != "1" {
			stream.Body.Close()
			cancel()
			if time.Now().After(deadline) {
				t.Skip("the session never produced a TUI-like tail (no tput/terminfo); cannot exercise a suppressed replay")
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}
		first := make([]byte, 4)
		_, err = io.ReadFull(stream.Body, first)
		cancel()
		stream.Body.Close()
		if err != nil {
			t.Fatalf("suppressed attach sent no bytes, so the page cannot read its headers: %v", err)
		}
		if string(first) != "\x1b[0m" {
			t.Fatalf("first bytes = %q, want the SGR-reset hello", first)
		}
		return
	}
}

func TestBrowserRowsFromAnotherSecretAreDropped(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	cfg.InstanceSecret = "secret-before"
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()

	first := New(cfg, st, mgr, log)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/v1/open/code", nil)
	if _, _, err := first.mintBrowserSession(rec, req, "Old browser", scopeFull); err != nil {
		t.Fatal(err)
	}
	if rows, _ := st.ListBrowsers(); len(rows) != 1 {
		t.Fatalf("minted rows = %d, want 1", len(rows))
	}

	// Rotating the instance secret makes the row unmatchable; the next daemon
	// drops it instead of listing a device that can never authenticate.
	rotated := cfg
	rotated.InstanceSecret = "secret-after"
	second := New(rotated, st, mgr, log)
	if rows, _ := st.ListBrowsers(); len(rows) != 0 {
		t.Fatalf("rows from the old secret survived: %#v", rows)
	}
	if len(second.browsers) != 0 {
		t.Fatalf("in-memory browsers from the old secret survived: %#v", second.browsers)
	}
}

func TestDevicesHideRowsFromAnotherSecret(t *testing.T) {
	cfg := config.Default()
	cfg.Token = "test-token"
	cfg.WorktreeRoot = t.TempDir()
	cfg.InstanceSecret = "secret-one"
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := session.NewManager(1024)
	defer mgr.CloseAll()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	server := New(cfg, st, mgr, log)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/v1/open/code", nil)
	if _, _, err := server.mintBrowserSession(rec, req, "Old browser", scopeFull); err != nil {
		t.Fatal(err)
	}

	// Rotate in place: the row is still in the map until it is used or pruned,
	// but the device list must not advertise it.
	server.cfg.InstanceSecret = "secret-two"
	srv := httptest.NewServer(server.Handler())
	defer srv.Close()

	client := newSessionClient()
	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open/code", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var opened openCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&opened); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	parsed, err := url.Parse(opened.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": strings.TrimPrefix(parsed.Fragment, "code=")})
	req, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/open", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DF-Request", "1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	req, err = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/devices", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var devices struct {
		Devices []deviceView `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
		t.Fatal(err)
	}
	for _, device := range devices.Devices {
		if device.Label == "Old browser" {
			t.Fatal("the device list still advertises a session from the old secret")
		}
	}
	if len(devices.Devices) != 1 || !devices.Devices[0].Current {
		t.Fatalf("devices = %#v", devices.Devices)
	}
}
