package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wintoplay/darkfactory/internal/policy"
	"github.com/Wintoplay/darkfactory/internal/store"
)

// The gateway is the one path a mutating verb takes: resolve the principal
// from the transport, decide, record the decision, then act (GV-4). M1 ships
// the seam, not the ledger: the recorder is a no-op until M2 supplies the
// append-only log, but the order is already fixed and a recorder failure
// leaves the effect unperformed.
//
// The two code-gated bootstrap routes, POST /api/v1/open and POST
// /api/v1/pair, deliberately do not pass through it: their authority is a
// one-time code, not a resolved principal, and they are the only routes that
// mint the first cookie. They still answer with typed refusals.

type principalKind string

const (
	principalHuman    principalKind = "human"
	principalOverseer principalKind = "overseer"
	principalWorker   principalKind = "worker"
)

// principal is resolved from the request transport, never from a body field
// (GV-1). The agent listener does not exist yet, so only human can arrive
// today; the other kinds keep the decision function honest for M2.
type principal struct {
	Kind principalKind
	ID   string
	// Local is stamped by the transport, never by a request field: the master
	// bearer token, or a cookie/session that arrived through the host's
	// loopback UI (loopback peer, loopback Host, no forwarding header). It is
	// the difference between workspace authority and the authority to mint or
	// revoke device credentials.
	Local bool
}

func principalFromRequest(r *http.Request) principal {
	info, _ := r.Context().Value(authInfoKey{}).(authInfo)
	switch info.kind {
	case authBearer:
		return principal{Kind: principalHuman, ID: "token", Local: true}
	case authCookie:
		return principal{Kind: principalHuman, ID: "device:" + info.deviceID, Local: isLocalUIRequest(r)}
	case authSession:
		return principal{Kind: principalHuman, ID: "device:" + info.deviceID, Local: isLocalUIRequest(r)}
	case authPane:
		return info.panePrincipal
	default:
		return principal{}
	}
}

// permissionClass is the transport-stamped tier a verb belongs to. It mirrors
// the Orca split without copying its size: today DarkFactory has one workspace
// grant, so the only extra tier is the credential boundary. Adding a class here
// is how a future narrower paired-device grant starts.
type permissionClass string

const (
	// permissionWorkspace is repo/worktree/session/run/OpenCode/files: it
	// already equals running commands as the daemon's user.
	permissionWorkspace permissionClass = "workspace"
	// permissionHostCredential mints or revokes device authority. A paired
	// device, even a full-scope one, does not get it; only the master token or
	// the host's loopback UI does.
	permissionHostCredential permissionClass = "host-credential"
)

// hostCredentialVerbs is deliberately a short, explicit list. Pairing a new
// device and revoking one are control-plane acts, not workspace acts, and the
// local web UI (the only UI that can call them) is already loopback-only.
// device.key.create is not here: a native client on another machine may
// register its own key after entering the master token.
var hostCredentialVerbs = map[string]permissionClass{
	verbPairCode:         permissionHostCredential,
	verbDeviceRevoke:     permissionHostCredential,
	verbDevicesRevokeAll: permissionHostCredential,
}

func verbPermission(verb string) permissionClass {
	if permission, ok := hostCredentialVerbs[verb]; ok {
		return permission
	}
	return permissionWorkspace
}

type decision struct {
	At        time.Time
	Principal principal
	Verb      string
	Target    string
	// Decision is LedgerAllow or LedgerDeny. Rule names the refusal rule when
	// the decision is a deny (GV-8), so the ledger quotes the reason instead of
	// a status code.
	Decision string
	Rule     string
	// Policy identifies the policy file that judged this row, so an audit can
	// say which rules were in force and when they changed (GV-6).
	Policy string
	// Outcome is empty on the row written before the effect, and carries what
	// the handler then did on the row written after it. An empty outcome on a
	// stored decision row means the daemon stopped between the two.
	Outcome string
	Status  int
}

type decisionRecorder interface {
	Record(decision) error
}

type nopRecorder struct{}

func (nopRecorder) Record(decision) error { return nil }

