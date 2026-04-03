//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/Temikus/denkeeper/internal/approval"
)

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
