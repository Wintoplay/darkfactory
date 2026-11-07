package store

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"

	bolt "go.etcd.io/bbolt"
)

var errInvalidReplayRecord = errors.New("invalid encrypted request replay record")

const (
	replayExpirySize = 8
	replayKeySize    = replayExpirySize + sha256.Size
	// replaySweepLimit bounds the expired records one request may delete, so a
	// backlog can never hold bolt's single writer for an unbounded burst.
	replaySweepLimit = 256
)

// ConsumeE2EERequest atomically records a request until its timestamp expires.
// It must commit before the caller dispatches the request.
//
// The key is big-endian expiry || SHA-256(deviceID NUL requestID), so expired
// records form an ordered prefix and each call deletes at most replaySweepLimit
// of them. A replay carries the same authenticated timestamp (the AEAD binds
// it), hence the same expiry, so membership stays a single Get.
func (s *Store) ConsumeE2EERequest(deviceID, requestID string, now, expires time.Time) (bool, error) {
	digest := sha256.Sum256([]byte(deviceID + "\x00" + requestID))
	nowMillis := now.UnixMilli()
	var key [replayKeySize]byte
	binary.BigEndian.PutUint64(key[:replayExpirySize], uint64(expires.UnixMilli()))
	copy(key[replayExpirySize:], digest[:])

	accepted := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(e2eeReplayBucket)
		cursor := bucket.Cursor()
		deleted := 0
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			if len(k) != replayKeySize || len(v) != 1 {
				return errInvalidReplayRecord
			}
			if int64(binary.BigEndian.Uint64(k[:replayExpirySize])) >= nowMillis {
				break
			}
			if err := cursor.Delete(); err != nil {
				return err
			}
			deleted++
			if deleted >= replaySweepLimit {
				break
			}
		}
		if bucket.Get(key[:]) != nil {
			return nil
		}
		if err := bucket.Put(key[:], []byte{1}); err != nil {
			return err
		}
		accepted = true
		return nil
	})
	return accepted && err == nil, err
}
