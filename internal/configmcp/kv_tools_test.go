package configmcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/kv"
)

// kvListResponse mirrors the kv_list payload the model sees.
type kvListResponse struct {
	Entries []struct {
		Key            string  `json:"key"`
		Value          *string `json:"value"`
		ValueTruncated bool    `json:"value_truncated"`
		ValueOmitted   bool    `json:"value_omitted"`
		UpdatedAt      string  `json:"updated_at"`
		ExpiresAt      *string `json:"expires_at"`
	} `json:"entries"`
	Truncated     bool   `json:"truncated"`
	OmittedValues int    `json:"omitted_values"`
	Note          string `json:"note"`
}

func decodeKVList(t *testing.T, text string) kvListResponse {
	t.Helper()
	var resp kvListResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshaling kv_list response: %v (body: %s)", err, text)
	}
	return resp
}

// seedKV writes n keys of identical size under the given prefix.
func seedKV(t *testing.T, store kv.Store, agent, prefix string, n, valueBytes int) {
	t.Helper()
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("%s%02d", prefix, i)
		if err := store.Set(context.Background(), agent, key, strings.Repeat("x", valueBytes), 0); err != nil {
			t.Fatalf("seeding %s: %v", key, err)
		}
	}
}

// newKVServer builds a server whose KV store is also returned, so tests can
// seed it directly rather than through the tools.
func newKVServer(t *testing.T, overrides func(*configmcp.Deps)) (*mcp.ClientSession, kv.Store) {
	t.Helper()
	s, err := kv.NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	sess, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.KVStore = s
		if overrides != nil {
			overrides(d)
		}
	})
	return sess, s
}

func TestHandleKVList_CapsTotalBytes(t *testing.T) {
	// 20 entries of 200 bytes each is ~4 KB of values against a 1 KB budget,
	// so the tail must come back without values.
	session, store := newKVServer(t, func(d *configmcp.Deps) {
		d.KVListMaxBytes = 1024
		d.KVListValueHeadBytes = 512
	})
	seedKV(t, store, "test-agent", "log:heartbeat:", 20, 200)

	text, isErr := callTool(t, session, "kv_list", map[string]any{"prefix": "log:heartbeat:"})
	if isErr {
		t.Fatalf("kv_list failed: %s", text)
	}
	resp := decodeKVList(t, text)

	if len(resp.Entries) != 20 {
		t.Fatalf("entries = %d, want all 20 keys returned", len(resp.Entries))
	}
	if !resp.Truncated {
		t.Error("truncated = false, want true")
	}
	if resp.OmittedValues == 0 {
		t.Error("omitted_values = 0, want the tail to be omitted")
	}
	if resp.Note == "" {
		t.Error("note is empty, want guidance to kv_get the omitted keys")
	}

	// Keys and metadata stay complete; only values are dropped, from the tail.
	seenOmitted := false
	for i, e := range resp.Entries {
		if e.Key == "" || e.UpdatedAt == "" {
			t.Fatalf("entry %d lost key/metadata: %+v", i, e)
		}
		if e.ValueOmitted {
			seenOmitted = true
			if e.Value != nil {
				t.Errorf("entry %d marked value_omitted but carries a value", i)
			}
			continue
		}
		if seenOmitted {
			t.Errorf("entry %d carries a value after an omitted one, want tail-only omission", i)
		}
		if e.Value == nil {
			t.Errorf("entry %d has no value and no marker", i)
		}
	}

	// Keys are deliberately unbounded by this cap (max_keys_per_agent bounds
	// them); what the budget governs is how many value bytes ride along.
	valueBytes := 0
	for _, e := range resp.Entries {
		if e.Value != nil {
			valueBytes += len(*e.Value)
		}
	}
	if valueBytes > 1024 {
		t.Errorf("included values total %d bytes, above the 1024-byte budget", valueBytes)
	}
	if valueBytes == 0 {
		t.Error("no values included at all, want the budget spent on the head of the list")
	}
	if omitted := countOmitted(resp); omitted != resp.OmittedValues {
		t.Errorf("omitted_values = %d, but %d entries are marked", resp.OmittedValues, omitted)
	}
}

func countOmitted(resp kvListResponse) int {
	n := 0
	for _, e := range resp.Entries {
		if e.ValueOmitted {
			n++
		}
	}
	return n
}

func TestHandleKVList_CapsSingleValue(t *testing.T) {
	// One value large enough to eat a whole budget on its own must be cut to
	// its head rather than crowding out every other entry.
	session, store := newKVServer(t, func(d *configmcp.Deps) {
		d.KVListMaxBytes = 8192
		d.KVListValueHeadBytes = 64
	})
	if err := store.Set(context.Background(), "test-agent", "log:big", strings.Repeat("y", 5000), 0); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := store.Set(context.Background(), "test-agent", "log:small", "short", 0); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	text, isErr := callTool(t, session, "kv_list", map[string]any{"prefix": "log:"})
	if isErr {
		t.Fatalf("kv_list failed: %s", text)
	}
	resp := decodeKVList(t, text)

	if len(resp.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(resp.Entries))
	}
	if !resp.Truncated {
		t.Error("truncated = false, want true after a head cap")
	}

	for _, e := range resp.Entries {
		switch e.Key {
		case "log:big":
			if !e.ValueTruncated {
				t.Error("log:big value_truncated = false, want true")
			}
			if e.Value == nil {
				t.Fatal("log:big lost its value entirely, want the head kept")
			}
			// Head plus the multi-byte ellipsis marker.
			if len(*e.Value) > 64+4 {
				t.Errorf("log:big value is %d bytes, want at most 64 plus marker", len(*e.Value))
			}
			if !strings.HasPrefix(*e.Value, strings.Repeat("y", 64)) {
				t.Error("log:big value is not the head of the stored value")
			}
		case "log:small":
			if e.ValueTruncated || e.ValueOmitted {
				t.Errorf("log:small should be untouched, got %+v", e)
			}
			if e.Value == nil || *e.Value != "short" {
				t.Errorf("log:small value = %v, want %q", e.Value, "short")
			}
		default:
			t.Fatalf("unexpected key %q", e.Key)
		}
	}
}

