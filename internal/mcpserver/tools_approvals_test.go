package mcpserver

import (
	"context"
	"testing"

	"github.com/Temikus/denkeeper/internal/approval"
)

func approvalWriteCtx() context.Context {
	return withScopes(context.Background(), []string{"approvals:write"})
}

func approvalResolveServer(t *testing.T) (*Server, *approval.Request) {
	t.Helper()
	store, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := approval.NewManager(store, testLogger())
	req, err := manager.Submit(context.Background(), "default", approval.ActionKindUserUpdate,
		"summary", "payload", "123", "test", "conv1",
		func(_ context.Context, _ string) error { return nil })
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
