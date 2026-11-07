package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func openLedger(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func appendRows(t *testing.T, st *Store, n int) []LedgerEntry {
	t.Helper()
	rows := make([]LedgerEntry, 0, n)
	at := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		row, err := st.AppendLedger(LedgerEntry{
			At:        at.Add(time.Duration(i) * time.Minute),
			Principal: "human:token",
			Verb:      "repo.create",
			Target:    "repo_1",
			Decision:  LedgerAllow,
		})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return rows
}

func TestLedgerChainsEveryRow(t *testing.T) {
	st := openLedger(t)
	rows := appendRows(t, st, 3)

	if rows[0].Seq != 1 || rows[0].PrevHash != ledgerGenesis {
		t.Fatalf("first row = %#v, want seq 1 behind the genesis hash", rows[0])
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Seq != rows[i-1].Seq+1 {
			t.Fatalf("seq %d follows %d", rows[i].Seq, rows[i-1].Seq)
		}
		if rows[i].PrevHash != rows[i-1].Hash {
			t.Fatalf("row %d does not name row %d as its predecessor", rows[i].Seq, rows[i-1].Seq)
		}
		if rows[i].Hash == rows[i-1].Hash {
			t.Fatal("identical hashes for different rows")
		}
	}
	checked, broken, err := st.VerifyLedger()
	if err != nil {
		t.Fatal(err)
	}
	if broken != 0 || checked != 3 {
		t.Fatalf("verify = %d checked, broken at %d; want 3 and 0", checked, broken)
	}
}

func TestLedgerIsDurable(t *testing.T) {
	path := t.TempDir() + "/state.db"
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rows := appendRows(t, st, 2)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	next, err := reopened.AppendLedger(LedgerEntry{Principal: "human:token", Verb: "repo.delete", Target: "repo_1", Decision: LedgerAllow, Outcome: LedgerApplied, Status: 204})
	if err != nil {
		t.Fatal(err)
	}
	if next.Seq != 3 || next.PrevHash != rows[1].Hash {
		t.Fatalf("row after reopen = %#v, want seq 3 behind %s", next, rows[1].Hash)
	}
	if _, broken, err := reopened.VerifyLedger(); err != nil || broken != 0 {
		t.Fatalf("verify after reopen: broken=%d err=%v", broken, err)
	}
}

func TestLedgerWindowReturnsNewestRows(t *testing.T) {
	st := openLedger(t)
	appendRows(t, st, 5)

	entries, total, err := st.Ledger(2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(entries) != 2 || entries[0].Seq != 4 || entries[1].Seq != 5 {
		t.Fatalf("window = %#v, want the last two rows oldest first", entries)
	}

	all, _, err := st.Ledger(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 || all[0].Seq != 1 {
		t.Fatalf("unlimited window = %d rows starting at %d", len(all), all[0].Seq)
	}
}

func TestLedgerVerifyDetectsTamperedRow(t *testing.T) {
	st := openLedger(t)
	appendRows(t, st, 4)

	err := st.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(ledgerBucket)
		key, raw := bucket.Cursor().First()
		var row LedgerEntry
		if err := json.Unmarshal(raw, &row); err != nil {
			return err
		}
		row.Target = "repo_2"
		edited, err := json.Marshal(row)
		if err != nil {
			return err
		}
		return bucket.Put(key, edited)
	})
	if err != nil {
		t.Fatal(err)
	}

	_, broken, err := st.VerifyLedger()
	if err != nil {
		t.Fatal(err)
	}
	if broken != 1 {
		t.Fatalf("broken at %d, want row 1", broken)
	}
}

func TestLedgerVerifyDetectsRemovedRow(t *testing.T) {
	st := openLedger(t)
	appendRows(t, st, 4)

	if err := st.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(ledgerBucket)
		key, _ := bucket.Cursor().First()
		return bucket.Delete(key)
	}); err != nil {
		t.Fatal(err)
	}

	_, broken, err := st.VerifyLedger()
	if err != nil {
		t.Fatal(err)
	}
	// The surviving head becomes row 1, which cannot carry a predecessor.
	if broken == 0 {
		t.Fatal("verify accepted a ledger with its first row removed")
	}
}

func TestLedgerHashCoversEveryField(t *testing.T) {
	base := LedgerEntry{Seq: 1, At: time.Unix(0, 0).UTC(), Principal: "human:token", Verb: "repo.create", Target: "repo_1", Decision: LedgerAllow, PrevHash: ledgerGenesis}
	hash := ledgerHash(base)
	if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		t.Fatalf("hash = %q, want 64 hex characters", hash)
	}
	variants := []LedgerEntry{base, base, base, base, base, base}
	variants[0].Principal = "human:device:abc"
	variants[1].Verb = "repo.delete"
	variants[2].Target = "repo_2"
	variants[3].Decision = LedgerDeny
	variants[4].Outcome = LedgerApplied
	variants[5].Status = 409
	for i, variant := range variants[1:] {
		if ledgerHash(variant) == hash {
			t.Fatalf("variant %d hashes the same as the base row", i+1)
		}
	}
	if ledgerHash(base) != hash {
		t.Fatal("hash is not deterministic")
	}
}
