// Package policy answers one question for the gateway: may this principal run
// this verb? It is data, not code (GV-5): a JSON file of allow and deny rules,
// evaluated deny-first, with no path to "allow" that a broken rule can open.
//
// Four properties hold and are tested as such:
//
//   - an empty policy permits nothing;
//   - a deny rule whose pattern cannot be evaluated denies;
//   - an allow rule whose pattern cannot be evaluated does not permit;
//   - a malformed policy refuses to start the daemon instead of being ignored.
//
// The unit of decision is a gateway verb, not a byte of terminal input. The
// rule that decided travels back to the caller and into the ledger, so a row
// can say why, not just that.
package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// Version is the only schema this build understands. A file with another
// version is refused rather than half-read.
const Version = 1

const (
	EffectAllow = "allow"
	EffectDeny  = "deny"

	// RuleNoRule is what a decision quotes when no rule matched. There is no
	// effect that reaches it as an allow: no match is a deny.
	RuleNoRule = "no-rule"
)

// Rule is one line of policy. Principal and Verb are glob patterns (`*`, `?`,
// `[...]`) matched with path.Match against the principal label (`human`,
// `human:token`, `human:device:abc`, `worker`, `overseer`) and the audit verb
// (`repo.create`, `opencode.*`). Rule is the name the refusal and the ledger
// quote; it is required, because a deny nobody can name is not auditable.
type Rule struct {
	Effect    string `json:"effect"`
	Principal string `json:"principal"`
	Verb      string `json:"verb"`
	Rule      string `json:"rule"`
}

// Policy is the file. Rule order is display order only: every deny is
// evaluated before any allow.
type Policy struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

// Decision is the verdict plus the rule that produced it. Allow is false for
// every path that is not an explicit allow.
type Decision struct {
	Allow bool
	Rule  string
}

// Default is the policy the daemon runs when no file exists: the human surface
// keeps every verb, and nothing else gets one. It is the same shape a file
// would have, so `darkfactory policy` can always print what is in force.
func Default() *Policy {
	return &Policy{Version: Version, Rules: []Rule{
		{Effect: EffectAllow, Principal: "human*", Verb: "*", Rule: "default-human"},
	}}
}

// Parse reads a policy document. Every structural problem is an error: an
// unknown field, a wrong version, an unknown effect, a rule that cannot be
// named, or a pattern left empty. Pattern *syntax* is not checked here on
// purpose — a pattern that fails to compile fails closed at match time, which
// is what "a broken deny denies, a broken allow does not permit" means.
func Parse(raw []byte) (*Policy, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var par Policy
	if err := decoder.Decode(&par); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("parse policy: trailing content after the object")
	}
	if par.Version != Version {
		return nil, fmt.Errorf("parse policy: version %d, want %d", par.Version, Version)
	}
	for i, rule := range par.Rules {
		switch rule.Effect {
		case EffectAllow, EffectDeny:
		case "":
			return nil, fmt.Errorf("rule %d: no effect", i+1)
		default:
			return nil, fmt.Errorf("rule %d: effect %q is not %s or %s", i+1, rule.Effect, EffectAllow, EffectDeny)
		}
		if strings.TrimSpace(rule.Rule) == "" {
			return nil, fmt.Errorf("rule %d: no rule name", i+1)
		}
		if rule.Principal == "" {
			return nil, fmt.Errorf("rule %d (%s): no principal", i+1, rule.Rule)
		}
		if rule.Verb == "" {
			return nil, fmt.Errorf("rule %d (%s): no verb", i+1, rule.Rule)
		}
	}
	return &par, nil
}

// Load reads the policy file. A missing file is the built-in default, not an
// error; a file that exists and does not parse is an error the daemon must not
// start through.
func Load(pathname string) (*Policy, error) {
	if strings.TrimSpace(pathname) == "" {
		return Default(), nil
	}
	raw, err := os.ReadFile(pathname)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pathname, err)
	}
	par, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", pathname, err)
	}
	return par, nil
}

// Decide evaluates the policy for one principal label and verb. Deny rules are
// all evaluated first; the first deny that matches wins, and only then can an
// allow match. No match is a deny (`RuleNoRule`).
func (p *Policy) Decide(principal, verb string) Decision {
	if p == nil {
		p = Default()
	}
	for _, rule := range p.Rules {
		if rule.Effect != EffectDeny {
			continue
		}
		matched, err := rule.match(principal, verb)
		if err != nil || matched {
			// A deny whose pattern cannot be evaluated denies: the failure
			// mode of a guard is not to open.
			return Decision{Allow: false, Rule: rule.Rule}
		}
	}
	for _, rule := range p.Rules {
		if rule.Effect != EffectAllow {
			continue
		}
		if matched, err := rule.match(principal, verb); err == nil && matched {
			return Decision{Allow: true, Rule: rule.Rule}
		}
	}
	return Decision{Allow: false, Rule: RuleNoRule}
}

// match reports whether both patterns select this principal and verb. It
// returns the matcher's error so the caller can fail closed on a bad pattern.
func (r Rule) match(principal, verb string) (bool, error) {
	byPrincipal, err := path.Match(r.Principal, principal)
	if err != nil {
		return false, err
	}
	byVerb, err := path.Match(r.Verb, verb)
	if err != nil {
		return false, err
	}
	return byPrincipal && byVerb, nil
}

// Hash identifies the policy that judged a decision. It is SHA-256 over the
// canonical JSON, so reformatting the file does not change it while any
// semantic edit does; the ledger stores the first 12 hex characters.
func (p *Policy) Hash() string {
	if p == nil {
		p = Default()
	}
	raw, err := json.Marshal(p)
	if err != nil {
		// Unreachable: two strings and an int.
		panic("darkfactory: encode policy: " + err.Error())
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}
