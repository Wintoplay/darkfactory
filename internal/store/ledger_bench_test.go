package store

import (
	"testing"
)

// BenchmarkAppendLedger is the guard on the hot path: INPUT writes one decision
// per request, so a keystroke costs two of these. If the number here climbs
// past a few hundred microseconds, the ledger needs batching before it can
// stay inline with the terminal.
func BenchmarkAppendLedger(b *testing.B) {
	st, err := Open(b.TempDir() + "/state.db")
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	entry := LedgerEntry{Principal: "human:token", Verb: "session.input", Target: "sess_1", Decision: LedgerAllow, Outcome: LedgerApplied, Status: 204}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.AppendLedger(entry); err != nil {
			b.Fatal(err)
		}
	}
}
