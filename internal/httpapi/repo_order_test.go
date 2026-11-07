package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/model"
	"github.com/Wintoplay/darkfactory/internal/store"
)

// seedRepos writes n repos, oldest first, and returns them in creation order.
func seedRepos(t *testing.T, s *Server, n int) []model.Repo {
	t.Helper()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	rows := make([]model.Repo, 0, n)
	for i := 0; i < n; i++ {
		repo, err := s.store.PutRepo(model.Repo{
			Name:      "proj-" + string(rune('a'+i)),
			Path:      "/tmp/proj-" + string(rune('a'+i)),
			Git:       true,
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, repo)
	}
	return rows
}

func repoOrder(t *testing.T, srvURL string) []string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srvURL+"/api/v1/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var state struct {
		Repos []model.Repo `json:"repos"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	order := make([]string, 0, len(state.Repos))
	for _, repo := range state.Repos {
		order = append(order, repo.ID)
	}
	return order
}

func postRepoOrder(t *testing.T, srvURL string, ids []string) (*http.Response, []byte) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srvURL+"/api/v1/repos/order", bytes.NewReader(body))
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
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, raw
}

func TestReorderReposPersistsAndIsAudited(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	rows := seedRepos(t, s, 3)

	resp, raw := postRepoOrder(t, srv.URL, []string{rows[2].ID, rows[0].ID, rows[1].ID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	want := []string{rows[2].ID, rows[0].ID, rows[1].ID}
	if got := repoOrder(t, srv.URL); len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("repo order = %v, want %v", got, want)
	}

	entries, _, err := s.store.Ledger(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("ledger rows = %d, want a decision and an outcome", len(entries))
	}
	if entries[0].Verb != verbRepoOrder || entries[0].Decision != store.LedgerAllow {
		t.Fatalf("decision row = %#v", entries[0])
	}
	if entries[1].Outcome != store.LedgerApplied || entries[1].Target != "fleet" {
		t.Fatalf("outcome row = %#v, want applied against the fleet", entries[1])
	}
}

func TestReorderReposKeepsUnnamedRepos(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	rows := seedRepos(t, s, 3)
	resp, raw := postRepoOrder(t, srv.URL, []string{rows[2].ID, rows[0].ID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	want := []string{rows[2].ID, rows[0].ID, rows[1].ID}
	if got := repoOrder(t, srv.URL); len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("repo order = %v, want %v (unnamed repo last, age order)", got, want)
	}
}

func TestReorderReposRefusesBadIDs(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	rows := seedRepos(t, s, 2)

	cases := []struct {
		name string
		ids  []string
		code string
		want int
	}{
		{"duplicate", []string{rows[0].ID, rows[0].ID}, codeRepoOrderInvalid, http.StatusBadRequest},
		{"unknown", []string{rows[0].ID, "repo_missing"}, codeRepoOrderInvalid, http.StatusBadRequest},
		{"empty", []string{}, codeRepoOrderInvalid, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, raw := postRepoOrder(t, srv.URL, tc.ids)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.want, raw)
			}
			var refusal refusalEnvelope
			if err := json.Unmarshal(raw, &refusal); err != nil {
				t.Fatal(err)
			}
			if refusal.Code != tc.code {
				t.Fatalf("code = %q, want %q", refusal.Code, tc.code)
			}
		})
	}
	if got := repoOrder(t, srv.URL); got[0] != rows[0].ID || got[1] != rows[1].ID {
		t.Fatalf("a refused reorder moved repos: %v", got)
	}
}

func TestReorderReposWithNoReposIsNotFound(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	resp, raw := postRepoOrder(t, srv.URL, []string{"repo_missing"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", resp.StatusCode, raw)
	}
	var refusal refusalEnvelope
	if err := json.Unmarshal(raw, &refusal); err != nil {
		t.Fatal(err)
	}
	if refusal.Code != codeRepoNotFound {
		t.Fatalf("code = %q, want %q", refusal.Code, codeRepoNotFound)
	}
}
