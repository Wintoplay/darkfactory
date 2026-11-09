package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Wintoplay/darkfactory/internal/id"
	"github.com/Wintoplay/darkfactory/internal/model"
	bolt "go.etcd.io/bbolt"
)

var ErrNotFound = errors.New("not found")

// ErrOrderInvalid reports a reorder that names a worktree outside the repo or
// the same worktree twice. The wrapped message is safe to show a caller.
var ErrOrderInvalid = errors.New("invalid worktree order")

// ErrRepoOrderInvalid reports a fleet reorder that names an unknown repo or
// the same repo twice. The wrapped message is safe to show a caller.
var ErrRepoOrderInvalid = errors.New("invalid repo order")

// ErrSessionOrderInvalid reports a workspace reorder that names a session from
// another workspace, an unknown session, or the same one twice.
var ErrSessionOrderInvalid = errors.New("invalid session order")

// ErrLocked means another process holds bbolt's exclusive lock on state.db —
// almost always a `darkfactory serve` that is already running. The caller can
// name the pid instead of surfacing bbolt's bare "timeout".
var ErrLocked = errors.New("state database is locked by another process")

var (
	reposBucket       = []byte("repos")
	worktreesBucket   = []byte("worktrees")
	sessionsBucket    = []byte("sessions")
	transcriptsBucket = []byte("transcripts")
	browsersBucket    = []byte("browsers")
	deviceKeysBucket  = []byte("deviceKeys")
	plansBucket       = []byte("plans")
	runsBucket        = []byte("runs")
	stepsBucket       = []byte("steps")
	e2eeBucket        = []byte("e2ee")
	e2eeReplayBucket  = []byte("e2eeReplay")
	settingsBucket    = []byte("settings")
)

var (
	e2eeHostKey             = []byte("host")
	notificationSettingsKey = "notifications"
)

