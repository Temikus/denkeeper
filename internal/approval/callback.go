package approval

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// Handler implements the adapter.CallbackResolver interface for approval
// callbacks. It maps Telegram inline keyboard button data (e.g.
// "appr:{id}:approve") to human-readable confirmation strings by delegating
// resolution to the Manager.
//
// It satisfies adapter.CallbackResolver without importing that package:
//
//	var _ adapter.CallbackResolver = (*Handler)(nil)
type Handler struct {
	manager *Manager
	logger  *slog.Logger
}

// NewCallbackHandler returns a Handler that resolves approval callbacks via m.
func NewCallbackHandler(m *Manager, logger *slog.Logger) *Handler {
	return &Handler{manager: m, logger: logger}
}

// Resolve maps a Telegram callback data string to a human-readable response.
// Returns ("", nil) for unrecognised callbacks (not approval-related).
// The returned string is suitable for sending directly to the user.
func (h *Handler) Resolve(ctx context.Context, data string) (string, error) {
	if !strings.HasPrefix(data, "appr:") {
		return "", nil // not an approval callback, ignore
	}

	resolved, err := h.manager.ResolveByCallback(ctx, data, "telegram")
	if err != nil {
		switch err {
		case ErrNotFound:
			h.logger.Warn("callback for unknown approval", "data", data)
			return "", nil
		case ErrStaleCallback:
			if resolved != nil {
				switch resolved.Status {
				case StatusExpired:
					return "⏰ This approval request has expired.", nil
				case StatusApproved:
					return "✅ Already approved: " + resolved.Summary, nil
				case StatusDenied:
					return "❌ Already denied: " + resolved.Summary, nil
				}
			}
			return "⚠️ This approval request is no longer pending.", nil
		default:
			return fmt.Sprintf("Error processing request: %v", err), err
		}
	}

	_, action, _ := parseCallback(data)
	switch action {
	case CallbackApproveSession:
		return "✅ Approved (auto-approve for 15 min): " + resolved.Summary, nil
	case CallbackApproveAlways:
		return "✅ Approved (auto-approve always): " + resolved.Summary, nil
	case CallbackApprove:
		return "✅ Approved: " + resolved.Summary, nil
	default:
		return "❌ Denied: " + resolved.Summary, nil
	}
}
