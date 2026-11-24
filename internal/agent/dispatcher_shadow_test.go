package agent

import (
	"context"
	"reflect"
	"testing"

	"github.com/Temikus/denkeeper/internal/adapter"
)

// Adapters never read Shadow: a pending approval renders the same with or
// without it.
func TestDispatcher_PendingWithShadowRendersUnchanged(t *testing.T) {
	render := func(shadow *ShadowVerdict) []adapter.OutgoingMessage {
		ma := &editorMockAdapter{name: "telegram"}
		d := newPipelineDispatcher(t, ma)
		handle := d.buildEventHandler(context.Background(), adapter.IncomingMessage{Adapter: "telegram", ExternalID: "12345"})
		handle(ChatEvent{Type: "tool_approval", Tool: "web_search", Text: `{"query":"test"}`, ApprovalCallback: "cb-1", Shadow: shadow})
		return ma.Sent()
	}
	score := 0.97
	with := render(&ShadowVerdict{Agent: "default", Decider: "jev", Verdict: "APPROVE", Lowest: "no_secrets", Score: &score})
	without := render(nil)
	if !reflect.DeepEqual(with, without) {
		t.Fatalf("shadow changed the adapter message:\nwith:    %+v\nwithout: %+v", with, without)
	}
}
