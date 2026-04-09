package api

import (
	"sync"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
)

func makeEvent(seq int64) WSEventFrame {
	return WSEventFrame{
		ChatEvent: agent.ChatEvent{Type: "content", Text: "msg"},
		SessionID: "s1",
		Seq:       seq,
	}
}

func TestReplayBuffer_AppendAndReplay(t *testing.T) {
	buf := NewReplayBuffer(10, 5*time.Minute)

	buf.Append(makeEvent(1))
	buf.Append(makeEvent(2))
	buf.Append(makeEvent(3))

	frames := buf.ReplaySince(0)
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3", len(frames))
	}
	if frames[0].Seq != 1 || frames[2].Seq != 3 {
		t.Errorf("frames out of order: %d, %d, %d", frames[0].Seq, frames[1].Seq, frames[2].Seq)
	}
}

func TestReplayBuffer_ReplaySince(t *testing.T) {
	buf := NewReplayBuffer(10, 5*time.Minute)

	for i := int64(1); i <= 5; i++ {
		buf.Append(makeEvent(i))
	}

	frames := buf.ReplaySince(3)
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
	if frames[0].Seq != 4 {
		t.Errorf("first frame seq = %d, want 4", frames[0].Seq)
	}
	if frames[1].Seq != 5 {
		t.Errorf("second frame seq = %d, want 5", frames[1].Seq)
	}
}

func TestReplayBuffer_WrapAround(t *testing.T) {
	buf := NewReplayBuffer(3, 5*time.Minute)

	buf.Append(makeEvent(1))
	buf.Append(makeEvent(2))
	buf.Append(makeEvent(3))
	buf.Append(makeEvent(4)) // overwrites seq 1
	buf.Append(makeEvent(5)) // overwrites seq 2

	frames := buf.ReplaySince(0)
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3", len(frames))
	}
	if frames[0].Seq != 3 {
		t.Errorf("oldest frame seq = %d, want 3", frames[0].Seq)
	}
	if frames[2].Seq != 5 {
		t.Errorf("newest frame seq = %d, want 5", frames[2].Seq)
	}
}

func TestReplayBuffer_TTLEviction(t *testing.T) {
	buf := NewReplayBuffer(10, 50*time.Millisecond)

	buf.Append(makeEvent(1))
	time.Sleep(100 * time.Millisecond)
	buf.Append(makeEvent(2))

	// Frame 1 should be expired.
	frames := buf.ReplaySince(0)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1 (old frame should be expired)", len(frames))
	}
	if frames[0].Seq != 2 {
		t.Errorf("frame seq = %d, want 2", frames[0].Seq)
	}
}

func TestReplayBuffer_EmptyReplay(t *testing.T) {
	buf := NewReplayBuffer(10, 5*time.Minute)
	frames := buf.ReplaySince(0)
	if frames != nil {
		t.Errorf("got %v, want nil for empty buffer", frames)
	}
}

func TestReplayBuffer_Concurrent(t *testing.T) {
	buf := NewReplayBuffer(50, 5*time.Minute)
	var wg sync.WaitGroup

	// Concurrent writers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				buf.Append(makeEvent(int64(base*10 + j)))
			}
		}(i)
	}

	// Concurrent readers.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = buf.ReplaySince(0)
			}
		}()
	}

	wg.Wait()
	// No race detected = pass.
}

func TestReplayBuffer_Len(t *testing.T) {
	buf := NewReplayBuffer(5, 5*time.Minute)
	if buf.Len() != 0 {
		t.Errorf("empty buffer len = %d, want 0", buf.Len())
	}
	buf.Append(makeEvent(1))
	buf.Append(makeEvent(2))
	if buf.Len() != 2 {
		t.Errorf("len = %d, want 2", buf.Len())
	}
	// Fill past capacity.
	for i := 3; i <= 10; i++ {
		buf.Append(makeEvent(int64(i)))
	}
	if buf.Len() != 5 {
		t.Errorf("len = %d, want 5 (capped at capacity)", buf.Len())
	}
}

func TestReplayStore_BufferCreation(t *testing.T) {
	store := NewReplayStore(10, 5*time.Minute)

	b1 := store.Buffer("s1")
	b2 := store.Buffer("s1")
	if b1 != b2 {
		t.Error("expected same buffer for same session")
	}

	b3 := store.Buffer("s2")
	if b1 == b3 {
		t.Error("expected different buffer for different session")
	}

	if store.Len() != 2 {
		t.Errorf("store len = %d, want 2", store.Len())
	}
}

func TestReplayStore_Remove(t *testing.T) {
	store := NewReplayStore(10, 5*time.Minute)
	store.Buffer("s1")
	store.Remove("s1")
	if store.Len() != 0 {
		t.Errorf("store len = %d, want 0 after remove", store.Len())
	}
}

func TestReplayStore_SessionLimit_EvictsOldest(t *testing.T) {
	store := NewReplayStore(10, 5*time.Minute)
	store.maxSessions = 3

	// Fill to capacity.
	b1 := store.Buffer("s1")
	b1.Append(makeEvent(1))
	store.Buffer("s2")
	store.Buffer("s3")

	if store.Len() != 3 {
		t.Fatalf("store len = %d, want 3", store.Len())
	}

	// s1 has the oldest newest-entry; s2 and s3 are empty (newer by insertion).
	// Requesting s4 should evict one buffer to make room.
	store.Buffer("s4")
	if store.Len() != 3 {
		t.Fatalf("store len = %d, want 3 after eviction", store.Len())
	}
	// s4 should now exist.
	found := false
	store.mu.Lock()
	_, found = store.buffers["s4"]
	store.mu.Unlock()
	if !found {
		t.Error("s4 buffer should exist after eviction made room")
	}
}

func TestReplayStore_Cleanup(t *testing.T) {
	store := NewReplayStore(10, 50*time.Millisecond)
	buf := store.Buffer("s1")
	buf.Append(makeEvent(1))

	time.Sleep(100 * time.Millisecond)
	store.Cleanup()

	if store.Len() != 0 {
		t.Errorf("store len = %d, want 0 after cleanup of expired buffers", store.Len())
	}
}
