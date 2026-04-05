package api

import (
	"sync"
	"time"
)

const defaultReplayCapacity = 200

// replayEntry pairs a frame with its insertion timestamp for TTL eviction.
type replayEntry struct {
	frame WSEventFrame
	at    time.Time
}

// ReplayBuffer is a fixed-size circular buffer of WSEventFrames with
// time-based eviction. It is safe for concurrent use.
type ReplayBuffer struct {
	mu      sync.Mutex
	entries []replayEntry
	head    int // next write position
	count   int // number of valid entries (≤ cap)
	ttl     time.Duration
}

// NewReplayBuffer creates a buffer with the given capacity and TTL.
func NewReplayBuffer(capacity int, ttl time.Duration) *ReplayBuffer {
	if capacity <= 0 {
		capacity = defaultReplayCapacity
	}
	return &ReplayBuffer{
		entries: make([]replayEntry, capacity),
		ttl:     ttl,
	}
}

// Append adds a frame to the buffer. Old entries beyond capacity are
// overwritten. Entries older than the TTL are logically evicted on read.
func (b *ReplayBuffer) Append(frame WSEventFrame) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.entries[b.head] = replayEntry{frame: frame, at: time.Now()}
	b.head = (b.head + 1) % len(b.entries)
	if b.count < len(b.entries) {
		b.count++
	}
}

// ReplaySince returns all buffered frames with Seq > afterSeq that are within
// the TTL window. Frames are returned in insertion order.
func (b *ReplayBuffer) ReplaySince(afterSeq int64) []WSEventFrame {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == 0 {
		return nil
	}

	cutoff := time.Now().Add(-b.ttl)
	cap := len(b.entries)

	// Start index: oldest valid entry.
	start := (b.head - b.count + cap) % cap

	var result []WSEventFrame
	for i := 0; i < b.count; i++ {
		idx := (start + i) % cap
		e := b.entries[idx]
		if e.at.Before(cutoff) {
			continue // expired
		}
		if e.frame.Seq <= afterSeq {
			continue // already seen
		}
		result = append(result, e.frame)
	}
	return result
}

// Len returns the number of entries currently in the buffer (including expired
// ones that haven't been overwritten yet).
func (b *ReplayBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

// ReplayStore manages per-session replay buffers.
type ReplayStore struct {
	mu       sync.Mutex
	buffers  map[string]*ReplayBuffer
	capacity int
	ttl      time.Duration
}

// NewReplayStore creates a store that allocates replay buffers on demand.
func NewReplayStore(capacity int, ttl time.Duration) *ReplayStore {
	if capacity <= 0 {
		capacity = defaultReplayCapacity
	}
	return &ReplayStore{
		buffers:  make(map[string]*ReplayBuffer),
		capacity: capacity,
		ttl:      ttl,
	}
}

// Buffer returns the replay buffer for the given session, creating one if it
// does not exist.
func (s *ReplayStore) Buffer(sessionID string) *ReplayBuffer {
	s.mu.Lock()
	defer s.mu.Unlock()

	buf, ok := s.buffers[sessionID]
	if !ok {
		buf = NewReplayBuffer(s.capacity, s.ttl)
		s.buffers[sessionID] = buf
	}
	return buf
}

// Remove deletes the replay buffer for a session.
func (s *ReplayStore) Remove(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.buffers, sessionID)
}

// Cleanup removes all buffers that have no entries within the TTL window.
// Call periodically to prevent unbounded growth.
func (s *ReplayStore) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-s.ttl)
	for id, buf := range s.buffers {
		buf.mu.Lock()
		allExpired := true
		if buf.count > 0 {
			// Check the most recent entry (head-1).
			newest := (buf.head - 1 + len(buf.entries)) % len(buf.entries)
			if !buf.entries[newest].at.Before(cutoff) {
				allExpired = false
			}
		}
		buf.mu.Unlock()
		if allExpired {
			delete(s.buffers, id)
		}
	}
}

// Len returns the number of tracked sessions.
func (s *ReplayStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buffers)
}
