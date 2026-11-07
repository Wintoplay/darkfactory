package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Wintoplay/darkfactory/internal/store"
)

// handleAudit answers the decision ledger, oldest row first (GV-6, GV-18). It
// is a read, so it carries no gateway verb, and it is open to a paired device:
// the same human is allowed to read what happened.
//
// Verification runs on every read. It costs one SHA-256 per row over a local
// bucket, and a chain nobody rechecked is not evidence of anything.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := auditDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > auditMaxLimit {
			s.refuseTarget(w, r, codeInvalidRequest, "limit", "limit must be between 1 and "+strconv.Itoa(auditMaxLimit))
			return
		}
		limit = n
	}
	entries, total, err := s.store.Ledger(limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	checked, broken, err := s.store.VerifyLedger()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := auditResponse{
		Entries:       make([]auditEntry, 0, len(entries)),
		Total:         total,
		Limit:         limit,
		Truncated:     total > len(entries),
		RetentionDays: s.cfg.LedgerRetentionDays,
		Chain:         auditChain{OK: broken == 0, Checked: checked, BadSeq: broken},
	}
	if broken != 0 {
		out.Chain.Error = fmt.Sprintf("row %d does not match the chain; every row after it is suspect", broken)
	}
	for _, entry := range entries {
		out.Entries = append(out.Entries, auditEntry{
			Seq:       entry.Seq,
			At:        entry.At,
			Principal: entry.Principal,
			Verb:      entry.Verb,
			Target:    entry.Target,
			Decision:  entry.Decision,
			Rule:      entry.Rule,
			Policy:    entry.Policy,
			Outcome:   entry.Outcome,
			Status:    entry.Status,
			Pruned:    entry.Marker,
			Hash:      entry.Hash,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

const (
	auditDefaultLimit = 100
	auditMaxLimit     = 1000
)

type auditResponse struct {
	Entries []auditEntry `json:"entries"`
	Total   int          `json:"total"`
	Limit   int          `json:"limit"`
	// Truncated says the window is the newest rows and older ones are not in
	// the body; the CLI prints it so a reader never mistakes 100 for all.
	Truncated bool       `json:"truncated"`
	Chain     auditChain `json:"chain"`
	// RetentionDays is the window a prune keeps; 0 means keep everything.
	RetentionDays int `json:"retentionDays"`
}

type auditChain struct {
	OK      bool   `json:"ok"`
	Checked int    `json:"checked"`
	BadSeq  uint64 `json:"badSeq"`
	// Error is the human explanation of a broken chain, empty when OK.
	Error string `json:"error,omitempty"`
}

type auditEntry struct {
	Seq       uint64    `json:"seq"`
	At        time.Time `json:"at"`
	Principal string    `json:"principal"`
	Verb      string    `json:"verb"`
	Target    string    `json:"target"`
	Decision  string    `json:"decision"`
	Rule      string    `json:"rule,omitempty"`
	Policy    string    `json:"policy,omitempty"`
	Outcome   string    `json:"outcome,omitempty"`
	Status    int       `json:"status,omitempty"`
	// Pruned is set on a prune row: which rows went and where the surviving
	// chain continues from.
	Pruned *store.PruneMarker `json:"pruned,omitempty"`
	Hash   string             `json:"hash"`
}
