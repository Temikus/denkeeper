package api

import (
	"encoding/json"
	"fmt"

	"github.com/Temikus/denkeeper/internal/agent"
)

// --- Client → Server frame types ---

// WSClientFrame is the envelope used to determine the type of a client frame.
type WSClientFrame struct {
	Type string `json:"type"`
}

// ChatRequestFrame is sent by the client to start or continue a chat session.
type ChatRequestFrame struct {
	Type           string `json:"type"`                       // "chat_request"
	SessionID      string `json:"session_id,omitempty"`       // omit to create new session
	Agent          string `json:"agent,omitempty"`            // agent name; defaults to "default"
	Message        string `json:"message"`                    // user message text
	UserID         string `json:"user_id,omitempty"`          // optional user identifier
	UserName       string `json:"user_name,omitempty"`        // optional display name
	Seq            int64  `json:"seq,omitempty"`              // monotonically increasing per session
	ResumeAfterSeq int64  `json:"resume_after_seq,omitempty"` // replay events after this seq on reconnect
}

// ApprovalResponseFrame is sent by the client to resolve a pending tool approval.
type ApprovalResponseFrame struct {
	Type       string `json:"type"`        // "approval_response"
	ApprovalID string `json:"approval_id"` // approval request ID
	Action     string `json:"action"`      // "approve", "deny", "auto_session", "auto_always"
}

// CancelFrame is sent by the client to abort an in-progress agent turn.
type CancelFrame struct {
	Type      string `json:"type"`       // "cancel"
	SessionID string `json:"session_id"` // session to cancel
}

// PongFrame is the client's keepalive response to a server ping.
type PongFrame struct {
	Type string `json:"type"` // "pong"
}

// --- Server → Client frame types ---

// WSEventFrame wraps a ChatEvent with session routing and sequence info.
type WSEventFrame struct {
	agent.ChatEvent
	SessionID string `json:"session_id,omitempty"` // session this event belongs to
	Seq       int64  `json:"seq,omitempty"`        // monotonically increasing per session
}

// PingFrame is the server's keepalive probe.
type PingFrame struct {
	Type string `json:"type"` // "ping"
}

// WSErrorFrame reports an error for a specific session without closing the connection.
type WSErrorFrame struct {
	Type      string `json:"type"`                 // "error"
	SessionID string `json:"session_id,omitempty"` // session this error relates to
	Code      string `json:"code"`                 // "rate_limited", "permission_denied", "agent_not_found", "internal", "replay_unavailable"
	Message   string `json:"message"`              // human-readable detail
}

// ActivityFrame notifies WebSocket clients of new activity on a conversation
// from another adapter (e.g. a Telegram message was processed). It is broadcast
// to all connected clients so the web UI can refresh its session list or
// reload the active conversation.
type ActivityFrame struct {
	Type           string `json:"type"`            // "activity"
	ConversationID string `json:"conversation_id"` // e.g. "default:telegram:12345"
	Agent          string `json:"agent"`           // agent name
	Adapter        string `json:"adapter"`         // source adapter name
	Summary        string `json:"summary"`         // brief description
}

// PanicFrame is sent by the client to trigger an emergency stop.
type PanicFrame struct {
	Type string `json:"type"` // "panic"
}

// PanicStatusFrame is broadcast by the server to notify clients of panic state.
type PanicStatusFrame struct {
	Type    string `json:"type"`    // "panic_status"
	Active  bool   `json:"active"`  // true = panicked, false = resumed
	Message string `json:"message"` // human-readable description
}

// --- Frame type constants ---

const (
	FrameTypeChatRequest      = "chat_request"
	FrameTypeApprovalResponse = "approval_response"
	FrameTypeCancel           = "cancel"
	FrameTypePong             = "pong"
	FrameTypePing             = "ping"
	FrameTypeError            = "error"
	FrameTypeActivity         = "activity"
	FrameTypePanic            = "panic"
	FrameTypePanicStatus      = "panic_status"
)

// validateApprovalResponseFrame checks required fields and action validity.
func validateApprovalResponseFrame(f ApprovalResponseFrame) error {
	if f.ApprovalID == "" {
		return fmt.Errorf("wsframes: approval_response requires approval_id")
	}
	if f.Action == "" {
		return fmt.Errorf("wsframes: approval_response requires action")
	}
	switch f.Action {
	case "approve", "deny", "auto_session", "auto_always":
		return nil
	default:
		return fmt.Errorf("wsframes: invalid approval action %q", f.Action)
	}
}

// ParseClientFrame reads a raw JSON message from a WebSocket client and
// returns the appropriate typed frame. Returns an error for unknown or
// malformed frames.
func ParseClientFrame(data []byte) (any, error) {
	var envelope WSClientFrame
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("wsframes: invalid JSON: %w", err)
	}

	switch envelope.Type {
	case FrameTypeChatRequest:
		var f ChatRequestFrame
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, fmt.Errorf("wsframes: invalid chat_request: %w", err)
		}
		if f.Message == "" && f.ResumeAfterSeq == 0 {
			return nil, fmt.Errorf("wsframes: chat_request requires message or resume_after_seq")
		}
		return f, nil

	case FrameTypeApprovalResponse:
		var f ApprovalResponseFrame
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, fmt.Errorf("wsframes: invalid approval_response: %w", err)
		}
		if err := validateApprovalResponseFrame(f); err != nil {
			return nil, err
		}
		return f, nil

	case FrameTypeCancel:
		var f CancelFrame
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, fmt.Errorf("wsframes: invalid cancel: %w", err)
		}
		if f.SessionID == "" {
			return nil, fmt.Errorf("wsframes: cancel requires session_id")
		}
		return f, nil

	case FrameTypePong:
		return PongFrame{Type: FrameTypePong}, nil

	case FrameTypePanic:
		return PanicFrame{Type: FrameTypePanic}, nil

	case "":
		return nil, fmt.Errorf("wsframes: missing type field")

	default:
		return nil, fmt.Errorf("wsframes: unknown frame type %q", envelope.Type)
	}
}
