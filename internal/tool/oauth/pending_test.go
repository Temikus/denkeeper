package oauth

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func testPendingManager() *PendingManager {
	return NewPendingManager(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func TestPendingManager_CreateAndComplete(t *testing.T) {
	pm := testPendingManager()

	pa := pm.Create("todoist")
	if pa.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if pa.ToolName != "todoist" {
		t.Errorf("tool name: got %q, want %q", pa.ToolName, "todoist")
	}

	authURL := "https://auth.example.com/authorize?state=test-state-123&code_challenge=abc"
	if err := pm.SetAuthURL(pa.ID, authURL); err != nil {
		t.Fatalf("set auth url: %v", err)
	}

	// Complete from callback in a goroutine.
	done := make(chan struct{})
	go func() {
		defer close(done)
		code, state, err := pm.WaitForCompletion(context.Background(), pa.ID)
		if err != nil {
			t.Errorf("wait: %v", err)
			return
		}
		if code != "auth-code-xyz" {
			t.Errorf("code: got %q, want %q", code, "auth-code-xyz")
		}
		if state != "test-state-123" {
			t.Errorf("state: got %q, want %q", state, "test-state-123")
		}
	}()

	// Simulate callback.
	time.Sleep(50 * time.Millisecond)
	if err := pm.CompleteByState("test-state-123", "auth-code-xyz"); err != nil {
		t.Fatalf("complete: %v", err)
	}

	<-done
}

func TestPendingManager_WaitTimeout(t *testing.T) {
	pm := testPendingManager()
	pa := pm.Create("slow-tool")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, _, err := pm.WaitForCompletion(ctx, pa.ID)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestPendingManager_Cancel(t *testing.T) {
	pm := testPendingManager()
	pa := pm.Create("cancel-tool")

	done := make(chan error, 1)
	go func() {
		_, _, err := pm.WaitForCompletion(context.Background(), pa.ID)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	pm.Cancel(pa.ID)

	err := <-done
	if err == nil {
		t.Fatal("expected cancel error")
	}
}

func TestPendingManager_DuplicateToolCancelsFirst(t *testing.T) {
	pm := testPendingManager()

	pa1 := pm.Create("todoist")
	id1 := pa1.ID

	// Creating a second pending for the same tool cancels the first.
	pa2 := pm.Create("todoist")
	if pa2.ID == id1 {
		t.Error("second pending should have different ID")
	}

	// First pending should no longer exist.
	if got := pm.Get(id1); got != nil {
		t.Error("first pending should be removed")
	}

	// Second should exist.
	if got := pm.Get(pa2.ID); got == nil {
		t.Error("second pending should exist")
	}
}

func TestPendingManager_List(t *testing.T) {
	pm := testPendingManager()

	pm.Create("tool-a")
	pm.Create("tool-b")

	list := pm.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(list))
	}
}

func TestPendingManager_GetByToolName(t *testing.T) {
	pm := testPendingManager()
	pm.Create("my-tool")

	got := pm.GetByToolName("my-tool")
	if got == nil {
		t.Fatal("expected pending auth")
	}
	if got.ToolName != "my-tool" {
		t.Errorf("tool name: got %q", got.ToolName)
	}

	got = pm.GetByToolName("nonexistent")
	if got != nil {
		t.Error("expected nil for nonexistent tool")
	}
}

func TestPendingManager_CompleteByState_UnknownState(t *testing.T) {
	pm := testPendingManager()

	err := pm.CompleteByState("unknown-state", "code")
	if err == nil {
		t.Fatal("expected error for unknown state")
	}
}

func TestPendingManager_WaitForCompletion_UnknownID(t *testing.T) {
	pm := testPendingManager()

	_, _, err := pm.WaitForCompletion(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown ID")
	}
}

func TestPendingManager_SetAuthURL_UnknownID(t *testing.T) {
	pm := testPendingManager()

	err := pm.SetAuthURL("nonexistent", "https://example.com")
	if err == nil {
		t.Fatal("expected error for unknown ID")
	}
}

func TestPendingManager_Cleanup(t *testing.T) {
	pm := testPendingManager()

	pa := pm.Create("old-tool")
	// Manually set created_at to the past.
	pm.mu.Lock()
	pm.pending[pa.ID].CreatedAt = time.Now().Add(-10 * time.Minute)
	pm.mu.Unlock()

	pm.Cleanup()

	if got := pm.Get(pa.ID); got != nil {
		t.Error("expected expired pending to be cleaned up")
	}
}

func TestPendingManager_ConcurrentCreateAndWait(t *testing.T) {
	pm := testPendingManager()

	pa := pm.Create("race-tool")
	if err := pm.SetAuthURL(pa.ID, "https://auth.example.com?state=race-state"); err != nil {
		t.Fatalf("set auth url: %v", err)
	}

	// Start waiting in a goroutine.
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_, _, err := pm.WaitForCompletion(ctx, pa.ID)
		done <- err
	}()

	// Let the wait settle, then create a duplicate (cancels the first).
	time.Sleep(50 * time.Millisecond)
	pm.Create("race-tool") // should not panic

	err := <-done
	if err == nil {
		t.Fatal("expected error from cancelled wait")
	}
}

func TestPendingManager_CancelCleansState(t *testing.T) {
	pm := testPendingManager()

	pa := pm.Create("state-tool")
	if err := pm.SetAuthURL(pa.ID, "https://auth.example.com?state=clean-state-123"); err != nil {
		t.Fatalf("set auth url: %v", err)
	}

	// Verify state mapping exists.
	pm.mu.Lock()
	if _, ok := pm.byState["clean-state-123"]; !ok {
		pm.mu.Unlock()
		t.Fatal("expected state mapping to exist before cancel")
	}
	pm.mu.Unlock()

	pm.Cancel(pa.ID)

	// Verify state mapping is cleaned up.
	pm.mu.Lock()
	if _, ok := pm.byState["clean-state-123"]; ok {
		pm.mu.Unlock()
		t.Error("expected state mapping to be cleaned up after cancel")
	}
	pm.mu.Unlock()
}

func TestPendingManager_StartCleanup(t *testing.T) {
	pm := testPendingManager()

	pa := pm.Create("cleanup-tool")
	// Backdate the pending auth.
	pm.mu.Lock()
	pm.pending[pa.ID].CreatedAt = time.Now().Add(-10 * time.Minute)
	pm.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	go pm.StartCleanup(ctx, 50*time.Millisecond)

	// Wait for cleanup to fire.
	time.Sleep(200 * time.Millisecond)
	cancel()

	if got := pm.Get(pa.ID); got != nil {
		t.Error("expected expired pending to be cleaned up by StartCleanup")
	}
}
