package store

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

// The ledger is the append-only record of what the daemon decided and what
// followed (GV-6, GV-18). It is one bbolt bucket keyed by the row's sequence
// number, and each row carries the hash of the row before it, so an edit in
// the middle of the history invalidates every row after it.
//
// The chain is tamper-evident against the API, not against the uid: a same-uid
// process can rewrite state.db and recompute the hashes. Containment (`jail`)
// is what makes the claim stronger; the ledger only proves that nobody edited
// it through a route.
//
// A decision is two rows, not one: "allow"/"deny" is written before the effect
// so a crash cannot leave an unrecorded action, and the outcome row is written
// after it. A row whose outcome is empty therefore means the daemon stopped
// between the two, which is exactly the `unknown` GV-20 asks for.

var ledgerBucket = []byte("ledger")

// Ledger decisions and outcomes are the closed vocabulary the chain stores and
// the CLI prints. A new value is a schema change, not a string edit.
const (
	LedgerAllow = "allow"
	LedgerDeny  = "deny"

	LedgerApplied = "applied"
	LedgerRefused = "refused"
	LedgerFailed  = "failed"
)

// ledgerGenesis is the `prev_hash` of row 1. Sixty-four zeros rather than an
// empty string, so every row has a predecessor of the same shape and the CLI
// can print the field without a special case.
const ledgerGenesis = "0000000000000000000000000000000000000000000000000000000000000000"

