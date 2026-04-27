//go:build integration

package integration

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/audit"
)

// recordingAdapter is a minimal adapter.Adapter implementation that records
// every Send call. Used by tests that need to assert no adapter-level message
// was emitted (e.g. that the API approval handler does not push a redundant
// notification on top of the engine-driven activity log).
type recordingAdapter struct {
	name string
	mu   sync.Mutex
	sent []adapter.OutgoingMessage
}

func (r *recordingAdapter) Name() string { return r.name }
func (r *recordingAdapter) Start(_ context.Context, _ chan<- adapter.IncomingMessage) error {
	select {} // never delivers — tests submit approvals directly via the manager
}
func (r *recordingAdapter) Send(_ context.Context, msg adapter.OutgoingMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, msg)
	return nil
}
func (r *recordingAdapter) SendTyping(_ context.Context, _ string) error { return nil }
func (r *recordingAdapter) Stop() error                                  { return nil }
func (r *recordingAdapter) Sent() []adapter.OutgoingMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]adapter.OutgoingMessage, len(r.sent))
	copy(out, r.sent)
	return out
}

func TestApproval_SubmitAndApproveViaAPI(t *testing.T) {
	h := NewHarness(t, nil)

	// Submit an approval request directly via the manager.
	actionCalled := false
	req, err := h.Approvals.Submit(
		context.Background(),
		"default", approval.ActionKindUserUpdate,
		"test approval", "payload",
		"chat-123", "api", "session-1",
		func(_ context.Context, _ string) error {
			actionCalled = true
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// List approvals via API — should include the pending one.
	listRec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/approvals", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listRec.Code, http.StatusOK)
	}
	var listResp []map[string]any
	DecodeJSON(t, listRec, &listResp)
	if len(listResp) == 0 {
		t.Fatal("expected at least one approval in list")
	}

	// Approve via API.
	approveRec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/approvals/"+req.ID+"/approve", nil))
	if approveRec.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want %d; body: %s", approveRec.Code, http.StatusOK, approveRec.Body.String())
	}
	if !actionCalled {
		t.Error("expected action closure to be called on approval")
	}

	var approveResp map[string]any
	DecodeJSON(t, approveRec, &approveResp)
	if approveResp["status"] != "approved" {
		t.Errorf("status = %v, want approved", approveResp["status"])
	}
}

func TestApproval_SubmitAndDenyViaAPI(t *testing.T) {
	h := NewHarness(t, nil)

	actionCalled := false
	req, err := h.Approvals.Submit(
		context.Background(),
		"default", approval.ActionKindUserUpdate,
		"deny this", "payload",
		"chat-123", "api", "session-1",
		func(_ context.Context, _ string) error {
			actionCalled = true
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	denyRec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/approvals/"+req.ID+"/deny", nil))
	if denyRec.Code != http.StatusOK {
		t.Fatalf("deny status = %d, want %d", denyRec.Code, http.StatusOK)
	}
	if actionCalled {
		t.Error("action closure should NOT be called on denial")
	}

	var denyResp map[string]any
	DecodeJSON(t, denyRec, &denyResp)
	if denyResp["status"] != "denied" {
		t.Errorf("status = %v, want denied", denyResp["status"])
	}
}

func TestApproval_ApproveNonexistent_Returns404(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/approvals/nonexistent/approve", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestApproval_ResolveViaAPI_DoesNotNotifyAdapter locks in the contract that
// the API approval handler does not push a redundant raw notification onto the
// originating adapter. Resolution feedback flows exclusively through the
// engine event stream (tool_start/tool_end → activity log) and the audit log.
// See server.go:handleResolveApproval for the rationale.
func TestApproval_ResolveViaAPI_DoesNotNotifyAdapter(t *testing.T) {
	rec := &recordingAdapter{name: "telegram"}
	h := NewHarness(t, &HarnessOpts{Adapters: []adapter.Adapter{rec}})

	// Approve path.
	approveReq, err := h.Approvals.Submit(
		context.Background(),
		"default", approval.ActionKindUserUpdate,
		"approve this", "payload",
		"chat-123", "telegram", "session-1",
		func(_ context.Context, _ string) error { return nil },
	)
	if err != nil {
		t.Fatalf("Submit approve: %v", err)
	}
	resp := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/approvals/"+approveReq.ID+"/approve", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("approve status = %d, body: %s", resp.Code, resp.Body.String())
	}

	// Deny path.
	denyReq, err := h.Approvals.Submit(
		context.Background(),
		"default", approval.ActionKindUserUpdate,
		"deny this", "payload",
		"chat-123", "telegram", "session-1",
		func(_ context.Context, _ string) error { return nil },
	)
	if err != nil {
		t.Fatalf("Submit deny: %v", err)
	}
	resp = h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/approvals/"+denyReq.ID+"/deny", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("deny status = %d, body: %s", resp.Code, resp.Body.String())
	}

	if got := rec.Sent(); len(got) != 0 {
		t.Errorf("adapter Send called %d time(s); want 0. Messages: %+v", len(got), got)
	}

	// Audit log must still record both resolutions so operators have a record
	// of the API-driven decision even though no adapter message was emitted.
	h.FlushAudit(t)
	events, _, err := h.AuditStore.List(context.Background(), audit.ListOpts{Category: audit.CategoryApproval})
	if err != nil {
		t.Fatalf("listing audit events: %v", err)
	}
	approveSeen, denySeen := false, false
	for _, e := range events {
		if e.Source != "api" {
			continue
		}
		switch e.Action {
		case "approve":
			approveSeen = true
		case "deny":
			denySeen = true
		}
	}
	if !approveSeen {
		t.Error("expected audit event with action=approve, source=api")
	}
	if !denySeen {
		t.Error("expected audit event with action=deny, source=api")
	}
}

func TestApproval_FilterByStatus(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	// Submit and approve one.
	req1, _ := h.Approvals.Submit(ctx, "default", approval.ActionKindUserUpdate,
		"first", "p", "c", "api", "s", func(_ context.Context, _ string) error { return nil })
	h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/approvals/"+req1.ID+"/approve", nil))

	// Submit another (stays pending).
	_, _ = h.Approvals.Submit(ctx, "default", approval.ActionKindUserUpdate,
		"second", "p", "c", "api", "s", func(_ context.Context, _ string) error { return nil })

	// Filter for pending only.
	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/approvals?status=pending", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var result []map[string]any
	DecodeJSON(t, rec, &result)
	for _, r := range result {
		if r["status"] != "pending" {
			t.Errorf("expected only pending approvals, got status=%v", r["status"])
		}
	}
}