type Store struct {
	db *bolt.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		if errors.Is(err, bolt.ErrTimeout) {
			return nil, fmt.Errorf("%w (%s)", ErrLocked, path)
		}
		return nil, fmt.Errorf("open state: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, bucket := range [][]byte{reposBucket, worktreesBucket, sessionsBucket, transcriptsBucket, browsersBucket, deviceKeysBucket, plansBucket, runsBucket, stepsBucket, ledgerBucket, e2eeBucket, e2eeReplayBucket, settingsBucket} {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize state: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) PutRepo(repo model.Repo) (model.Repo, error) {
	if repo.ID == "" {
		repo.ID = id.New()
	}
	if repo.CreatedAt.IsZero() {
		repo.CreatedAt = time.Now().UTC()
	}
	return repo, s.put(reposBucket, repo.ID, repo)
}

func (s *Store) GetRepo(id string) (model.Repo, error) {
	var repo model.Repo
	err := s.get(reposBucket, id, &repo)
	return repo, err
}

func (s *Store) ListRepos() ([]model.Repo, error) {
	var repos []model.Repo
	err := s.list(reposBucket, func(raw []byte) error {
		var repo model.Repo
		if err := json.Unmarshal(raw, &repo); err != nil {
			return err
		}
		repos = append(repos, repo)
		return nil
	})
	sort.SliceStable(repos, func(i, j int) bool {
		a, b := repos[i], repos[j]
		// The fleet follows Position, then creation time. A repo nobody has
		// dragged (Position 0) sorts after the positioned ones, oldest first, so
		// a newly added repo still appears at the end.
		if (a.Position == 0) != (b.Position == 0) {
			return a.Position != 0
		}
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	return repos, err
}

func (s *Store) DeleteRepo(id string) error {
	return s.delete(reposBucket, id)
}

// ReorderRepos sets the fleet order of repo groups: ids first, in that order,
// then any repo the caller did not name, oldest first. It runs in one
// transaction, so a crash cannot leave the fleet half reordered, and it refuses
// an unknown id or a duplicate instead of silently dropping a repo.
func (s *Store) ReorderRepos(ids []string) ([]model.Repo, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: send at least one repo id", ErrRepoOrderInvalid)
	}
	var ordered []model.Repo
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(reposBucket)
		if bucket == nil {
			return errors.New("repos bucket missing")
		}
		byID := map[string]model.Repo{}
		var all []model.Repo
		if err := bucket.ForEach(func(_, raw []byte) error {
			var repo model.Repo
			if err := json.Unmarshal(raw, &repo); err != nil {
				return err
			}
			byID[repo.ID] = repo
			all = append(all, repo)
			return nil
		}); err != nil {
			return err
		}
		if len(all) == 0 {
			return ErrNotFound
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				return fmt.Errorf("%w: %s appears twice", ErrRepoOrderInvalid, id)
			}
			seen[id] = true
			repo, ok := byID[id]
			if !ok {
				return fmt.Errorf("%w: repo %s does not exist", ErrRepoOrderInvalid, id)
			}
			ordered = append(ordered, repo)
		}
		rest := make([]model.Repo, 0, len(all))
		for _, repo := range all {
			if !seen[repo.ID] {
				rest = append(rest, repo)
			}
		}
		sort.SliceStable(rest, func(i, j int) bool { return rest[i].CreatedAt.Before(rest[j].CreatedAt) })
		ordered = append(ordered, rest...)
		for i := range ordered {
			ordered[i].Position = i + 1
			if err := putJSON(bucket, ordered[i].ID, ordered[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ordered, nil
}

func (s *Store) PutWorktree(wt model.Worktree) (model.Worktree, error) {
	if wt.ID == "" {
		wt.ID = id.New()
	}
	if wt.CreatedAt.IsZero() {
		wt.CreatedAt = time.Now().UTC()
	}
	return wt, s.put(worktreesBucket, wt.ID, wt)
}

func (s *Store) GetWorktree(id string) (model.Worktree, error) {
	var wt model.Worktree
	err := s.get(worktreesBucket, id, &wt)
	return wt, err
}

func (s *Store) ListWorktrees() ([]model.Worktree, error) {
	var worktrees []model.Worktree
	err := s.list(worktreesBucket, func(raw []byte) error {
		var wt model.Worktree
		if err := json.Unmarshal(raw, &wt); err != nil {
			return err
		}
		worktrees = append(worktrees, wt)
		return nil
	})
	sort.SliceStable(worktrees, func(i, j int) bool {
		a, b := worktrees[i], worktrees[j]
		// The fleet follows Position, then creation time. A row nobody has
		// dragged (Position 0) sorts after the positioned ones, oldest first, so
		// a new worktree still appears at the end of its repo.
		if (a.Position == 0) != (b.Position == 0) {
			return a.Position != 0
		}
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	return worktrees, err
}

// ReorderWorktrees sets the fleet order of one repo's worktrees: ids first, in
// that order, then any sibling the caller did not name, oldest first. It runs in
// one transaction, so a crash cannot leave half a repo reordered, and it refuses
// an id that belongs to another repo instead of silently moving it.
func (s *Store) ReorderWorktrees(repoID string, ids []string) ([]model.Worktree, error) {
	if repoID == "" {
		return nil, errors.New("no repo")
	}
	var ordered []model.Worktree
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(worktreesBucket)
		if bucket == nil {
			return errors.New("worktrees bucket missing")
		}
		byID := map[string]model.Worktree{}
		var siblings []model.Worktree
		if err := bucket.ForEach(func(_, raw []byte) error {
			var wt model.Worktree
			if err := json.Unmarshal(raw, &wt); err != nil {
				return err
			}
			byID[wt.ID] = wt
			if wt.RepoID == repoID {
				siblings = append(siblings, wt)
			}
			return nil
		}); err != nil {
			return err
		}
		if len(siblings) == 0 {
			return ErrNotFound
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				return fmt.Errorf("%w: %s appears twice", ErrOrderInvalid, id)
			}
			seen[id] = true
			wt, ok := byID[id]
			if !ok || wt.RepoID != repoID {
				return fmt.Errorf("%w: worktree %s is not in this repo", ErrOrderInvalid, id)
			}
			ordered = append(ordered, wt)
		}
		rest := make([]model.Worktree, 0, len(siblings))
		for _, wt := range siblings {
			if !seen[wt.ID] {
				rest = append(rest, wt)
			}
		}
		sort.SliceStable(rest, func(i, j int) bool { return rest[i].CreatedAt.Before(rest[j].CreatedAt) })
		ordered = append(ordered, rest...)
		for i := range ordered {
			ordered[i].Position = i + 1
			if err := putJSON(bucket, ordered[i].ID, ordered[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ordered, nil
}

func (s *Store) DeleteWorktree(id string) error {
	return s.delete(worktreesBucket, id)
}

func (s *Store) PutSession(session model.Session) (model.Session, error) {
	if session.ID == "" {
		session.ID = id.New()
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	return session, s.put(sessionsBucket, session.ID, session)
}

func (s *Store) GetSession(id string) (model.Session, error) {
	var session model.Session
	err := s.get(sessionsBucket, id, &session)
	return session, err
}

// UpdateSession writes a session only when the row already exists. Exit and
// reaper callbacks must not resurrect a row deleted by the user.
func (s *Store) UpdateSession(session model.Session) error {
	raw, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("encode %s: %w", session.ID, err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(sessionsBucket)
		if b.Get([]byte(session.ID)) == nil {
			return ErrNotFound
		}
		return b.Put([]byte(session.ID), raw)
	})
}

func (s *Store) ListSessions() ([]model.Session, error) {
	var sessions []model.Session
	err := s.list(sessionsBucket, func(raw []byte) error {
		var session model.Session
		if err := json.Unmarshal(raw, &session); err != nil {
			return err
		}
		sessions = append(sessions, session)
		return nil
	})
	SortSessions(sessions)
	return sessions, err
}

// SessionLess is the order the sidebar shows: pinned sessions first, then the
// dragged Position, then age. A row nobody has dragged (Position 0) sorts after
// the positioned ones, so a new session still lands at the end of its workspace
// instead of jumping above the ones the owner arranged.
func SessionLess(a, b model.Session) bool {
	if a.Pinned != b.Pinned {
		return a.Pinned
	}
	if (a.Position == 0) != (b.Position == 0) {
		return a.Position != 0
	}
	if a.Position != b.Position {
		return a.Position < b.Position
	}
	return a.CreatedAt.Before(b.CreatedAt)
}

// SortSessions sorts in place with SessionLess.
func SortSessions(sessions []model.Session) {
	sort.SliceStable(sessions, func(i, j int) bool { return SessionLess(sessions[i], sessions[j]) })
}

// ReorderSessions sets the order of one workspace's sessions: ids first, in
// that order, then any sibling the caller did not name, in display order. It
// runs in one transaction, so a crash cannot leave half a workspace reordered,
// and it refuses an id that belongs to another workspace instead of silently
// moving it.
func (s *Store) ReorderSessions(worktreeID string, ids []string) ([]model.Session, error) {
	if worktreeID == "" {
		return nil, errors.New("no worktree")
	}
	var ordered []model.Session
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(sessionsBucket)
		if bucket == nil {
			return errors.New("sessions bucket missing")
		}
		byID := map[string]model.Session{}
		var siblings []model.Session
		if err := bucket.ForEach(func(_, raw []byte) error {
			var session model.Session
			if err := json.Unmarshal(raw, &session); err != nil {
				return err
			}
			byID[session.ID] = session
			if session.WorktreeID == worktreeID {
				siblings = append(siblings, session)
			}
			return nil
		}); err != nil {
			return err
		}
		if len(siblings) == 0 {
			return ErrNotFound
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				return fmt.Errorf("%w: %s appears twice", ErrSessionOrderInvalid, id)
			}
			seen[id] = true
			session, ok := byID[id]
			if !ok || session.WorktreeID != worktreeID {
				return fmt.Errorf("%w: session %s is not in this workspace", ErrSessionOrderInvalid, id)
			}
			ordered = append(ordered, session)
		}
		rest := make([]model.Session, 0, len(siblings))
		for _, session := range siblings {
			if !seen[session.ID] {
				rest = append(rest, session)
			}
		}
		SortSessions(rest)
		ordered = append(ordered, rest...)
		for i := range ordered {
			ordered[i].Position = i + 1
			if err := putJSON(bucket, ordered[i].ID, ordered[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ordered, nil
}

// SetSessionPinned flips one session's pin inside a transaction, so a concurrent
// status update cannot be lost to a read-modify-write on a stale row.
func (s *Store) SetSessionPinned(id string, pinned bool) (model.Session, error) {
	var out model.Session
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(sessionsBucket)
		raw := bucket.Get([]byte(id))
		if raw == nil {
			return ErrNotFound
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		out.Pinned = pinned
		return putJSON(bucket, out.ID, out)
	})
	if err != nil {
		return model.Session{}, err
	}
	return out, nil
}

func (s *Store) DeleteSession(id string) error {
	return s.delete(sessionsBucket, id)
}

// PutTranscript stores the bounded terminal ring for a session. It is separate
// from the session row so /state never carries megabytes of output.
func (s *Store) PutTranscript(id string, data []byte) error {
	if len(data) == 0 {
		if err := s.DeleteTranscript(id); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		return nil
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(transcriptsBucket).Put([]byte(id), data)
	})
}

func (s *Store) GetTranscript(id string) ([]byte, error) {
	var out []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(transcriptsBucket).Get([]byte(id))
		if raw == nil {
			return ErrNotFound
		}
		out = append([]byte(nil), raw...)
		return nil
	})
	return out, err
}

func (s *Store) DeleteTranscript(id string) error {
	return s.delete(transcriptsBucket, id)
}

// Browser is a paired browser session. The map key in the bucket is
// SHA-256(cookie token); the raw token stays in the browser and is never
// written here. ID is the public device identifier used by the revoke UI.
type Browser struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Scope      string    `json:"scope"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	// Terminal is compatibility output, not an independent grant. Current
	// authentication derives workspace access from Scope; rows written before
	// the workspace change are read the same way.
	Terminal bool `json:"terminal,omitempty"`
	// SecretTag fingerprints the instance secret a row was minted under. A
	// rotation changes the tag, so old rows are dropped instead of lingering as
	// phantom devices that can never authenticate again.
	SecretTag string `json:"secretTag,omitempty"`
	// KeyVersion records how the row key was derived. Rows without it predate
	// the instance-secret salt and are dropped on load: their key can never
	// match again, and listing them would show phantom devices.
	KeyVersion int `json:"keyVersion,omitempty"`
	// E2EEPublicKey is the native client's P-256 SPKI public key, base64. Its
	// presence opts the device into the legacy encrypted-envelope route. The
	// server never stores the client's private half.
	E2EEPublicKey string `json:"e2eePublicKey,omitempty"`
	// E2EESuite names the negotiated envelope suite. Empty means the legacy
	// P-256 scheme above; "x25519-mlkem768-v1" means the hybrid suite, which
	// has no stored client key because every request carries an ephemeral one.
	E2EESuite string `json:"e2eeSuite,omitempty"`
}

// PutBrowser persists a hashed browser session. The key is
// SHA-256(instanceSecret ‖ cookie token); the raw token stays in the browser and
// is never written here.
func (s *Store) PutBrowser(key string, browser Browser) error {
	if browser.ExpiresAt.IsZero() {
		return fmt.Errorf("encode browser: expiry is empty")
	}
	raw, err := json.Marshal(browser)
	if err != nil {
		return fmt.Errorf("encode browser: %w", err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(browsersBucket).Put([]byte(key), raw)
	})
}

func (s *Store) DeleteBrowser(key string) error {
	return s.delete(browsersBucket, key)
}

// ListBrowsers returns every stored hash and device record. A malformed row is
// skipped rather than invalidating every other browser session.
func (s *Store) ListBrowsers() (map[string]Browser, error) {
	out := make(map[string]Browser)
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(browsersBucket).ForEach(func(key, raw []byte) error {
			var rec Browser
			if err := json.Unmarshal(raw, &rec); err != nil {
				return nil
			}
			out[string(key)] = rec
			return nil
		})
	})
	return out, err
}

// GetE2EEHostKey returns the daemon's persisted identity file (JSON: P-256
// PKCS#8, X25519 scalar, ML-KEM-768 seed, epoch). A row written before the v2
// suite is a bare PKCS#8 DER key and is upgraded by the caller in place.
// A missing key is reported as ErrNotFound so the caller can create one once;
// an unreadable key must fail closed instead of silently rotating identity.
func (s *Store) GetE2EEHostKey() ([]byte, error) {
	var out []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(e2eeBucket).Get(e2eeHostKey)
		if raw == nil {
			return ErrNotFound
		}
		out = append([]byte(nil), raw...)
		return nil
	})
	return out, err
}

func (s *Store) PutE2EEHostKey(raw []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(e2eeBucket).Put(e2eeHostKey, raw)
	})
}

// DeviceKey is a long-lived public-key credential for a desktop/web client.
// The private key never leaves the client; the server stores only the SPKI
// public key. ID is the public device identifier used by the revoke UI.
type DeviceKey struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Scope      string    `json:"scope"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	// Terminal is compatibility output for the key's scope. Workspace access is
	// derived from Scope, so a read-only device cannot upgrade itself by
	// registering a key.
	Terminal bool `json:"terminal,omitempty"`
	// PublicKey is base64 DER SubjectPublicKeyInfo for an ECDSA P-256 key.
	PublicKey string `json:"publicKey"`
	// Algorithm is "ES256" today; a future algorithm is a new row, not a
	// silent reinterpretation of this one.
	Algorithm string `json:"algorithm"`
	// SecretTag mirrors Browser.SecretTag so a rotated instance secret drops
	// device keys that can no longer be trusted with the same generation.
	SecretTag string `json:"secretTag,omitempty"`
}

func (s *Store) PutDeviceKey(key DeviceKey) error {
	if key.ID == "" {
		return fmt.Errorf("encode device key: id is empty")
	}
	if key.PublicKey == "" {
		return fmt.Errorf("encode device key: public key is empty")
	}
	if key.ExpiresAt.IsZero() {
		return fmt.Errorf("encode device key: expiry is empty")
	}
	raw, err := json.Marshal(key)
	if err != nil {
		return fmt.Errorf("encode device key: %w", err)
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(deviceKeysBucket).Put([]byte(key.ID), raw)
	})
}

func (s *Store) DeleteDeviceKey(id string) error {
	return s.delete(deviceKeysBucket, id)
}

// ListDeviceKeys returns every stored public-key credential. A malformed row
// is skipped rather than invalidating every other device.
func (s *Store) ListDeviceKeys() (map[string]DeviceKey, error) {
	out := make(map[string]DeviceKey)
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(deviceKeysBucket).ForEach(func(_, raw []byte) error {
			var rec DeviceKey
			if err := json.Unmarshal(raw, &rec); err != nil || rec.ID == "" {
				return nil
			}
			out[rec.ID] = rec
			return nil
		})
	})
	return out, err
}

func (s *Store) PutPlan(plan model.Plan) (model.Plan, error) {
	if plan.Hash == "" {
		return plan, errors.New("plan hash is empty")
	}
	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = time.Now().UTC()
	}
	return plan, s.put(plansBucket, plan.Hash, plan)
}

func (s *Store) GetPlan(hash string) (model.Plan, error) {
	var plan model.Plan
	err := s.get(plansBucket, hash, &plan)
	return plan, err
}

func (s *Store) PutRun(run model.Run) (model.Run, error) {
	if run.ID == "" {
		run.ID = id.New()
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	}
	return run, s.put(runsBucket, run.ID, run)
}

func (s *Store) GetRun(id string) (model.Run, error) {
	var run model.Run
	err := s.get(runsBucket, id, &run)
	return run, err
}

func (s *Store) ListRuns() ([]model.Run, error) {
	var runs []model.Run
	err := s.list(runsBucket, func(raw []byte) error {
		var run model.Run
		if err := json.Unmarshal(raw, &run); err != nil {
			return err
		}
		runs = append(runs, run)
		return nil
	})
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.Before(runs[j].CreatedAt) })
	return runs, err
}

