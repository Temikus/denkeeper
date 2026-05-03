package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/llm"
)

// StreamSession abstracts the transport used to deliver chat events.
// Both SSE and WebSocket implement this interface.
type StreamSession interface {
	// SendEvent streams an intermediate pipeline event to the client.
	SendEvent(evt agent.ChatEvent)

	// SendContent streams the final response text.
	SendContent(text string)

	// SendDone signals that the agent turn is complete.
	SendDone(sessionID string)

	// SendError reports a fatal error for the stream.
	SendError(message string)
}

// ---------------------------------------------------------------------------
// SSE implementation
// ---------------------------------------------------------------------------

// SSEStreamSession writes Server-Sent Events to an http.ResponseWriter.
type SSEStreamSession struct {
	w     http.ResponseWriter
	flush func()
}

// NewSSEStreamSession creates an SSE session. The caller must set SSE headers
// and write the status code before calling this.
func NewSSEStreamSession(w http.ResponseWriter) *SSEStreamSession {
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	return &SSEStreamSession{w: w, flush: flush}
}

func (s *SSEStreamSession) writeEvent(data any) {
	b, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(s.w, "data: %s\n\n", b)
	s.flush()
}

func (s *SSEStreamSession) SendEvent(evt agent.ChatEvent) {
	s.writeEvent(evt)
}

func (s *SSEStreamSession) SendContent(text string) {
	s.writeEvent(map[string]string{"type": "content", "text": text})
}

func (s *SSEStreamSession) SendDone(sessionID string) {
	s.writeEvent(map[string]string{"type": "done", "session_id": sessionID})
}

func (s *SSEStreamSession) SendError(message string) {
	s.writeEvent(map[string]string{"type": "error", "message": message})
}

// ---------------------------------------------------------------------------
// Shared chat pipeline runner
// ---------------------------------------------------------------------------

// runChatStream invokes the engine pipeline and routes events through the
// given StreamSession. This is the shared implementation used by both the
// SSE handler and the WebSocket handler.
func (s *Server) runChatStream(ctx context.Context, stream StreamSession, eng *agent.Engine, msg adapter.IncomingMessage, sessionID string) {
	onEvent := func(evt agent.ChatEvent) {
		stream.SendEvent(evt)
	}

	responseText, err := eng.ChatWithEvents(ctx, msg, onEvent)
	if err != nil {
		if ctx.Err() != nil {
			// Client disconnected — don't attempt to send error.
			s.logger.Info("chat stream cancelled (client disconnected)", "session", sessionID)
			return
		}
		s.logger.Error("chat stream error", "error", err, "session", sessionID)
		stream.SendError(llm.UserFacingError(err))
		return
	}

	// Always send the final content frame with the full text. Clients that
	// already received content_delta events use the content frame to verify
	// completeness; clients that don't support deltas use it as the response.
	stream.SendContent(responseText)
	stream.SendDone(sessionID)
}
