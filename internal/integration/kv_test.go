//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"
)

func TestKV_ListEmpty(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/kv/default", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	entries, ok := resp["entries"].([]any)
	if !ok {
		t.Fatalf("entries is not an array: %v", resp["entries"])
	}
	if len(entries) != 0 {
		t.Errorf("entries count = %d, want 0", len(entries))
	}
}

func TestKV_SetAndGet(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	// Set a value directly via the store (no SET endpoint in REST API).
	if err := h.KVStore.Set(ctx, "default", "greeting", "hello world", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get via API.
	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/kv/default/greeting", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	if resp["key"] != "greeting" {
		t.Errorf("key = %v, want greeting", resp["key"])
	}
	if resp["value"] != "hello world" {
		t.Errorf("value = %v, want 'hello world'", resp["value"])
	}
}

func TestKV_GetNotFound(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/kv/default/nonexistent", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestKV_GetBadAgent(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/kv/no-such-agent/key", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestKV_DeleteKey(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	if err := h.KVStore.Set(ctx, "default", "temp-key", "temp-value", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Delete via API.
	delRec := h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/kv/default/temp-key", nil))
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", delRec.Code, http.StatusNoContent)
	}

	// Verify it's gone.
	getRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/kv/default/temp-key", nil))
	if getRec.Code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want %d", getRec.Code, http.StatusNotFound)
	}
}

func TestKV_ListWithPrefix(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	// Set several keys with different prefixes.
	if err := h.KVStore.Set(ctx, "default", "app:color", "blue", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := h.KVStore.Set(ctx, "default", "app:size", "large", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := h.KVStore.Set(ctx, "default", "other:key", "value", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// List with prefix filter.
	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/kv/default?prefix=app:", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	entries := resp["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries count = %d, want 2", len(entries))
	}
}

func TestKV_ListAll(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	if err := h.KVStore.Set(ctx, "default", "k1", "v1", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := h.KVStore.Set(ctx, "default", "k2", "v2", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/kv/default", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	entries := resp["entries"].([]any)
	if len(entries) != 2 {
		t.Errorf("entries count = %d, want 2", len(entries))
	}
}
