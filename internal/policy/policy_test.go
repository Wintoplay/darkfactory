package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The four fail-closed properties GV-5 names, one test each, then the ordering
// and matching rules they sit on.

func TestEmptyPolicyDeniesEverything(t *testing.T) {
	par, err := Parse([]byte(`{"version":1,"rules":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"repo.create", "session.input", "logout", "anything"} {
		if got := par.Decide("human:token", verb); got.Allow {
			t.Fatalf("empty policy allowed %s", verb)
		} else if got.Rule != RuleNoRule {
			t.Fatalf("empty policy quoted rule %q, want %q", got.Rule, RuleNoRule)
		}
	}
}

func TestBrokenDenyDenies(t *testing.T) {
	// "[" is a pattern path.Match cannot compile. A deny that cannot be
	// evaluated must not be skipped: the failure mode of a guard is not to
	// open.
	par, err := Parse([]byte(`{"version":1,"rules":[
		{"effect":"deny","principal":"human*","verb":"[","rule":"broken-deny"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := par.Decide("human:token", "repo.create")
	if got.Allow {
		t.Fatal("a deny with an uncompilable pattern permitted the verb")
	}
	if got.Rule != "broken-deny" {
		t.Fatalf("rule = %q, want broken-deny", got.Rule)
	}
}

func TestBrokenAllowDoesNotPermit(t *testing.T) {
	par, err := Parse([]byte(`{"version":1,"rules":[
		{"effect":"allow","principal":"human*","verb":"[","rule":"broken-allow"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := par.Decide("human:token", "repo.create")
	if got.Allow {
		t.Fatal("an allow with an uncompilable pattern permitted the verb")
	}
	if got.Rule != RuleNoRule {
		t.Fatalf("rule = %q, want %q", got.Rule, RuleNoRule)
	}
}

func TestMalformedPolicyRefusesStartup(t *testing.T) {
	cases := map[string]string{
		"not json":            `{`,
		"unknown field":       `{"version":1,"rules":[],"extra":true}`,
		"wrong version":       `{"version":2,"rules":[]}`,
		"no version":          `{"rules":[]}`,
		"unknown effect":      `{"version":1,"rules":[{"effect":"maybe","principal":"*","verb":"*","rule":"r"}]}`,
		"missing effect":      `{"version":1,"rules":[{"principal":"*","verb":"*","rule":"r"}]}`,
		"missing rule name":   `{"version":1,"rules":[{"effect":"allow","principal":"*","verb":"*"}]}`,
		"missing principal":   `{"version":1,"rules":[{"effect":"allow","verb":"*","rule":"r"}]}`,
		"missing verb":        `{"version":1,"rules":[{"effect":"allow","principal":"*","rule":"r"}]}`,
		"trailing object":     `{"version":1,"rules":[]}{"version":1}`,
		"rules is not a list": `{"version":1,"rules":{}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(raw)); err == nil {
				t.Fatalf("accepted a malformed policy: %s", raw)
			}
		})
	}
}

func TestMissingFileIsTheDefaultPolicy(t *testing.T) {
	par, err := Load(filepath.Join(t.TempDir(), "policy.json"))
	if err != nil {
		t.Fatalf("missing policy file should fall back to the default: %v", err)
	}
	if !par.Decide("human:token", "repo.create").Allow {
		t.Fatal("the default policy does not allow the human surface")
	}
	if par.Decide("worker:abc", "session.input").Allow {
		t.Fatal("the default policy allowed a worker")
	}
}

func TestLoadReadsAFile(t *testing.T) {
	pathname := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(pathname, []byte(`{"version":1,"rules":[
		{"effect":"allow","principal":"human*","verb":"*","rule":"human"},
		{"effect":"deny","principal":"human*","verb":"repo.delete","rule":"no-deletes"}
	]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	par, err := Load(pathname)
	if err != nil {
		t.Fatal(err)
	}
	if got := par.Decide("human:token", "repo.create"); !got.Allow || got.Rule != "human" {
		t.Fatalf("repo.create = %#v, want allow by human", got)
	}
	if got := par.Decide("human:token", "repo.delete"); got.Allow || got.Rule != "no-deletes" {
		t.Fatalf("repo.delete = %#v, want deny by no-deletes", got)
	}
}

func TestBrokenFileFailsLoad(t *testing.T) {
	pathname := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(pathname, []byte(`{"version":9}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(pathname); err == nil {
		t.Fatal("an existing but invalid policy file was accepted")
	}
}

// TestDenyBeatsAllowInEitherOrder is GV-5's "deny before allow": the file's
// order is presentation, not precedence.
func TestDenyBeatsAllowInEitherOrder(t *testing.T) {
	allowFirst, err := Parse([]byte(`{"version":1,"rules":[
		{"effect":"allow","principal":"human*","verb":"*","rule":"allow-all"},
		{"effect":"deny","principal":"human*","verb":"repo.delete","rule":"no-deletes"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	denyFirst, err := Parse([]byte(`{"version":1,"rules":[
		{"effect":"deny","principal":"human*","verb":"repo.delete","rule":"no-deletes"},
		{"effect":"allow","principal":"human*","verb":"*","rule":"allow-all"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, par := range map[string]*Policy{"allow first": allowFirst, "deny first": denyFirst} {
		got := par.Decide("human:token", "repo.delete")
		if got.Allow || got.Rule != "no-deletes" {
			t.Fatalf("%s: repo.delete = %#v, want deny by no-deletes", name, got)
		}
		if got := par.Decide("human:token", "repo.create"); !got.Allow {
			t.Fatalf("%s: repo.create should stay allowed", name)
		}
	}
}

func TestPatternsMatchLabelsAndVerbs(t *testing.T) {
	par, err := Parse([]byte(`{"version":1,"rules":[
		{"effect":"allow","principal":"human:device:*","verb":"opencode.*","rule":"phone-chat"},
		{"effect":"allow","principal":"human:token","verb":"repo.create","rule":"one-verb"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		principal, verb string
		allow           bool
		rule            string
	}{
		{"human:device:abc", "opencode.prompt", true, "phone-chat"},
		{"human:device:abc", "repo.create", false, RuleNoRule},
		{"human:token", "opencode.prompt", false, RuleNoRule},
		{"human:token", "repo.create", true, "one-verb"},
		{"worker:abc", "opencode.prompt", false, RuleNoRule},
	}
	for _, tc := range cases {
		got := par.Decide(tc.principal, tc.verb)
		if got.Allow != tc.allow || got.Rule != tc.rule {
			t.Fatalf("%s %s = %#v, want allow=%v rule=%s", tc.principal, tc.verb, got, tc.allow, tc.rule)
		}
	}
}

func TestHashTracksMeaningNotFormatting(t *testing.T) {
	compact, err := Parse([]byte(`{"version":1,"rules":[{"effect":"allow","principal":"human*","verb":"*","rule":"human"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	spaced, err := Parse([]byte("{\n  \"version\": 1,\n  \"rules\": [\n    {\n      \"effect\": \"allow\",\n      \"principal\": \"human*\",\n      \"verb\": \"*\",\n      \"rule\": \"human\"\n    }\n  ]\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if compact.Hash() != spaced.Hash() {
		t.Fatalf("the same policy hashed differently: %s vs %s", compact.Hash(), spaced.Hash())
	}
	if len(compact.Hash()) != 12 || strings.Trim(compact.Hash(), "0123456789abcdef") != "" {
		t.Fatalf("hash = %q, want 12 hex characters", compact.Hash())
	}

	changed, err := Parse([]byte(`{"version":1,"rules":[{"effect":"allow","principal":"human*","verb":"repo.*","rule":"human"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if changed.Hash() == compact.Hash() {
		t.Fatal("changing a rule did not change the policy hash")
	}
	if empty := (&Policy{Version: 1}).Hash(); empty == compact.Hash() {
		t.Fatal("an empty policy hashes like the default")
	}
}

// TestDefaultPolicyIsWhatRunsWithNoFile keeps `darkfactory policy` honest: the
// printed default must be the one the gateway uses.
func TestDefaultPolicyIsWhatRunsWithNoFile(t *testing.T) {
	par, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if par.Hash() != Default().Hash() {
		t.Fatal("Load with no path is not the default policy")
	}
	if got := par.Decide("human:device:abc", "worktree.delete"); !got.Allow {
		t.Fatalf("the default policy refused a paired human: %#v", got)
	}
}