func TestHandleKVList_KeysOnly(t *testing.T) {
	session, store := newKVServer(t, nil)
	seedKV(t, store, "test-agent", "log:heartbeat:", 5, 300)

	text, isErr := callTool(t, session, "kv_list", map[string]any{
		"prefix":    "log:heartbeat:",
		"keys_only": true,
	})
	if isErr {
		t.Fatalf("kv_list failed: %s", text)
	}
	resp := decodeKVList(t, text)

	if len(resp.Entries) != 5 {
		t.Fatalf("entries = %d, want 5", len(resp.Entries))
	}
	if resp.Truncated {
		t.Error("truncated = true, want false: the caller asked for keys only")
	}
	if strings.Contains(text, "xxx") {
		t.Errorf("keys_only response leaked value bytes: %s", text)
	}
	for i, e := range resp.Entries {
		if e.Value != nil {
			t.Errorf("entry %d carries a value under keys_only", i)
		}
		if e.Key == "" || e.UpdatedAt == "" {
			t.Errorf("entry %d lost key/metadata: %+v", i, e)
		}
	}
}

func TestHandleKVList_SmallNamespaceIsUnchanged(t *testing.T) {
	// The common case must not grow markers or flags.
	session, store := newKVServer(t, nil)
	if err := store.Set(context.Background(), "test-agent", "pref:tz", "Australia/Sydney", 0); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	text, isErr := callTool(t, session, "kv_list", map[string]any{"prefix": "pref:"})
	if isErr {
		t.Fatalf("kv_list failed: %s", text)
	}
	resp := decodeKVList(t, text)

	if resp.Truncated || resp.OmittedValues != 0 || resp.Note != "" {
		t.Errorf("small list reported truncation: %s", text)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].Value == nil || *resp.Entries[0].Value != "Australia/Sydney" {
		t.Errorf("unexpected payload: %s", text)
	}
}

func TestHandleKVList_EmptyValueSurvives(t *testing.T) {
	// A stored empty string must not read as an omitted value.
	session, store := newKVServer(t, nil)
	if err := store.Set(context.Background(), "test-agent", "state:flag", "", 0); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	text, isErr := callTool(t, session, "kv_list", map[string]any{"prefix": "state:"})
	if isErr {
		t.Fatalf("kv_list failed: %s", text)
	}
	resp := decodeKVList(t, text)

	if len(resp.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(resp.Entries))
	}
	e := resp.Entries[0]
	if e.ValueOmitted {
		t.Error("empty stored value reported as value_omitted")
	}
	if e.Value == nil || *e.Value != "" {
		t.Errorf("value = %v, want an empty string", e.Value)
	}
}

func TestHandleKVList_DescriptionStatesLiveCaps(t *testing.T) {
	session, _ := newKVServer(t, func(d *configmcp.Deps) {
		d.KVListMaxBytes = 4242
		d.KVListValueHeadBytes = 321
	})

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tl := range result.Tools {
		if tl.Name != "kv_list" {
			continue
		}
		if !strings.Contains(tl.Description, "4242") || !strings.Contains(tl.Description, "321") {
			t.Errorf("kv_list description does not state the live caps: %s", tl.Description)
		}
		return
	}
	t.Fatal("kv_list not advertised")
}

func TestHandleKVSet_DefaultTTLByPrefix(t *testing.T) {
	session, store := newKVServer(t, func(d *configmcp.Deps) {
		d.KVDefaultTTL = map[string]time.Duration{
			"log:":           720 * time.Hour,
			"log:heartbeat:": 168 * time.Hour,
		}
	})
	ctx := context.Background()
	set := func(key, ttl string) {
		t.Helper()
		args := map[string]any{"key": key, "value": "v"}
		if ttl != "" {
			args["ttl"] = ttl
		}
		if text, isErr := callTool(t, session, "kv_set", args); isErr {
			t.Fatalf("kv_set %s: %s", key, text)
		}
	}
	expiry := func(key string) *time.Time {
		t.Helper()
		entries, err := store.List(ctx, "test-agent", key)
		if err != nil || len(entries) != 1 {
			t.Fatalf("List %s: n=%d err=%v", key, len(entries), err)
		}
		return entries[0].ExpiresAt
	}
	within := func(got *time.Time, want time.Duration) bool {
		if got == nil {
			return false
		}
		d := time.Until(*got)
		return d > want-time.Minute && d <= want+time.Minute
	}

	set("log:email-cleanup:2026-09-05", "")
	if exp := expiry("log:email-cleanup:2026-09-05"); !within(exp, 720*time.Hour) {
		t.Errorf("log: prefix default not applied, expiry = %v", exp)
	}
	set("log:heartbeat:2026-09-05", "")
	if exp := expiry("log:heartbeat:2026-09-05"); !within(exp, 168*time.Hour) {
		t.Errorf("longest prefix should win, expiry = %v", exp)
	}
	set("log:heartbeat:2026-09-06", "24h")
	if exp := expiry("log:heartbeat:2026-09-06"); !within(exp, 24*time.Hour) {
		t.Errorf("explicit ttl should win, expiry = %v", exp)
	}
	set("pref:email_noise_senders", "")
	if exp := expiry("pref:email_noise_senders"); exp != nil {
		t.Errorf("unmatched prefix should not expire, got %v", exp)
	}
}
