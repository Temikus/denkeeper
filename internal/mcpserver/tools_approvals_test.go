package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/approval"
)

func approvalWriteCtx() context.Context {
	return withScopes(context.Background(), []string{"approvals:write"})
}

func approvalResolveServer(t *testing.T) (*Server, *approval.Request) {
	t.Helper()
	return approvalResolveServerWithAction(t, func(_ context.Context, _ string) error { return nil })
}

func approvalResolveServerWithAction(t *testing.T, action approval.ActionFunc) (*Server, *approval.Request) {
	t.Helper()
	store, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := approval.NewManager(store, testLogger())
	req, err := manager.Submit(context.Background(), "default", approval.ActionKindUserUpdate,
		"summary", "payload", "123", "test", "conv1", action)
	if err != nil {
		t.Fatalf("submitting approval: %v", err)
	}
	return &Server{deps: Deps{Approvals: manager, Logger: testLogger()}}, req
}

func TestApprovalResolve_Approved(t *testing.T) {
	s, req := approvalResolveServer(t)
	res, _, err := s.handleApprovalResolve(approvalWriteCtx(), nil, approvalResolveInput{
		ID: req.ID, Action: "approve",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if got := toolResultText(res); got != "approval approved" {
		t.Errorf("result = %q, want %q", got, "approval approved")
	}
}

func TestApprovalResolve_Denied(t *testing.T) {
	s, req := approvalResolveServer(t)
	res, _, err := s.handleApprovalResolve(approvalWriteCtx(), nil, approvalResolveInput{
		ID: req.ID, Action: "deny",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if got := toolResultText(res); got != "approval denied" {
		t.Errorf("result = %q, want %q", got, "approval denied")
	}
}

// A failed action leaves the row approved, so the reply must not read as a
// retryable failure (a retry gets ErrAlreadyResolved).
func TestApprovalResolve_ActionFailed(t *testing.T) {
	s, req := approvalResolveServerWithAction(t, func(_ context.Context, _ string) error {
		return errors.New("disk full")
	})
	res, _, err := s.handleApprovalResolve(approvalWriteCtx(), nil, approvalResolveInput{
		ID: req.ID, Action: "approve",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Error("expected an error result")
	}
	got := toolResultText(res)
	if !strings.Contains(got, "approval approved") || !strings.Contains(got, "action failed: disk full") {
		t.Errorf("result = %q, want approval recorded + action failure", got)
	}
	if strings.Contains(got, "resolve failed") {
		t.Errorf("result = %q reads as an unrecorded resolve", got)
	}
}
