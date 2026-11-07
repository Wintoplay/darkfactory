package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Wintoplay/darkfactory/internal/policy"
	"github.com/Wintoplay/darkfactory/internal/store"
)

// TestPolicyDenyIsRefusedAndRecorded is GV-5 end to end: a deny in the policy
// file refuses the mutation before it happens and lands in the ledger with the
// rule that refused it and the policy that wrote it.
func TestPolicyDenyIsRefusedAndRecorded(t *testing.T) {
	par, err := policy.Parse([]byte(`{"version":1,"rules":[
		{"effect":"allow","principal":"human*","verb":"*","rule":"human"},
		{"effect":"deny","principal":"human*","verb":"repo.create","rule":"no-repos"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	srv, s := newRefusalServer(t, func(s *Server) { s.gateway = newGateway(s.gateway.recorder, par, s.log) })

	resp := postJSON(t, srv.URL+"/api/v1/repos", "test-token", map[string]string{"path": t.TempDir()})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 403: %s", resp.StatusCode, raw)
	}
	var body refusalEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != codePolicyDenied || body.Rule != "no-repos" {
		t.Fatalf("refusal = %#v, want policy_denied rule=no-repos", body)
	}
	repos, err := s.store.ListRepos()
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 0 {
		t.Fatalf("a denied mutation created a repo: %#v", repos)
	}
	rows, _, err := s.store.Ledger(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Decision != store.LedgerDeny || rows[0].Rule != "no-repos" {
		t.Fatalf("ledger rows = %#v, want one deny by no-repos", rows)
	}
	if rows[0].Policy != par.Hash() {
		t.Fatalf("row policy = %q, want %q", rows[0].Policy, par.Hash())
	}
}

// TestEmptyPolicyRefusesEveryMutation is "empty policy permits nothing" seen
// from the outside: the human token is a principal, and the file still says no.
func TestEmptyPolicyRefusesEveryMutation(t *testing.T) {
	par, err := policy.Parse([]byte(`{"version":1,"rules":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	srv, _ := newRefusalServer(t, func(s *Server) { s.gateway = newGateway(s.gateway.recorder, par, s.log) })

	requests := []*http.Request{
		mustRequest(t, http.MethodPost, srv.URL+"/api/v1/repos", map[string]string{"path": t.TempDir()}),
		mustRequest(t, http.MethodPost, srv.URL+"/api/v1/worktrees", map[string]string{"repoId": "repo_1", "name": "one"}),
		mustRequest(t, http.MethodDelete, srv.URL+"/api/v1/repos/repo_1", nil),
	}
	for _, req := range requests {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want 403: %s", req.Method, req.URL.Path, resp.StatusCode, raw)
		}
		var body refusalEnvelope
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if body.Code != codePolicyDenied || body.Rule != policy.RuleNoRule {
			t.Fatalf("%s %s refusal = %#v, want policy_denied rule=%s", req.Method, req.URL.Path, body, policy.RuleNoRule)
		}
	}
}

func mustRequest(t *testing.T, method, url string, body any) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestAuditShowsRetentionAndAPruneMarker covers the read side of pruning: the
// window is reported, and the marker row is visible instead of looking like a
// decision somebody made.
func TestAuditShowsRetentionAndAPruneMarker(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if _, err := s.store.AppendLedger(store.LedgerEntry{At: old.Add(time.Duration(i) * time.Minute), Principal: "human:token", Verb: "repo.create", Decision: store.LedgerAllow}); err != nil {
			t.Fatal(err)
		}
	}
	marker, err := s.store.PruneLedger(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if marker == nil {
		t.Fatal("no marker written")
	}

	audit := auditOf(t, srv.URL, "")
	if !audit.Chain.OK {
		t.Fatalf("chain = %#v, a prune with a marker must verify", audit.Chain)
	}
	if audit.RetentionDays != 30 {
		t.Fatalf("retentionDays = %d, want the config default", audit.RetentionDays)
	}
	if len(audit.Entries) != 1 || audit.Entries[0].Pruned == nil {
		t.Fatalf("entries = %#v, want the marker alone", audit.Entries)
	}
	if got := audit.Entries[0].Pruned; got.Dropped != 3 || got.ThroughSeq != 3 || got.ThroughHash == "" {
		t.Fatalf("pruned = %#v, want 3 rows through seq 3", got)
	}
}

// auditOf fetches the ledger the way the CLI does.
func auditOf(t *testing.T, base, query string) auditResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/audit"+query, nil)
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
		t.Fatalf("audit status = %d: %s", resp.StatusCode, raw)
	}
	var out auditResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMutationWritesTwoLedgerRows is GV-4's record half: the decision lands
// before the effect and the outcome lands after it.
func TestMutationWritesTwoLedgerRows(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	resp := postJSON(t, srv.URL+"/api/v1/repos", "test-token", map[string]string{"path": t.TempDir()})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status = %d: %s", resp.StatusCode, raw)
	}

	audit := auditOf(t, srv.URL, "")
	if !audit.Chain.OK {
		t.Fatalf("chain = %#v, want ok", audit.Chain)
	}
	if len(audit.Entries) != 2 {
		t.Fatalf("rows = %d, want a decision and an outcome: %#v", len(audit.Entries), audit.Entries)
	}
	decision, outcome := audit.Entries[0], audit.Entries[1]
	if decision.Decision != store.LedgerAllow || decision.Outcome != "" {
		t.Fatalf("decision row = %#v", decision)
	}
	if decision.Verb != verbRepoCreate || decision.Principal != "human:token" {
		t.Fatalf("decision row = %#v, want %s by human:token", decision, verbRepoCreate)
	}
	if decision.Target == "" {
		t.Fatal("decision row has no target")
	}
	if outcome.Outcome != store.LedgerApplied || outcome.Status != http.StatusCreated {
		t.Fatalf("outcome row = %#v, want applied 201", outcome)
	}
	// The decision row cannot know what a create will make; the outcome row can,
	// so an auditor can tie repo.create to the repo.
	if outcome.Target == "request" || outcome.Target == "" {
		t.Fatalf("outcome row target = %q, want the created repo id", outcome.Target)
	}
	if decision.Target != "request" {
		t.Fatalf("decision row target = %q, want the pre-effect placeholder", decision.Target)
	}
}

