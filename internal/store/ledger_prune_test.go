package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// TestLedgerHashIgnoresAbsentFields pins the rule in LedgerEntry's comment: a
// field appended with omitempty does not rehash rows written before it existed,
// so a chain survives a schema addition instead of needing a rewrite.
func TestLedgerHashIgnoresAbsentFields(t *testing.T) {
	at := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	entry := LedgerEntry{
		Seq: 1, At: at, Principal: "human:token", Verb: "repo.create",
		Target: "repo_1", Decision: LedgerAllow, PrevHash: ledgerGenesis,
	}
	// The canonical JSON as this build wrote it before Policy and Marker
	// existed: every field of that row, in declaration order.
	before := `{"seq":1,"at":"2026-10-31T09:00:00Z","principal":"human:token","verb":"repo.create","target":"repo_1","decision":"allow","prevHash":"` +
		ledgerGenesis + `","hash":""}`
	sum := sha256.Sum256([]byte(before))
	if got, want := ledgerHash(entry), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("row written before the fields were added hashes to %s, want %s", got, want)
	}

	entry.Policy = "abc123"
	if ledgerHash(entry) == ledgerHash(LedgerEntry{Seq: 1, At: at, Principal: "human:token", Verb: "repo.create", Target: "repo_1", Decision: LedgerAllow, PrevHash: ledgerGenesis}) {
		t.Fatal("a row with a policy hashes like one without")
	}
}

func TestPruneLeavesAVerifiableChain(t *testing.T) {
	st := openLedger(t)
	old := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		if _, err := st.AppendLedger(LedgerEntry{At: old.Add(time.Duration(i) * time.Minute), Principal: "human:token", Verb: "repo.create", Target: "repo_1", Decision: LedgerAllow}); err != nil {
			t.Fatal(err)
		}
	}
	kept := make([]LedgerEntry, 0, 3)
	for i := 0; i < 3; i++ {
		row, err := st.AppendLedger(LedgerEntry{At: base.Add(time.Duration(i) * time.Minute), Principal: "human:token", Verb: "repo.delete", Target: "repo_1", Decision: LedgerAllow})
		if err != nil {
			t.Fatal(err)
		}
		kept = append(kept, row)
	}

	marker, err := st.PruneLedger(base)
	if err != nil {
		t.Fatal(err)
	}
	if marker == nil || marker.Marker == nil {
		t.Fatal("prune wrote no marker")
	}
	if marker.Marker.Dropped != 4 || marker.Marker.ThroughSeq != 4 || marker.Marker.ThroughHash == "" {
		t.Fatalf("marker = %#v, want 4 rows through seq 4", marker.Marker)
	}

	rows, _, err := st.Ledger(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 { // three kept rows plus the marker
		t.Fatalf("rows after prune = %d, want 4", len(rows))
	}
	if rows[0].Seq != 5 {
		t.Fatalf("first surviving row = %d, want 5", rows[0].Seq)
	}
	if rows[0].PrevHash != marker.Marker.ThroughHash {
		t.Fatal("the surviving row does not link to the marker's declared hash")
	}
	if rows[len(rows)-1].Marker == nil {
		t.Fatal("the marker is not the newest row")
	}
	if checked, broken, err := st.VerifyLedger(); err != nil || broken != 0 {
		t.Fatalf("verify after prune: checked=%d broken=%d err=%v", checked, broken, err)
	}
}

func TestDeletionWithoutAMarkerBreaksTheChain(t *testing.T) {
	st := openLedger(t)
	appendRows(t, st, 4)
	if err := st.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(ledgerBucket)
		key, _ := bucket.Cursor().First()
		return bucket.Delete(key)
	}); err != nil {
		t.Fatal(err)
	}
	if _, broken, err := st.VerifyLedger(); err != nil || broken == 0 {
		t.Fatalf("verify = broken %d err %v, want a break", broken, err)
	}
}

func TestPruneTwiceKeepsOneAnchor(t *testing.T) {
	st := openLedger(t)
	early := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	late := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{early, early.Add(time.Hour), early.Add(2 * time.Hour), mid, mid.Add(time.Hour), late} {
		if _, err := st.AppendLedger(LedgerEntry{At: at, Principal: "human:token", Verb: "repo.create", Decision: LedgerAllow}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := st.PruneLedger(mid); err != nil {
		t.Fatal(err)
	}
	second, err := st.PruneLedger(late)
	if err != nil {
		t.Fatal(err)
	}
	// The first prune dropped 1..3 and left its marker at seq 7; the second
	// drops the two mid rows (4, 5) and stops at the row that is not old enough.
	if second == nil || second.Marker.Dropped != 2 || second.Marker.ThroughSeq != 5 {
		t.Fatalf("second prune = %#v, want 2 rows through seq 5", second.Marker)
	}
	rows, _, err := st.Ledger(0)
	if err != nil {
		t.Fatal(err)
	}
	anchor := rows[len(rows)-1]
	if anchor.Marker == nil || rows[0].PrevHash != anchor.Marker.ThroughHash {
		t.Fatalf("the surviving chain does not start at the newest marker: first row prev %s, marker through %v", rows[0].PrevHash, anchor.Marker)
	}
	if checked, broken, err := st.VerifyLedger(); err != nil || broken != 0 {
		t.Fatalf("verify after two prunes: checked=%d broken=%d err=%v", checked, broken, err)
	}
}

func TestPruneEverythingStillVerifies(t *testing.T) {
	st := openLedger(t)
	appendRows(t, st, 3)
	marker, err := st.PruneLedger(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if marker == nil {
		t.Fatal("prune wrote no marker")
	}
	rows, total, err := st.Ledger(0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].Marker == nil {
		t.Fatalf("rows after pruning everything = %d (total %d), want the marker alone", len(rows), total)
	}
	if checked, broken, err := st.VerifyLedger(); err != nil || broken != 0 {
		t.Fatalf("verify with only a marker: checked=%d broken=%d err=%v", checked, broken, err)
	}
}

func TestPruneKeepsRowsThatAreNotOldEnough(t *testing.T) {
	st := openLedger(t)
	appendRows(t, st, 2)
	// The fixture rows are dated 2026-10-31; this cutoff is earlier than them.
	marker, err := st.PruneLedger(time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if marker != nil {
		t.Fatalf("prune deleted rows that are inside the window: %#v", marker)
	}
	if _, total, err := st.Ledger(0); err != nil || total != 2 {
		t.Fatalf("total = %d err = %v, want 2", total, err)
	}
}

// TestPruneMarkerSurvivesEncoding keeps the receipt readable after a restart.
func TestPruneMarkerSurvivesEncoding(t *testing.T) {
	st := openLedger(t)
	appendRows(t, st, 2)
	marker, err := st.PruneLedger(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := st.Ledger(0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	var back LedgerEntry
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatal(err)
	}
	if back.Marker == nil || back.Marker.ThroughHash != marker.Marker.ThroughHash {
		t.Fatalf("marker round-trip = %#v, want %#v", back.Marker, marker.Marker)
	}
}