func (s *Store) PutStep(step model.Step) (model.Step, error) {
	if step.ID == "" {
		step.ID = id.New()
	}
	if step.CreatedAt.IsZero() {
		step.CreatedAt = time.Now().UTC()
	}
	return step, s.put(stepsBucket, step.ID, step)
}

func (s *Store) GetStep(id string) (model.Step, error) {
	var step model.Step
	err := s.get(stepsBucket, id, &step)
	return step, err
}

func (s *Store) ListSteps(runID string) ([]model.Step, error) {
	var steps []model.Step
	err := s.list(stepsBucket, func(raw []byte) error {
		var step model.Step
		if err := json.Unmarshal(raw, &step); err != nil {
			return err
		}
		if step.RunID == runID {
			steps = append(steps, step)
		}
		return nil
	})
	sort.Slice(steps, func(i, j int) bool {
		if steps[i].Ordinal != steps[j].Ordinal {
			return steps[i].Ordinal < steps[j].Ordinal
		}
		return steps[i].Visit < steps[j].Visit
	})
	return steps, err
}

func (s *Store) put(bucket []byte, id string, value any) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return putJSON(tx.Bucket(bucket), id, value)
	})
}

// putJSON is the transaction-scoped half of put, for a write that must share
// one transaction with other rows (a reorder).
func putJSON(bucket *bolt.Bucket, id string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", id, err)
	}
	return bucket.Put([]byte(id), raw)
}

func (s *Store) get(bucket []byte, id string, out any) error {
	return s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucket).Get([]byte(id))
		if raw == nil {
			return ErrNotFound
		}
		return json.Unmarshal(raw, out)
	})
}

func (s *Store) list(bucket []byte, fn func([]byte) error) error {
	return s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).ForEach(func(_, raw []byte) error {
			return fn(raw)
		})
	})
}

func (s *Store) delete(bucket []byte, id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		if b.Get([]byte(id)) == nil {
			return ErrNotFound
		}
		return b.Delete([]byte(id))
	})
}
