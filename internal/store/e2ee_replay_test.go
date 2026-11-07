package store

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestE2EEReplayPersistsUntilEnvelopeExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	now := time.Unix(1000, 0)
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := st.ConsumeE2EERequest("device", "request", now, now.Add(10*time.Minute))
	if err != nil || !accepted {
		t.Fatalf("first request: %v, %v", accepted, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	accepted, err = st.ConsumeE2EERequest("device", "request", now.Add(6*time.Minute), now.Add(10*time.Minute))
	if err != nil || accepted {
		t.Fatalf("replay after restart: %v, %v", accepted, err)
	}
	accepted, err = st.ConsumeE2EERequest("other", "request", now.Add(6*time.Minute), now.Add(10*time.Minute))
	if err != nil || !accepted {
		t.Fatalf("other device: %v, %v", accepted, err)
	}
	accepted, err = st.ConsumeE2EERequest("device", "request", now.Add(10*time.Minute+time.Millisecond), now.Add(11*time.Minute))
	if err != nil || !accepted {
		t.Fatalf("expired entry: %v, %v", accepted, err)
	}
}

func TestE2EEReplayConcurrentOnlyOneAccepted(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	expires := now.Add(5 * time.Minute)

	const callers = 16
	results := make(chan bool, callers)
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() {
			accepted, err := st.ConsumeE2EERequest("device", "same-request", now, expires)
			results <- accepted
			errs <- err
		}()
	}
	acceptedCount := 0
	for i := 0; i < callers; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		if <-results {
			acceptedCount++
		}
	}
	if acceptedCount != 1 {
		t.Fatalf("accepted %d concurrent copies of one request, want 1", acceptedCount)
	}
}

// A record that expires exactly now is still live: the clock check rejects the
// envelope at the same instant only once its timestamp is strictly older than
// the window, and the sweep uses the same strict comparison.
func TestE2EEReplayRecordSurvivesAtExpiryInstant(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	if accepted, err := st.ConsumeE2EERequest("device", "first", now, now); err != nil || !accepted {
		t.Fatalf("first request: %v, %v", accepted, err)
	}
	if accepted, err := st.ConsumeE2EERequest("device", "second", now, now.Add(time.Minute)); err != nil || !accepted {
		t.Fatalf("second request: %v, %v", accepted, err)
	}
	if accepted, err := st.ConsumeE2EERequest("device", "first", now, now); err != nil || accepted {
		t.Fatalf("replay at the expiry instant: %v, %v", accepted, err)
	}
}

// One request may not pay for the whole expired backlog; later requests drain
// it. This is the bound that keeps bolt's single writer available under load.
func TestE2EEReplaySweepIsBounded(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	expired := now.Add(-time.Minute)
	const seeded = 3*replaySweepLimit + 10
	err = st.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(e2eeReplayBucket)
		for i := 0; i < seeded; i++ {
			var key [replayKeySize]byte
			binary.BigEndian.PutUint64(key[:replayExpirySize], uint64(expired.UnixMilli()))
			sum := sha256.Sum256([]byte(fmt.Sprintf("old-%d", i)))
			copy(key[replayExpirySize:], sum[:])
			if err := bucket.Put(key[:], []byte{1}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if accepted, err := st.ConsumeE2EERequest("device", "new", now, now.Add(time.Minute)); err != nil || !accepted {
		t.Fatalf("new request: %v, %v", accepted, err)
	}
	if got, want := replayKeyCount(t, st), seeded-replaySweepLimit+1; got != want {
		t.Fatalf("after one call: %d keys, want %d (bounded sweep of %d)", got, want, replaySweepLimit)
	}

	for i := 0; i < seeded/replaySweepLimit+2; i++ {
		id := fmt.Sprintf("drain-%d", i)
		if accepted, err := st.ConsumeE2EERequest("device", id, now, now.Add(time.Minute)); err != nil || !accepted {
			t.Fatalf("%s: %v, %v", id, accepted, err)
		}
	}
	if got := expiredReplayCount(t, st, now); got != 0 {
		t.Fatalf("%d expired records remain after draining", got)
	}
}

func replayKeyCount(t *testing.T, st *Store) int {
	t.Helper()
	count := 0
	if err := st.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(e2eeReplayBucket).ForEach(func(_, _ []byte) error {
			count++
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func expiredReplayCount(t *testing.T, st *Store, now time.Time) int {
	t.Helper()
	count := 0
	if err := st.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(e2eeReplayBucket).Cursor()
		for k, _ := cursor.First(); k != nil; k, _ = cursor.Next() {
			if int64(binary.BigEndian.Uint64(k[:replayExpirySize])) < now.UnixMilli() {
				count++
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return count
}