// ledgerRecorder is the real recorder: one hash-chained row per decision and
// one per outcome, in state.db (GV-6, GV-18).
type ledgerRecorder struct {
	store *store.Store
}

func (r ledgerRecorder) Record(event decision) error {
	_, err := r.store.AppendLedger(store.LedgerEntry{
		At:        event.At,
		Principal: principalLabel(event.Principal),
		Verb:      event.Verb,
		Target:    event.Target,
		Decision:  event.Decision,
		Rule:      event.Rule,
		Policy:    event.Policy,
		Outcome:   event.Outcome,
		Status:    event.Status,
	})
	return err
}

// principalLabel is the actor string the chain stores: kind and id, never a
// secret. An unresolved principal is refused before it reaches the ledger, so
// "unknown" only appears on a deny row that policy rejected first.
func principalLabel(p principal) string {
	if p.Kind == "" {
		return "unknown"
	}
	if p.ID == "" {
		return string(p.Kind)
	}
	return string(p.Kind) + ":" + p.ID
}

type gateway struct {
	recorder decisionRecorder
	policy   *policy.Policy
	log      *slog.Logger
}

// auditTargetKey carries a mutable holder into a handler, so a create can name
// the object it made. The decision row cannot know it: it is written before
// anything happens. The outcome row can.
type auditTargetKey struct{}

type auditTarget struct{ value string }

// withAuditTarget attaches a holder to the request the handler receives.
func withAuditTarget(r *http.Request) (*http.Request, *auditTarget) {
	holder := &auditTarget{}
	return r.WithContext(context.WithValue(r.Context(), auditTargetKey{}, holder)), holder
}

// setAuditTarget lets a handler record what it created, so a ledger row for
// `repo.create` names the repo instead of "request".
func setAuditTarget(r *http.Request, target string) {
	if holder, ok := r.Context().Value(auditTargetKey{}).(*auditTarget); ok {
		holder.value = target
	}
}

// Gateway verbs are stable audit names, not URLs: the ledger records them
// verbatim, so they change only by adding a new name.
const (
	verbOpenCode             = "open.code"
	verbLogout               = "logout"
	verbPairCode             = "pair.code"
	verbDeviceRevoke         = "device.revoke"
	verbDeviceKeyCreate      = "device.key.create"
	verbDevicesRevokeAll     = "devices.revoke_all"
	verbOpenCodeCreate       = "opencode.session.create"
	verbOpenCodePrompt       = "opencode.prompt"
	verbOpenCodeInterrupt    = "opencode.interrupt"
	verbOpenCodePermission   = "opencode.permission.reply"
	verbOpenCodeCommand      = "opencode.command"
	verbPickDirectory        = "fs.pick"
	verbDebugLog             = "debug.log"
	verbRepoCreate           = "repo.create"
	verbRepoOrder            = "repo.order"
	verbRepoDelete           = "repo.delete"
	verbWorktreeCreate       = "worktree.create"
	verbWorktreeDelete       = "worktree.delete"
	verbWorktreeOrder        = "worktree.order"
	verbRunCreate            = "run.create"
	verbRunStart             = "run.start"
	verbRunCancel            = "run.cancel"
	verbRunAnswer            = "run.answer"
	verbSessionCreate        = "session.create"
	verbSessionDelete        = "session.delete"
	verbSessionOrder         = "session.order"
	verbSessionPin           = "session.pin"
	verbSessionInput         = "session.input"
	verbSessionNative        = "session.native"
	verbSessionNativePane    = "session.native_pane"
	verbSessionResize        = "session.resize"
	verbSessionRedraw        = "session.redraw"
	verbWorktreeReveal       = "worktree.reveal"
	verbNotificationDismiss  = "notification.dismiss"
	verbNotificationClear    = "notification.clear"
	verbNotificationSettings = "notification.settings"
)

func newGateway(recorder decisionRecorder, par *policy.Policy, log *slog.Logger) *gateway {
	if recorder == nil {
		recorder = nopRecorder{}
	}
	if par == nil {
		par = policy.Default()
	}
	return &gateway{recorder: recorder, policy: par, log: log}
}