// LedgerEntry is one row. The JSON field order is the hash input: append a new
// field at the end and tag it omitempty, so a row written before the field
// existed still re-encodes byte for byte and keeps verifying. A field that is
// never empty and added later does rehash history, and that is a new chain.
type LedgerEntry struct {
	Seq       uint64    `json:"seq"`
	At        time.Time `json:"at"`
	Principal string    `json:"principal"`
	Verb      string    `json:"verb"`
	Target    string    `json:"target"`
	Decision  string    `json:"decision"`
	Rule      string    `json:"rule,omitempty"`
	// Policy is the hash of the policy file that judged this row, so an audit
	// can say which rules were in force (GV-6).
	Policy  string `json:"policy,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	Status  int    `json:"status,omitempty"`
	// Marker is set only on a prune row: it says which rows were dropped and
	// which hash the surviving chain continues from. Retention has to delete
	// history; the marker is what keeps the deletion visible and checkable.
	Marker   *PruneMarker `json:"marker,omitempty"`
	PrevHash string       `json:"prevHash"`
	Hash     string       `json:"hash"`
}

// PruneMarker is the receipt a prune leaves behind. ThroughSeq and ThroughHash
// name the last row that was deleted, so the first surviving row's prevHash can
// be checked against it, and Dropped says how many rows went.
type PruneMarker struct {
	ThroughSeq  uint64 `json:"throughSeq"`
	ThroughHash string `json:"throughHash"`
	Dropped     int    `json:"dropped"`
}

// AppendLedger writes one row and returns it with its sequence and hash filled
// in. Sequence and predecessor are read inside the same transaction as the
// write, so two writers cannot fork the chain.
func (s *Store) AppendLedger(entry LedgerEntry) (LedgerEntry, error) {
	if s == nil || s.db == nil {
		return LedgerEntry{}, errors.New("no store")
	}
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	entry.At = entry.At.UTC()
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(ledgerBucket)
		if bucket == nil {
			return errors.New("ledger bucket missing")
		}
		entry.Seq = 1
		entry.PrevHash = ledgerGenesis
		if key, raw := bucket.Cursor().Last(); key != nil {
			var prev LedgerEntry
			if err := json.Unmarshal(raw, &prev); err != nil {
				return fmt.Errorf("read ledger head: %w", err)
			}
			entry.Seq = prev.Seq + 1
			entry.PrevHash = prev.Hash
		}
		entry.Hash = ledgerHash(entry)
		raw, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		return bucket.Put(ledgerKey(entry.Seq), raw)
	})
	return entry, err
}

// Ledger returns the newest rows, oldest first, plus the total row count so a
// caller can say what it left out. A limit of zero or less returns everything.
func (s *Store) Ledger(limit int) ([]LedgerEntry, int, error) {
	if s == nil || s.db == nil {
		return nil, 0, errors.New("no store")
	}
	var entries []LedgerEntry
	total := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(ledgerBucket)
		if bucket == nil {
			return errors.New("ledger bucket missing")
		}
		total = bucket.Stats().KeyN
		cursor := bucket.Cursor()
		for key, raw := cursor.Last(); key != nil && (limit <= 0 || len(entries) < limit); key, raw = cursor.Prev() {
			var entry LedgerEntry
			if err := json.Unmarshal(raw, &entry); err != nil {
				return fmt.Errorf("read ledger row: %w", err)
			}
			entries = append(entries, entry)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries, total, nil
}

// VerifyLedger recomputes the chain and reports how many rows it checked and
// the sequence of the first row that does not verify, 0 when the chain is
// intact. A row is bad when its hash does not match its contents, when its
// predecessor is not the previous row, or when the sequence has a hole.
//
// After a prune the first surviving row has no predecessor left to read, so the
// newest PruneMarker declares what it must link to. A deletion without a marker
// is therefore a broken chain, which is the point of writing one.
func (s *Store) VerifyLedger() (int, uint64, error) {
	if s == nil || s.db == nil {
		return 0, 0, errors.New("no store")
	}
	checked := 0
	var broken uint64
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(ledgerBucket)
		if bucket == nil {
			return errors.New("ledger bucket missing")
		}
		anchor := ledgerGenesis
		if key, raw := bucket.Cursor().Last(); key != nil {
			var head LedgerEntry
			if err := json.Unmarshal(raw, &head); err != nil {
				return fmt.Errorf("read ledger head: %w", err)
			}
			if head.Marker != nil {
				anchor = head.Marker.ThroughHash
			}
		}
		want := uint64(0) // taken from the first surviving row
		previous := anchor
		return bucket.ForEach(func(_, raw []byte) error {
			var entry LedgerEntry
			if err := json.Unmarshal(raw, &entry); err != nil {
				broken = want
				if broken == 0 {
					broken = 1
				}
				return fmt.Errorf("read ledger row: %w", err)
			}
			if want == 0 {
				want = entry.Seq
			}
			checked++
			if entry.Seq != want || entry.PrevHash != previous || entry.Hash != ledgerHash(entry) {
				broken = want
				return fmt.Errorf("ledger row %d does not verify", want)
			}
			want++
			previous = entry.Hash
			return nil
		})
	})
	if broken != 0 {
		// The walk stops at the first bad row, which is what "the first bad
		// row" means; the error is for a human, the number for a client.
		return checked, broken, nil
	}
	return checked, 0, err
}

// PruneLedger deletes every row older than before and leaves one marker row
// describing what went. Both happen in one transaction, so a crash cannot drop
// rows without a receipt. It returns the marker it wrote, or nil when nothing
// was old enough.
func (s *Store) PruneLedger(before time.Time) (*LedgerEntry, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("no store")
	}
	var marker *LedgerEntry
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(ledgerBucket)
		if bucket == nil {
			return errors.New("ledger bucket missing")
		}
		var doomed [][]byte
		var throughSeq uint64
		var throughHash string
		cursor := bucket.Cursor()
		for key, raw := cursor.First(); key != nil; key, raw = cursor.Next() {
			var entry LedgerEntry
			if err := json.Unmarshal(raw, &entry); err != nil {
				return fmt.Errorf("read ledger row: %w", err)
			}
			if !entry.At.Before(before) {
				break
			}
			doomed = append(doomed, append([]byte(nil), key...))
			throughSeq, throughHash = entry.Seq, entry.Hash
		}
		if len(doomed) == 0 {
			return nil
		}
		var head LedgerEntry
		if _, raw := bucket.Cursor().Last(); raw != nil {
			if err := json.Unmarshal(raw, &head); err != nil {
				return fmt.Errorf("read ledger head: %w", err)
			}
		}
		row := LedgerEntry{
			At:        time.Now().UTC(),
			Principal: "daemon",
			Verb:      "ledger.prune",
			Target:    fmt.Sprintf("through:%d", throughSeq),
			Decision:  LedgerAllow,
			Marker:    &PruneMarker{ThroughSeq: throughSeq, ThroughHash: throughHash, Dropped: len(doomed)},
			Seq:       head.Seq + 1,
			PrevHash:  head.Hash,
		}
		row.Hash = ledgerHash(row)
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if err := bucket.Put(ledgerKey(row.Seq), encoded); err != nil {
			return err
		}
		marker = &row
		for _, key := range doomed {
			if err := bucket.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
	return marker, err
}

// ledgerHash is SHA-256 over the row's canonical JSON with the hash field
// blanked. encoding/json writes struct fields in declaration order and the row
// has no maps, so the encoding is stable across processes and versions.
func ledgerHash(entry LedgerEntry) string {
	entry.Hash = ""
	raw, err := json.Marshal(entry)
	if err != nil {
		// Unreachable: every field is a scalar or a time.Time.
		panic("darkfactory: encode ledger row: " + err.Error())
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ledgerKey(seq uint64) []byte {
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, seq)
	return key
}
