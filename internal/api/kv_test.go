package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/kv"
)

// testDepsWithKV returns deps with a real in-memory KV store.
func testDepsWithKV(t *testing.T) Deps {
	t.Helper()
	deps := testDeps()
	store, err := kv.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	deps.KVStore = store
	return deps
}

func TestListKV_Empty(t *testing.T) {
	deps := testDepsWithKV(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/kv/default", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Errorf("expected empty entries: %s", rec.Body.String())
	}
}

func TestListKV_WithData(t *testing.T) {
	deps := testDepsWithKV(t)
	ctx := context.Background()
	_ = deps.KVStore.Set(ctx, "default", "key1", "value1", 0)
	_ = deps.KVStore.Set(ctx, "default", "key2", "value2", 0)

	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/kv/default", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"key1"`) || !strings.Contains(rec.Body.String(), `"key2"`) {
		t.Errorf("response missing keys: %s", rec.Body.String())
	}
}

func TestListKV_WithPrefix(t *testing.T) {
	deps := testDepsWithKV(t)
	ctx := context.Background()
	_ = deps.KVStore.Set(ctx, "default", "cache:a", "1", 0)
	_ = deps.KVStore.Set(ctx, "default", "cache:b", "2", 0)
	_ = deps.KVStore.Set(ctx, "default", "other", "3", 0)

	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/kv/default?prefix=cache:", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"cache:a"`) || !strings.Contains(body, `"cache:b"`) {
		t.Errorf("response missing prefixed keys: %s", body)
	}
	if strings.Contains(body, `"other"`) {
		t.Errorf("response should not include non-prefixed key: %s", body)
	}
}

func TestListKV_AgentNotFound(t *testing.T) {
	deps := testDepsWithKV(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/kv/no-such-agent", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestGetKV_Success(t *testing.T) {
	deps := testDepsWithKV(t)
	ctx := context.Background()
	_ = deps.KVStore.Set(ctx, "default", "mykey", "myvalue", 0)

	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/kv/default/mykey", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"myvalue"`) {
		t.Errorf("response missing value: %s", rec.Body.String())
	}
}

func TestGetKV_NotFound(t *testing.T) {
	deps := testDepsWithKV(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/kv/default/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDeleteKV_Success(t *testing.T) {
	deps := testDepsWithKV(t)
	ctx := context.Background()
	_ = deps.KVStore.Set(ctx, "default", "delme", "gone", 0)

	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/kv/default/delme", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	// Verify key is gone.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/kv/default/delme", nil)
	req2.Header.Set("Authorization", "Bearer dk-test-key")
	rec2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotFound {
		t.Errorf("GET after delete: status = %d, want %d", rec2.Code, http.StatusNotFound)
	}
}

func TestGetKV_AgentNotFound(t *testing.T) {
	deps := testDepsWithKV(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/kv/no-such-agent/key1", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDeleteKV_AgentNotFound(t *testing.T) {
	deps := testDepsWithKV(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/kv/no-such-agent/key1", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestGetKV_WildcardPath(t *testing.T) {
	deps := testDepsWithKV(t)
	ctx := context.Background()
	_ = deps.KVStore.Set(ctx, "default", "ns/subkey", "nested-value", 0)

	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/kv/default/ns/subkey", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"nested-value"`) {
		t.Errorf("response missing value: %s", rec.Body.String())
	}
}

func TestDeleteKV_WildcardPath(t *testing.T) {
	deps := testDepsWithKV(t)
	ctx := context.Background()
	_ = deps.KVStore.Set(ctx, "default", "ns/to-delete", "value", 0)

	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/kv/default/ns/to-delete", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestListKV_NoStore(t *testing.T) {
	deps := testDeps() // no KVStore
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/kv/default", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestGetKV_NoStore(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/kv/default/key1", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestDeleteKV_NoStore(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/kv/default/key1", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
