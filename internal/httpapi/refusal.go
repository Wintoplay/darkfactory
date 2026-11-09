package httpapi

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Wintoplay/darkfactory/internal/store"
)

// Refusal codes are part of the HTTP contract (GV-7, SC-5): clients branch on
// the code, not on prose, so a code is renamed only by adding a new one. The
// table below is pinned by TestRefusalCodesAreStable; changing a status or
// dropping a code fails that test on purpose.
const (
	codeInvalidRequest            = "invalid_request"
	codeInvalidID                 = "invalid_id"
	codeInvalidPath               = "invalid_path"
	codeNotADirectory             = "not_a_directory"
	codeNotFound                  = "not_found"
	codeInternalError             = "internal_error"
	codeUnauthorized              = "unauthorized"
	codeOriginDenied              = "origin_denied"
	codeSourceDenied              = "source_denied"
	codeHostAdmin                 = "host_admin"
	codeReadOnly                  = "read_only"
	codeTerminalRequired          = "terminal_required"
	codeNativeTerminalRemote      = "native_terminal_remote"
	codeNativeTerminalUnavailable = "native_terminal_unavailable"
	codeNativeTerminalFailed      = "native_terminal_failed"
	codeNativePaneScope           = "native_pane_scope"
	codeNotAPrincipal             = "not_a_principal"
	codeCapabilityScope           = "capability_scope"
	codePolicyDenied              = "policy_denied"
	codeOpenHostOnly              = "open_host_only"
	codeOpenInvalid               = "open_invalid"
	codePairHTTPSOnly             = "pair_https_only"
	codePairRateLimited           = "pair_rate_limited"
	codePairInvalid               = "pair_invalid"
	codeRepoExists                = "repo_exists"
	codeRepoNotFound              = "repo_not_found"
	codeRepoOrderInvalid          = "repo_order_invalid"
	codeRepoBusy                  = "repo_busy"
	codeRepoNameUnsafe            = "repo_name_unsafe"
	codeWorkspaceBusy             = "workspace_busy"
	codeWorkspaceTied             = "workspace_tied_to_repo"
	codeWorktreeNotFound          = "worktree_not_found"
	codeWorktreeBusy              = "worktree_busy"
	codeWorktreeDirty             = "worktree_dirty"
	codeWorktreeRequired          = "worktree_required"
	codeWorktreeCreateFailed      = "worktree_create_failed"
	codeWorktreeOrderInvalid      = "worktree_order_invalid"
	codeWorktreeRemoveFailed      = "worktree_remove_failed"
	codeRevealRemote              = "reveal_remote"
	codeRevealUnavailable         = "reveal_unavailable"
	codeRevealFailed              = "reveal_failed"
	codeStoreUnavailable          = "store_unavailable"
	codeRunNotFound               = "run_not_found"
	codeRunTerminal               = "run_terminal"
	codeStepNotWaiting            = "step_not_waiting"
	codeInvalidPlan               = "invalid_plan"
	codeInvalidStep               = "invalid_step"
	codeSessionNotFound           = "session_not_found"
	codeSessionCreateFailed       = "session_create_failed"
	codeSessionOrderInvalid       = "session_order_invalid"
	codeTerminalWriteFailed       = "terminal_write_failed"
	codeInvalidResize             = "invalid_resize"
	codeStreamingUnsupported      = "streaming_unsupported"
	codeUnknownAgent              = "unknown_agent"
	codeDeviceNotFound            = "device_not_found"
	codeDeviceRateLimited         = "device_rate_limited"
	codeInvalidScope              = "invalid_scope"
	codePickerRemote              = "picker_remote"
	codePickerUnavailable         = "picker_unavailable"
	codePickerFailed              = "picker_failed"
	codeOpenCodeDisabled          = "opencode_disabled"
	codeOpenCodeUnavailable       = "opencode_unavailable"
	codeOpenCodeTooLarge          = "opencode_response_too_large"
	codeOpenCodeBadRequest        = "opencode_bad_request"
	codeOpenCodeNotFound          = "opencode_not_found"
	codeOpenCodeConflict          = "opencode_conflict"
	codeOpenCodeRateLimited       = "opencode_rate_limited"
	codeOpenCodeFailed            = "opencode_failed"
	codeUnknownLocation           = "unknown_location"
	codeRequestBodyTooLarge       = "request_too_large"
	codeMethodNotAllowed          = "method_not_allowed"
	codeE2EEUnavailable           = "e2ee_unavailable"
	codeE2EERateLimited           = "e2ee_rate_limited"
	codeE2EESuiteDowngrade        = "e2ee_suite_downgrade"
)

// refusalSpec is the immutable half of a refusal: the status a code maps to
// and the advice a client may show. A code with no hint is not a user mistake.
type refusalSpec struct {
	status     int
	remediable bool
	hint       string
}