// do runs the fixed order. It calls act only after the decision row is
// durable, so a failing recorder cannot leave half a mutation behind, and it
// records the outcome after the handler ran, which is how a crash between the
// two shows up as `unknown` instead of a silent success (GV-20).
func (g *gateway) do(p principal, verb, target string, act func() int, resolved func() string) *refusal {
	if refusal := g.decide(p, verb); refusal != nil {
		// A refusal changed nothing, so failing to record it must not turn a
		// deny into a 500 — the caller still gets the reason it was refused.
		event := decision{At: time.Now().UTC(), Principal: p, Verb: verb, Target: target,
			Decision: store.LedgerDeny, Rule: refusal.Rule, Policy: g.policy.Hash()}
		_ = g.recorder.Record(event)
		return refusal
	}
	event := decision{At: time.Now().UTC(), Principal: p, Verb: verb, Target: target,
		Decision: store.LedgerAllow, Policy: g.policy.Hash()}
	if err := g.recorder.Record(event); err != nil {
		ref := refusalFor(codeInternalError, target, "")
		ref.Detail = "the decision could not be recorded, so nothing was changed"
		return &ref
	}
	status := act()
	if resolved != nil {
		if created := resolved(); created != "" {
			target = created
		}
	}
	event.Target = target
	event.Outcome = outcomeFor(status)
	event.Status = status
	if err := g.recorder.Record(event); err != nil {
		// The effect already happened and the response is already written, so
		// there is nothing to undo and nothing to report to this client. The
		// decision row stands with no outcome, which is what `audit` prints as
		// unknown; the log says why it is missing.
		g.logger().Warn("ledger outcome not recorded", "verb", verb, "target", target, "status", status, "error", err)
	}
	return nil
}

// outcomeFor maps the status the handler answered with onto the ledger's
// closed vocabulary. A handler that wrote no status left the implicit 200.
func outcomeFor(status int) string {
	switch {
	case status == 0:
		return store.LedgerApplied
	case status < 400:
		return store.LedgerApplied
	case status < 500:
		return store.LedgerRefused
	default:
		return store.LedgerFailed
	}
}

func (g *gateway) logger() *slog.Logger {
	if g.log != nil {
		return g.log
	}
	return slog.Default()
}

// decide answers the gateway's question in two layers. The human-only verbs
// (GV-8) come first, and no policy can open them to a worker or an overseer:
// that gate is structural, not data. Everything a human principal asks for is
// then judged by the policy file, deny before allow (GV-5), and no match is a
// deny.
func (g *gateway) decide(p principal, verb string) *refusal {
	switch p.Kind {
	case principalHuman:
	case principalWorker:
		ref := refusalFor(codeCapabilityScope, "", "")
		ref.Rule = "human_only"
		return &ref
	default:
		ref := refusalFor(codeNotAPrincipal, "", "")
		ref.Rule = "principal"
		return &ref
	}
	if verbPermission(verb) == permissionHostCredential && !p.Local {
		ref := refusalFor(codeHostAdmin, "", "")
		ref.Rule = "host_only"
		return &ref
	}
	verdict := g.policy.Decide(principalLabel(p), verb)
	if verdict.Allow {
		return nil
	}
	ref := refusalFor(codePolicyDenied, "", "")
	ref.Rule = verdict.Rule
	ref.Detail = "policy " + g.policy.Hash() + " does not allow " + verb
	return &ref
}

// mutate is the middleware form used by the route table. Every authenticated
// mutation carries a verb, so a route added without one is caught by
// TestEveryMutationGoesThroughGateway.
func (s *Server) mutate(verb string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := r.PathValue("id")
		if target == "" {
			target = "request"
		}
		r, created := withAuditTarget(r)
		recorder := &statusWriter{ResponseWriter: w}
		if refusal := s.gateway.do(principalFromRequest(r), verb, target, func() int {
			next(recorder, r)
			return recorder.status
		}, func() string { return created.value }); refusal != nil {
			s.writeRefusal(w, *refusal)
		}
	}
}

// statusWriter remembers what the handler answered so the caller can record it,
// and forwards the optional interfaces the handlers may still reach for.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