// TestRefusedMutationRecordsWhy covers the outcome vocabulary: a handler that
// answers 4xx is a refusal, and the row says so instead of "applied".
func TestRefusedMutationRecordsWhy(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/repos/repo_missing", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}

	audit := auditOf(t, srv.URL, "")
	if len(audit.Entries) != 2 {
		t.Fatalf("rows = %d, want 2: %#v", len(audit.Entries), audit.Entries)
	}
	if got := audit.Entries[1]; got.Outcome != store.LedgerRefused || got.Status != http.StatusNotFound {
		t.Fatalf("outcome row = %#v, want refused 404", got)
	}
}

// TestDeniedPrincipalIsRecorded checks the deny path: a principal the gate
// refuses still leaves a row, with the rule that refused it.
func TestDeniedPrincipalIsRecorded(t *testing.T) {
	_, s := newRefusalServer(t, nil)
	ref := s.gateway.do(principal{Kind: principalWorker, ID: "worker_1"}, verbRepoCreate, "repo_1", func() int {
		t.Fatal("act ran for a denied principal")
		return 0
	}, nil)
	if ref == nil || ref.Code != codeCapabilityScope {
		t.Fatalf("refusal = %#v", ref)
	}

	entries, _, err := s.store.Ledger(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("rows = %d, want 1 deny: %#v", len(entries), entries)
	}
	row := entries[0]
	if row.Decision != store.LedgerDeny || row.Rule != "human_only" {
		t.Fatalf("row = %#v, want deny rule=human_only", row)
	}
	if row.Principal != "worker:worker_1" || row.Outcome != "" {
		t.Fatalf("row = %#v, want the worker named and no outcome", row)
	}
}

// TestAuditReportsTheWindowItReturned is GV-18's "the limit is printed": a
// caller must be able to tell 100 rows from all of them.
func TestAuditReportsTheWindowItReturned(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	for i := 0; i < 3; i++ {
		if _, err := s.store.AppendLedger(store.LedgerEntry{Principal: "human:token", Verb: "repo.create", Target: "repo_1", Decision: store.LedgerAllow}); err != nil {
			t.Fatal(err)
		}
	}

	audit := auditOf(t, srv.URL, "?limit=2")
	if len(audit.Entries) != 2 || !audit.Truncated || audit.Total != 3 || audit.Limit != 2 {
		t.Fatalf("window = %d rows, truncated=%v total=%d limit=%d; want 2/true/3/2", len(audit.Entries), audit.Truncated, audit.Total, audit.Limit)
	}
	if audit.Entries[1].Seq != 3 {
		t.Fatalf("window ends at seq %d, want the newest row", audit.Entries[1].Seq)
	}
}

func TestAuditRefusesBadLimit(t *testing.T) {
	srv, _ := newRefusalServer(t, nil)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/audit?limit=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body refusalEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != codeInvalidRequest || body.Target != "limit" {
		t.Fatalf("refusal = %#v", body)
	}
}

// TestUnknownOutcomeStaysVisible simulates a crash between the decision row
// and the outcome row: the row is there, the outcome is empty, and nothing
// pretends it succeeded (GV-20).
func TestUnknownOutcomeStaysVisible(t *testing.T) {
	srv, s := newRefusalServer(t, nil)
	if _, err := s.store.AppendLedger(store.LedgerEntry{Principal: "human:token", Verb: verbSessionCreate, Target: "sess_1", Decision: store.LedgerAllow}); err != nil {
		t.Fatal(err)
	}
	audit := auditOf(t, srv.URL, "")
	if len(audit.Entries) != 1 || audit.Entries[0].Outcome != "" {
		t.Fatalf("rows = %#v, want one row with no outcome", audit.Entries)
	}
	if !audit.Chain.OK {
		t.Fatalf("chain = %#v, an unknown outcome is not a broken chain", audit.Chain)
	}
}