var refusalSpecs = map[string]refusalSpec{
	codeInvalidRequest:            {http.StatusBadRequest, true, "send a JSON body that matches the route's fields"},
	codeInvalidID:                 {http.StatusBadRequest, true, "use the id returned when the object was created"},
	codeInvalidPath:               {http.StatusBadRequest, true, "use an absolute path"},
	codeNotADirectory:             {http.StatusBadRequest, true, "choose a directory"},
	codeNotFound:                  {http.StatusNotFound, false, ""},
	codeInternalError:             {http.StatusInternalServerError, false, ""},
	codeUnauthorized:              {http.StatusUnauthorized, true, "send the bearer token or pair this device"},
	codeOriginDenied:              {http.StatusForbidden, true, "use the daemon's own origin and send X-DF-Request: 1 on mutations"},
	codeSourceDenied:              {http.StatusForbidden, false, ""},
	codeHostAdmin:                 {http.StatusForbidden, true, "use the daemon's local UI or the master token for this action"},
	codeReadOnly:                  {http.StatusForbidden, true, "pair a device with full scope for this action"},
	codeTerminalRequired:          {http.StatusForbidden, true, "pair a device with terminal access"},
	codeNativeTerminalRemote:      {http.StatusForbidden, true, "open the native terminal on the daemon host"},
	codeNativeTerminalUnavailable: {http.StatusConflict, true, "install Ghostty and use a graphical Linux session"},
	codeNativeTerminalFailed:      {http.StatusInternalServerError, false, ""},
	codeNativePaneScope:           {http.StatusForbidden, true, "request a new terminal pane from the local UI"},
	codeNotAPrincipal:             {http.StatusForbidden, false, ""},
	codeCapabilityScope:           {http.StatusForbidden, false, "a worker capability cannot use a human-only verb"},
	codePolicyDenied:              {http.StatusForbidden, false, "the policy file does not allow this verb for this principal"},
	codeOpenHostOnly:              {http.StatusForbidden, true, "run darkfactory open on the daemon host"},
	codeOpenInvalid:               {http.StatusUnauthorized, true, "run darkfactory open again"},
	codePairHTTPSOnly:             {http.StatusForbidden, true, "use HTTPS (Tailscale Serve or an SSH tunnel) for remote pairing"},
	codePairRateLimited:           {http.StatusTooManyRequests, true, "wait a minute and try again"},
	codePairInvalid:               {http.StatusUnauthorized, true, "ask the host for a new pairing code"},
	codeRepoExists:                {http.StatusConflict, true, "use the existing repo row"},
	codeRepoNotFound:              {http.StatusNotFound, false, ""},
	codeRepoOrderInvalid:          {http.StatusBadRequest, true, "send the repo ids in the order you want, without duplicates"},
	codeRepoBusy:                  {http.StatusConflict, true, "remove its git worktrees first"},
	codeRepoNameUnsafe:            {http.StatusConflict, true, "rename the repo directory to a path-safe name"},
	codeWorkspaceBusy:             {http.StatusConflict, true, "use the existing workspace"},
	codeWorkspaceTied:             {http.StatusConflict, true, "remove the repo instead; the files stay on disk"},
	codeWorktreeNotFound:          {http.StatusNotFound, false, ""},
	codeWorktreeBusy:              {http.StatusConflict, true, "retry with force=1 to stop the live sessions"},
	codeWorktreeDirty:             {http.StatusConflict, true, "commit or stash the changes, or retry with force=1"},
	codeWorktreeRequired:          {http.StatusBadRequest, true, "choose a worktree or workspace"},
	codeWorktreeCreateFailed:      {http.StatusConflict, false, "check the base ref and branch name"},
	codeWorktreeOrderInvalid:      {http.StatusBadRequest, true, "send the worktree ids of one repo, without duplicates"},
	codeWorktreeRemoveFailed:      {http.StatusConflict, false, ""},
	codeRevealRemote:              {http.StatusConflict, true, "reveal the folder on the daemon's machine"},
	codeRevealUnavailable:         {http.StatusConflict, true, "open the folder from the daemon host"},
	codeRevealFailed:              {http.StatusInternalServerError, false, ""},
	codeStoreUnavailable:          {http.StatusServiceUnavailable, false, ""},
	codeRunNotFound:               {http.StatusNotFound, false, ""},
	codeRunTerminal:               {http.StatusConflict, false, "start a new run"},
	codeStepNotWaiting:            {http.StatusConflict, false, ""},
	codeInvalidPlan:               {http.StatusBadRequest, true, "send at least one command, exec or pty step"},
	codeInvalidStep:               {http.StatusBadRequest, true, "use the ordinal from the run timeline"},
	codeSessionNotFound:           {http.StatusNotFound, false, ""},
	codeSessionCreateFailed:       {http.StatusConflict, false, "check the worktree directory and the agent preset"},
	codeSessionOrderInvalid:       {http.StatusBadRequest, true, "send the session ids of one workspace, without duplicates"},
	codeTerminalWriteFailed:       {http.StatusConflict, false, ""},
	codeInvalidResize:             {http.StatusBadRequest, true, "send positive cols and rows"},
	codeStreamingUnsupported:      {http.StatusInternalServerError, false, ""},
	codeUnknownAgent:              {http.StatusBadRequest, true, "choose an agent from the configured presets"},
	codeDeviceNotFound:            {http.StatusNotFound, false, ""},
	codeDeviceRateLimited:         {http.StatusTooManyRequests, true, "wait a minute and try again"},
	codeInvalidScope:              {http.StatusBadRequest, true, "use full or read"},
	codePickerRemote:              {http.StatusConflict, true, "use the folder browser"},
	codePickerUnavailable:         {http.StatusConflict, true, "use the folder browser"},
	codePickerFailed:              {http.StatusInternalServerError, false, ""},
	codeOpenCodeDisabled:          {http.StatusServiceUnavailable, true, "configure opencodeAPIURL and opencodePasswordFile"},
	codeOpenCodeUnavailable:       {http.StatusBadGateway, false, ""},
	codeOpenCodeTooLarge:          {http.StatusBadGateway, false, ""},
	codeOpenCodeBadRequest:        {http.StatusBadRequest, true, "check the fields OpenCode rejected"},
	codeOpenCodeNotFound:          {http.StatusNotFound, false, ""},
	codeOpenCodeConflict:          {http.StatusConflict, false, ""},
	codeOpenCodeRateLimited:       {http.StatusTooManyRequests, true, "wait and retry"},
	codeOpenCodeFailed:            {http.StatusBadGateway, false, ""},
	codeUnknownLocation:           {http.StatusForbidden, true, "choose one of this daemon's worktrees"},
	codeRequestBodyTooLarge:       {http.StatusRequestEntityTooLarge, true, "send a smaller body"},
	codeMethodNotAllowed:          {http.StatusMethodNotAllowed, true, "use one of the methods in the Allow header"},
	codeE2EEUnavailable:           {http.StatusServiceUnavailable, false, ""},
	codeE2EERateLimited:           {http.StatusTooManyRequests, true, "wait a moment and retry"},
	codeE2EESuiteDowngrade:        {http.StatusForbidden, false, "this host requires a newer app version; update and re-pair"},
}

