package mcp

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// sessionIdPrefix mirrors the id format the library's own manager issues, so
	// a session handed out here is indistinguishable from that one.
	sessionIdPrefix = "mcp-session-"
	// sessionIdleTTL is how long a session survives without being used. An id
	// kept forever is a leak: a client that initializes and never sends DELETE
	// would hold an entry for the life of the process.
	sessionIdleTTL = 2 * time.Hour
	// sessionMaxLive bounds how many sessions exist at once, so a caller that
	// only ever initializes cannot grow the registry without limit. Past the cap
	// the least recently used session is dropped and its client re-initializes.
	sessionMaxLive = 256
	// sessionDeadTTL is how long a terminated id is remembered, so a request
	// arriving late on a closed session is told the session is gone rather than
	// unknown.
	sessionDeadTTL = 10 * time.Minute
)

// sessionStore is the /mcp session registry. The library's default manager
// keeps every id it ever issued in memory for the life of the process; this one
// bounds both the number of live sessions and how long an idle one survives.
type sessionStore struct {
	mu   sync.Mutex
	live map[string]time.Time // id → last use
	dead map[string]time.Time // terminated id → when it stops being remembered
	now  func() time.Time

	ttl     time.Duration
	deadTTL time.Duration
	max     int
}

// newSessionStore builds a store with an idle TTL and a cap on live sessions
// (max <= 0 means uncapped).
func newSessionStore(ttl time.Duration, max int) *sessionStore {
	return &sessionStore{
		live:    map[string]time.Time{},
		dead:    map[string]time.Time{},
		now:     time.Now,
		ttl:     ttl,
		deadTTL: sessionDeadTTL,
		max:     max,
	}
}

// Generate issues a new session id, first dropping whatever expired.
func (s *sessionStore) Generate() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	if s.max > 0 && len(s.live) >= s.max {
		s.evictLeastRecent()
	}
	id := sessionIdPrefix + uuid.NewString()
	s.live[id] = s.now()
	return id
}

// Validate reports whether a session may still be used. It returns
// isTerminated=true for an id that was explicitly closed, which is a valid id
// whose session is over — the caller answers that differently from an unknown
// one.
func (s *sessionStore) Validate(sessionID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	if !validSessionID(sessionID) {
		return false, fmt.Errorf("invalid session id")
	}
	if _, ok := s.dead[sessionID]; ok {
		return true, nil
	}
	if _, ok := s.live[sessionID]; !ok {
		return false, fmt.Errorf("unknown session id")
	}
	// Using a session keeps it alive.
	s.live[sessionID] = s.now()
	return false, nil
}

// Terminate closes a session. Closing an already closed session is not an
// error: the client may retry a DELETE it never saw acknowledged.
func (s *sessionStore) Terminate(sessionID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	if !validSessionID(sessionID) {
		return false, fmt.Errorf("invalid session id")
	}
	if _, ok := s.dead[sessionID]; ok {
		return false, nil
	}
	if _, ok := s.live[sessionID]; !ok {
		return false, fmt.Errorf("unknown session id")
	}
	delete(s.live, sessionID)
	s.dead[sessionID] = s.now()
	return false, nil
}

// sweep drops idle live sessions and expired tombstones. Callers hold mu.
func (s *sessionStore) sweep() {
	now := s.now()
	for id, seen := range s.live {
		if now.Sub(seen) > s.ttl {
			delete(s.live, id)
		}
	}
	for id, at := range s.dead {
		if now.Sub(at) > s.deadTTL {
			delete(s.dead, id)
		}
	}
}

// evictLeastRecent drops the session that has gone unused the longest. Callers
// hold mu.
func (s *sessionStore) evictLeastRecent() {
	var oldest string
	var at time.Time
	for id, seen := range s.live {
		if oldest == "" || seen.Before(at) {
			oldest, at = id, seen
		}
	}
	if oldest != "" {
		delete(s.live, oldest)
	}
}

// validSessionID reports whether an id has the shape this store issues, so a
// fabricated id is rejected without touching the maps.
func validSessionID(sessionID string) bool {
	if !strings.HasPrefix(sessionID, sessionIdPrefix) {
		return false
	}
	_, err := uuid.Parse(strings.TrimPrefix(sessionID, sessionIdPrefix))
	return err == nil
}
