package mcp

import (
	"testing"
	"time"
)

// A session that goes unused must eventually disappear: otherwise a client that
// initializes and never sends DELETE holds an entry for the life of the process.
func TestSessionStoreExpiresIdle(t *testing.T) {
	base := time.Now()
	clock := base
	s := newSessionStore(time.Hour, 0)
	s.now = func() time.Time { return clock }

	id := s.Generate()
	if terminated, err := s.Validate(id); err != nil || terminated {
		t.Fatalf("fresh session rejected: terminated=%v err=%v", terminated, err)
	}

	// Use before the deadline keeps the session: the TTL is idle time, not a
	// hard lifetime.
	clock = base.Add(45 * time.Minute)
	if _, err := s.Validate(id); err != nil {
		t.Fatalf("session expired early: %v", err)
	}
	clock = base.Add(90 * time.Minute)
	if _, err := s.Validate(id); err != nil {
		t.Fatalf("session expired while in use: %v", err)
	}

	clock = base.Add(90*time.Minute + time.Hour + time.Second)
	if _, err := s.Validate(id); err == nil {
		t.Fatal("an idle session outlived its TTL")
	}
}

// A caller that only ever initializes must not be able to grow the registry
// without limit.
func TestSessionStoreCapsLiveSessions(t *testing.T) {
	base := time.Now()
	clock := base
	s := newSessionStore(time.Hour, 3)
	s.now = func() time.Time { return clock }

	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, s.Generate())
		clock = clock.Add(time.Second)
	}
	if len(s.live) != 3 {
		t.Fatalf("live = %d, want 3", len(s.live))
	}

	// A fourth session evicts the least recently used one.
	newest := s.Generate()
	if len(s.live) != 3 {
		t.Fatalf("live = %d after the cap, want 3", len(s.live))
	}
	if _, err := s.Validate(ids[0]); err == nil {
		t.Fatal("the least recently used session was not evicted")
	}
	for _, id := range append(ids[1:], newest) {
		if _, err := s.Validate(id); err != nil {
			t.Fatalf("a session inside the cap was dropped: %v", err)
		}
	}
}

// Closing a session is remembered: a request arriving on it is told the session
// is over, which is not the same as an id nobody ever issued.
func TestSessionStoreRemembersTerminated(t *testing.T) {
	base := time.Now()
	clock := base
	s := newSessionStore(time.Hour, 0)
	s.now = func() time.Time { return clock }

	id := s.Generate()
	if _, err := s.Terminate(id); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	terminated, err := s.Validate(id)
	if err != nil {
		t.Fatalf("a terminated session must still be a known id: %v", err)
	}
	if !terminated {
		t.Fatal("terminated session reported as active")
	}
	// A repeated DELETE is not an error: the client may be retrying.
	if _, err := s.Terminate(id); err != nil {
		t.Fatalf("re-terminate: %v", err)
	}

	// The tombstone is dropped eventually.
	clock = base.Add(sessionDeadTTL + time.Minute)
	if _, err := s.Validate(id); err == nil {
		t.Fatal("a terminated id is still remembered after its tombstone expired")
	}
}

// Fabricated ids are rejected on shape alone.
func TestSessionStoreRejectsForeignIDs(t *testing.T) {
	s := newSessionStore(time.Hour, 0)
	for _, id := range []string{"", "abc", "mcp-session-", "mcp-session-not-a-uuid", "other-session-1234"} {
		if _, err := s.Validate(id); err == nil {
			t.Fatalf("id %q was accepted", id)
		}
		if _, err := s.Terminate(id); err == nil {
			t.Fatalf("id %q could be terminated", id)
		}
	}
	// A well-formed id that was never issued is unknown, not terminated.
	if terminated, err := s.Validate("mcp-session-3f330a0d-e46c-440e-852c-7830bd71ac4e"); err == nil || terminated {
		t.Fatalf("an unissued id was accepted: terminated=%v err=%v", terminated, err)
	}
}