// refusal is a failure before it is encoded. Route handlers and the gateway
// build one; only writeRefusal knows the wire shape.
type refusal struct {
	Status     int
	Code       string
	Rule       string
	Target     string
	Detail     string
	Remediable bool
	Hint       string
}

// refusalEnvelope is GV-7's shape. `remediable` is always present so a client
// can decide whether to offer a retry without guessing from the code.
type refusalEnvelope struct {
	Code       string `json:"code"`
	Rule       string `json:"rule,omitempty"`
	Target     string `json:"target,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Remediable bool   `json:"remediable"`
	Hint       string `json:"hint,omitempty"`
}

// refusalFor resolves a code against the registry. An unknown code is a
// programming error: it becomes a generic 500 with no detail, never a leak.
func refusalFor(code, target, detail string) refusal {
	spec, ok := refusalSpecs[code]
	if !ok {
		spec = refusalSpecs[codeInternalError]
		code = codeInternalError
		detail = ""
	}
	return refusal{
		Status:     spec.status,
		Code:       code,
		Target:     target,
		Detail:     clipDetail(detail),
		Remediable: spec.remediable,
		Hint:       spec.hint,
	}
}

// clipDetail bounds what an upstream or a decoder can put on the wire. Details
// are for the caller, not a log sink; every handler passes a value it chose.
func clipDetail(value string) string {
	value = strings.TrimSpace(value)
	const limit = 1000
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

// refuse writes a code with the path's id as the target. Most refusals are
// about the object in the URL; a handler that knows a better target uses
// refuseTarget.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, code, detail string) {
	s.writeRefusal(w, refusalFor(code, r.PathValue("id"), detail))
}

func (s *Server) refuseTarget(w http.ResponseWriter, r *http.Request, code, target, detail string) {
	s.writeRefusal(w, refusalFor(code, target, detail))
}

func (s *Server) writeRefusal(w http.ResponseWriter, ref refusal) {
	if ref.Status < 400 || ref.Status > 599 {
		ref.Status = http.StatusInternalServerError
		ref.Code = codeInternalError
		ref.Detail = ""
	}
	writeJSON(w, ref.Status, refusalEnvelope{
		Code:       ref.Code,
		Rule:       ref.Rule,
		Target:     ref.Target,
		Detail:     ref.Detail,
		Remediable: ref.Remediable,
		Hint:       ref.Hint,
	})
}

// fail is the last resort for a store or process error. The detail stays in
// the log: a 5xx body says only that it failed.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		s.writeRefusal(w, refusalFor(codeNotFound, r.PathValue("id"), ""))
		return
	}
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	s.writeRefusal(w, refusalFor(codeInternalError, "", ""))
}

// refusalCodeList is the pin for TestRefusalCodesAreStable. It is generated
// from the registry, never hand-edited; the test compares the full code/status
// set so a rename or a status change cannot slip through review.
func refusalCodeList() []string {
	codes := make([]string, 0, len(refusalSpecs))
	for code := range refusalSpecs {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}
