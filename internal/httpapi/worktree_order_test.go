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

// seedFleet writes one repo and n worktrees into it, oldest first.
func seedFleet(t *testing.T, s *Server, n int) (model.Repo, []model.Worktree) {
	t.Helper()
	repo, err := s.store.PutRepo(model.Repo{Name: "proj", Path: "/tmp/proj", Git: true})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	rows := make([]model.Worktree, 0, n)
	for i := 0; i < n; i++ {
		wt, err := s.store.PutWorktree(model.Worktree{
			RepoID:    repo.ID,
			Path:      "/tmp/proj-" + string(rune('a'+i)),
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, wt)
	}
	return repo, rows
}

func fleetOrder(t *testing.T, srvURL, repoID string) []string {
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
		Worktrees []model.Worktree `json:"worktrees"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, wt := range state.Worktrees {
		if wt.RepoID == repoID {
			order = append(order, wt.ID)
		}
	}
	return order
}

func TestReorderWorktreesPersistsAndIsAudited(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	repo, rows := seedFleet(t, s, 3)

	body, err := json.Marshal(map[string]any{"repoId": repo.ID, "ids": []string{rows[2].ID, rows[0].ID, rows[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/worktrees/order", bytes.NewReader(body))
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
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}

	want := []string{rows[2].ID, rows[0].ID, rows[1].ID}
	if got := fleetOrder(t, srv.URL, repo.ID); len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("fleet order = %v, want %v", got, want)
	}

	// The drag is a mutation like any other: it must appear in the ledger, and
	// the outcome row must name the repo the client reordered.
	entries, _, err := s.store.Ledger(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("ledger rows = %d, want a decision and an outcome", len(entries))
	}
	if entries[0].Verb != verbWorktreeOrder || entries[0].Decision != store.LedgerAllow {
		t.Fatalf("decision row = %#v", entries[0])
	}
	if entries[1].Outcome != store.LedgerApplied || entries[1].Target != repo.ID {
		t.Fatalf("outcome row = %#v, want applied against the repo", entries[1])
	}
}

func TestReorderRefusesAWorktreeFromAnotherRepo(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	repo, rows := seedFleet(t, s, 2)
	otherRepo, otherRows := seedFleet(t, s, 1)

	body, err := json.Marshal(map[string]any{"repoId": repo.ID, "ids": []string{rows[0].ID, otherRows[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/worktrees/order", bytes.NewReader(body))
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
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, raw)
	}
	var refusal refusalEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&refusal); err != nil {
		t.Fatal(err)
	}
	if refusal.Code != codeWorktreeOrderInvalid {
		t.Fatalf("code = %q, want %q", refusal.Code, codeWorktreeOrderInvalid)
	}
	if got := fleetOrder(t, srv.URL, repo.ID); got[0] != rows[0].ID || got[1] != rows[1].ID {
		t.Fatalf("a refused reorder moved rows: %v", got)
	}
	if got := fleetOrder(t, srv.URL, otherRepo.ID); got[0] != otherRows[0].ID {
		t.Fatalf("the other repo changed: %v", got)
	}
}

func TestReorderRefusesUnknownRepoAndEmptyList(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	repo, rows := seedFleet(t, s, 1)

	cases := []struct {
		name string
		body map[string]any
		code string
		want int
	}{
		{"unknown repo", map[string]any{"repoId": "repo_missing", "ids": []string{rows[0].ID}}, codeRepoNotFound, http.StatusNotFound},
		{"no ids", map[string]any{"repoId": repo.ID, "ids": []string{}}, codeWorktreeOrderInvalid, http.StatusBadRequest},
		{"no repo", map[string]any{"ids": []string{rows[0].ID}}, codeInvalidRequest, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/worktrees/order", bytes.NewReader(body))
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
			if resp.StatusCode != tc.want {
				raw, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.want, raw)
			}
			var refusal refusalEnvelope
			if err := json.NewDecoder(resp.Body).Decode(&refusal); err != nil {
				t.Fatal(err)
			}
			if refusal.Code != tc.code {
				t.Fatalf("code = %q, want %q", refusal.Code, tc.code)
			}
		})
	}
}
