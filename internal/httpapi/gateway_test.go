package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/Wintoplay/darkfactory/internal/store"
)

type testRecorder struct {
	mu     sync.Mutex
	events []decision
	err    error
}

func (r *testRecorder) Record(event decision) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return r.err
}

func (r *testRecorder) snapshot() []decision {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]decision(nil), r.events...)
}

func TestGatewayRecordsBeforeAct(t *testing.T) {
	recorder := &testRecorder{}
	g := newGateway(recorder, nil, nil)
	acted := false
	ref := g.do(principal{Kind: principalHuman, ID: "token"}, verbRepoCreate, "repo_1", func() int {
		acted = true
		return http.StatusCreated
	}, nil)
	if ref != nil {
		t.Fatalf("unexpected refusal: %#v", ref)
	}
	if !acted {
		t.Fatal("act did not run")
	}
	events := recorder.snapshot()
	if len(events) != 2 {
		t.Fatalf("events = %d, want a decision and an outcome", len(events))
	}
	if events[0].Verb != verbRepoCreate || events[0].Target != "repo_1" {
		t.Fatalf("event = %#v", events[0])
	}
	if events[0].Principal.Kind != principalHuman || events[0].Principal.ID != "token" {
		t.Fatalf("principal = %#v", events[0].Principal)
	}
	if events[0].At.IsZero() {
		t.Fatal("event has no timestamp")
	}
	if events[0].Decision != store.LedgerAllow || events[0].Outcome != "" {
		t.Fatalf("decision row = %#v, want an allow with no outcome", events[0])
	}
	if events[1].Outcome != store.LedgerApplied || events[1].Status != http.StatusCreated {
		t.Fatalf("outcome row = %#v, want applied 201", events[1])
	}
}

func TestGatewayRecorderFailurePreventsAct(t *testing.T) {
	recorder := &testRecorder{err: io.ErrUnexpectedEOF}
	g := newGateway(recorder, nil, nil)
	acted := false
	ref := g.do(principal{Kind: principalHuman, ID: "token"}, verbRepoCreate, "repo_1", func() int {
		acted = true
		return http.StatusCreated
	}, nil)
	if acted {
		t.Fatal("act ran even though the decision was not recorded")
	}
	if ref == nil || ref.Code != codeInternalError || ref.Status != http.StatusInternalServerError {
		t.Fatalf("refusal = %#v, want 500 internal_error", ref)
	}
	if ref.Detail == "" {
		t.Fatal("a refused record should say nothing was changed")
	}
}

func TestGatewayDeniesNonHumanPrincipals(t *testing.T) {
	cases := []struct {
		name string
		p    principal
		code string
		rule string
	}{
		{"worker", principal{Kind: principalWorker, ID: "worker_1"}, codeCapabilityScope, "human_only"},
		{"overseer", principal{Kind: principalOverseer, ID: "overseer_1"}, codeNotAPrincipal, "principal"},
		{"anonymous", principal{}, codeNotAPrincipal, "principal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acted := false
			ref := newGateway(nil, nil, nil).do(tc.p, verbRepoCreate, "repo_1", func() int { acted = true; return http.StatusCreated }, nil)
			if acted {
				t.Fatal("act ran for a denied principal")
			}
			if ref == nil || ref.Code != tc.code || ref.Rule != tc.rule {
				t.Fatalf("refusal = %#v, want %s rule=%s", ref, tc.code, tc.rule)
			}
		})
	}
}

// TestEveryMutationGoesThroughGateway is the static half of GV-4: every
// authenticated POST/DELETE in the route table names a gateway verb, and the
// only exemptions are the code-gated, encrypted-envelope, and device-key
// bootstrap routes. The envelope dispatches its inner request through this same
// Handler, so the inner mutation still records its verb.
func TestEveryMutationGoesThroughGateway(t *testing.T) {
	_, s := newRefusalServer(t, nil)
	bootstrap := map[string]bool{
		"POST /api/v1/open":                  true,
		"POST /api/v1/pair":                  true,
		"POST /api/v1/e2ee":                  true,
		"POST /api/v1/device-keys/challenge": true,
		"POST /api/v1/device-keys/verify":    true,
	}
	seen := map[string]string{}
	for _, rt := range s.routeTable() {
		key := rt.method + " " + rt.path
		mutation := rt.method != http.MethodGet && rt.method != http.MethodHead && rt.method != http.MethodOptions
		switch {
		case mutation && !bootstrap[key] && rt.verb == "":
			t.Errorf("%s is a mutation without a gateway verb", key)
		case !mutation && rt.verb != "":
			t.Errorf("%s is a read with gateway verb %q", key, rt.verb)
		case mutation && !bootstrap[key] && !rt.auth:
			t.Errorf("%s is a mutation outside auth", key)
		}
		if rt.verb == "" {
			continue
		}
		if other, ok := seen[rt.verb]; ok {
			t.Errorf("verb %q is used by both %s and %s", rt.verb, other, key)
		}
		seen[rt.verb] = key
	}
	if len(bootstrap) != 5 {
		t.Fatalf("bootstrap exemptions = %d, want 5", len(bootstrap))
	}
}

func TestFailedRecordLeavesNoRepo(t *testing.T) {
	recorder := &testRecorder{err: io.ErrUnexpectedEOF}
	srv, s := newRefusalServer(t, func(s *Server) { s.gateway = newGateway(recorder, nil, nil) })
	dir := t.TempDir()

	resp := postJSON(t, srv.URL+"/api/v1/repos", "test-token", map[string]string{"path": dir})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 500: %s", resp.StatusCode, raw)
	}
	var body refusalEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != codeInternalError {
		t.Fatalf("code = %q, want %q", body.Code, codeInternalError)
	}
	repos, err := s.store.ListRepos()
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 0 {
		t.Fatalf("repo was created despite a failed record: %#v", repos)
	}
	events := recorder.snapshot()
	if len(events) != 1 || events[0].Verb != verbRepoCreate {
		t.Fatalf("recorded events = %#v", events)
	}
}

func TestSuccessfulMutationIsRecorded(t *testing.T) {
	recorder := &testRecorder{}
	srv, _ := newRefusalServer(t, func(s *Server) { s.gateway = newGateway(recorder, nil, nil) })
	dir := t.TempDir()

	resp := postJSON(t, srv.URL+"/api/v1/repos", "test-token", map[string]string{"path": dir})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201: %s", resp.StatusCode, raw)
	}
	events := recorder.snapshot()
	if len(events) != 2 {
		t.Fatalf("events = %d, want a decision and an outcome", len(events))
	}
	if events[0].Verb != verbRepoCreate {
		t.Fatalf("verb = %q, want %q", events[0].Verb, verbRepoCreate)
	}
	if events[0].Principal.ID != "token" {
		t.Fatalf("principal = %#v", events[0].Principal)
	}
	if events[1].Outcome != store.LedgerApplied || events[1].Status != http.StatusCreated {
		t.Fatalf("outcome row = %#v, want applied 201", events[1])
	}
}
