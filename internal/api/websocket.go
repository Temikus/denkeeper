package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/approval"
)

const (
	// wsPingInterval is how often the server sends ping frames.
	wsPingInterval = 30 * time.Second

	// wsPongTimeout is how long the server waits for a pong reply.
	wsPongTimeout = 10 * time.Second

	// wsWriteTimeout is the deadline for writing a single message.
	wsWriteTimeout = 10 * time.Second

	// wsMaxMessageSize is the maximum incoming frame size (64 KB).
	wsMaxMessageSize = 64 * 1024

	// wsSendBufferSize is the capacity of the outbound message channel.
	wsSendBufferSize = 256

	// wsMaxConcurrentChats is the maximum number of concurrent chat
	// goroutines per WebSocket connection. Prevents DoS via chat spam.
	wsMaxConcurrentChats = 10
)

// upgrader is the gorilla/websocket upgrader shared across connections.
// CheckOrigin is overridden per-request in handleWebSocket.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(_ *http.Request) bool { return true }, // overridden at upgrade time
}

// ---------------------------------------------------------------------------
// WSHub — manages all active WebSocket connections
// ---------------------------------------------------------------------------

// WSHub tracks active WebSocket connections and provides graceful shutdown.
type WSHub struct {
	conns       map[*WSConn]struct{}
	mu          sync.Mutex
	maxConns    int
	replayStore *ReplayStore
	logger      *slog.Logger
}

// NewWSHub creates a hub. maxConns=0 means unlimited.
func NewWSHub(maxConns int, replayTTL time.Duration, logger *slog.Logger) *WSHub {
	return &WSHub{
		conns:       make(map[*WSConn]struct{}),
		maxConns:    maxConns,
		replayStore: NewReplayStore(defaultReplayCapacity, replayTTL),
		logger:      logger,
	}
}

// Register adds a connection. Returns false if the hub is at capacity.
func (h *WSHub) Register(c *WSConn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.maxConns > 0 && len(h.conns) >= h.maxConns {
		return false
	}
	h.conns[c] = struct{}{}
	return true
}

// Unregister removes a connection from the hub.
func (h *WSHub) Unregister(c *WSConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, c)
}

// Shutdown closes all active connections gracefully.
func (h *WSHub) Shutdown() {
	h.mu.Lock()
	conns := make([]*WSConn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()

	for _, c := range conns {
		c.Close(websocket.CloseGoingAway, "server shutting down")
	}
}

// StartCleanup launches a background goroutine that periodically removes
// stale replay buffers. It stops when ctx is cancelled.
func (h *WSHub) StartCleanup(ctx context.Context) {
	ttl := h.replayStore.ttl
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(ttl)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.replayStore.Cleanup()
			}
		}
	}()
}

// ConnCount returns the current number of active connections.
func (h *WSHub) ConnCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}

// Broadcast sends a JSON frame to all connected WebSocket clients.
// Frames are sent on a best-effort basis; slow clients may miss frames
// because sendJSON drops non-critical frames when the send buffer is full.
func (h *WSHub) Broadcast(frame any) {
	h.mu.Lock()
	conns := make([]*WSConn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()

	for _, c := range conns {
		c.sendJSON(frame)
	}
}

// ---------------------------------------------------------------------------
// WSConn — a single WebSocket connection
// ---------------------------------------------------------------------------

// WSConn wraps a gorilla/websocket connection with read/write pump goroutines.
type WSConn struct {
	conn   *websocket.Conn
	hub    *WSHub
	server *Server

	keyName string // authenticated identity

	send chan []byte   // outbound message queue
	done chan struct{} // closed when connection is shutting down
	once sync.Once     // ensures done is closed exactly once

	// connCtx is cancelled when the connection closes. All chat goroutines
	// should derive their context from this so they stop when the client
	// disconnects.
	connCtx    context.Context
	connCancel context.CancelFunc

	// sessions tracks in-flight chat sessions on this connection.
	// Key: sessionID, Value: context.CancelFunc.
	sessions sync.Map

	// chatSem limits the number of concurrent chat goroutines per connection.
	chatSem chan struct{}

	// seqCounter provides monotonically increasing sequence numbers.
	seqCounter atomic.Int64
}

func newWSConn(conn *websocket.Conn, hub *WSHub, server *Server, keyName string) *WSConn {
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // G118: cancel is called in WSConn.Close
	return &WSConn{
		conn:       conn,
		hub:        hub,
		server:     server,
		keyName:    keyName,
		send:       make(chan []byte, wsSendBufferSize),
		done:       make(chan struct{}),
		connCtx:    ctx,
		connCancel: cancel,
		chatSem:    make(chan struct{}, wsMaxConcurrentChats),
	}
}

// Start spawns the read and write pump goroutines.
func (c *WSConn) Start() {
	go c.writePump()
	go c.readPump()
}

// Close sends a close frame and shuts down the connection.
func (c *WSConn) Close(code int, text string) {
	c.once.Do(func() {
		close(c.done)
		// Cancel the connection-level context so all in-flight chat
		// goroutines stop when the client disconnects.
		c.connCancel()
	})
	msg := websocket.FormatCloseMessage(code, text)
	_ = c.conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(wsWriteTimeout))
	_ = c.conn.Close()
	c.hub.Unregister(c)

	// Cancel all in-flight sessions (belt-and-suspenders — connCancel above
	// already cancels parent, but per-session cancels are still useful for
	// the sessions.Delete cleanup path).
	c.sessions.Range(func(_, val any) bool {
		if cancel, ok := val.(context.CancelFunc); ok {
			cancel()
		}
		return true
	})
}

// sendJSON marshals v as JSON and queues it for the write pump.
// Protocol-critical frames (done, error) block until queued to prevent
// the client from missing operation completion signals.
func (c *WSConn) sendJSON(v any) {
	// Check if connection is already closing — send on a closed channel panics.
	select {
	case <-c.done:
		return
	default:
	}

	data, err := json.Marshal(v)
	if err != nil {
		c.hub.logger.Error("ws: marshal error", "error", err)
		return
	}

	// Determine if the frame is protocol-critical (must not be dropped).
	critical := false
	frameType := "unknown"
	switch f := v.(type) {
	case WSEventFrame:
		frameType = f.Type
		critical = f.Type == "done" || f.Type == "error"
	case WSErrorFrame:
		frameType = "error"
		critical = true
	}

	if critical {
		// Block until the frame is queued or the connection closes.
		select {
		case c.send <- data:
		case <-c.done:
		}
		return
	}

	select {
	case c.send <- data:
	case <-c.done:
	default:
		// Send buffer full — drop the non-critical message to avoid blocking.
		c.hub.logger.Warn("ws: send buffer full, dropping message",
			"key", c.keyName,
			"frame_type", frameType,
			"queue_depth", len(c.send),
		)
	}
}

// sendError sends an error frame for a specific session.
func (c *WSConn) sendError(sessionID, code, message string) {
	c.sendJSON(WSErrorFrame{
		Type:      FrameTypeError,
		SessionID: sessionID,
		Code:      code,
		Message:   message,
	})
}

// nextSeq returns the next sequence number for event framing.
func (c *WSConn) nextSeq() int64 {
	return c.seqCounter.Add(1)
}

// ---------------------------------------------------------------------------
// Read pump — reads client frames and dispatches them
// ---------------------------------------------------------------------------

func (c *WSConn) readPump() {
	defer c.Close(websocket.CloseNormalClosure, "")

	c.conn.SetReadLimit(wsMaxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPingInterval + wsPongTimeout))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(wsPingInterval + wsPongTimeout))
		return nil
	})

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				c.hub.logger.Warn("ws: read error", "error", err, "key", c.keyName)
			}
			return
		}

		frame, parseErr := ParseClientFrame(data)
		if parseErr != nil {
			c.sendError("", "invalid_frame", parseErr.Error())
			continue
		}

		switch f := frame.(type) {
		case ChatRequestFrame:
			c.handleChatRequest(f)
		case ApprovalResponseFrame:
			c.handleApprovalResponse(f)
		case CancelFrame:
			c.handleCancel(f)
		case PongFrame:
			// Already handled by the pong handler; this is the JSON-level pong.
		}
	}
}

// ---------------------------------------------------------------------------
// Write pump — drains the send channel and sends ping frames
// ---------------------------------------------------------------------------

func (c *WSConn) writePump() {
	ticker := time.NewTicker(wsPingInterval)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
			// Drain queued messages to coalesce writes.
			n := len(c.send)
			for i := 0; i < n; i++ {
				_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
				if err := c.conn.WriteMessage(websocket.TextMessage, <-c.send); err != nil {
					return
				}
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-c.done:
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Frame handlers
// ---------------------------------------------------------------------------

func (c *WSConn) handleChatRequest(f ChatRequestFrame) {
	agentName := f.Agent
	if agentName == "" {
		agentName = "default"
	}
	if len(f.Message) > maxChatMessageLen {
		c.sendError(f.SessionID, "invalid_frame",
			fmt.Sprintf("message exceeds maximum length of %d bytes", maxChatMessageLen))
		return
	}
	eng := c.server.deps.Dispatcher.Agent(agentName)
	if eng == nil {
		c.sendError(f.SessionID, "agent_not_found", fmt.Sprintf("agent %q not found", agentName))
		return
	}

	sessionID := f.SessionID
	if sessionID == "" {
		sessionID = generateID()
	}

	// Handle replay-only reconnect.
	if f.ResumeAfterSeq > 0 && f.Message == "" {
		buf := c.hub.replayStore.Buffer(sessionID)
		frames := buf.ReplaySince(f.ResumeAfterSeq)
		if len(frames) == 0 {
			c.sendError(sessionID, "replay_unavailable", "replay buffer expired or empty")
			return
		}
		for _, ef := range frames {
			c.sendJSON(ef)
		}
		return
	}

	msg := adapter.IncomingMessage{
		Adapter:        "ws",
		ExternalID:     sessionID,
		UserID:         f.UserID,
		UserName:       f.UserName,
		Text:           f.Message,
		Timestamp:      time.Now(),
		ConversationID: sessionID,
	}

	// Limit concurrent chat goroutines per connection.
	select {
	case c.chatSem <- struct{}{}:
	default:
		c.sendError(sessionID, "rate_limited", "too many concurrent chat requests")
		return
	}

	// Derive from the connection context so processing stops when the
	// client disconnects.
	ctx, cancel := context.WithCancel(c.connCtx)
	c.sessions.Store(sessionID, cancel)

	// Run the chat pipeline in a goroutine so the read pump stays free.
	go func() {
		defer func() {
			<-c.chatSem
			c.sessions.Delete(sessionID)
			cancel()
		}()

		replayBuf := c.hub.replayStore.Buffer(sessionID)

		onEvent := func(evt agent.ChatEvent) {
			seq := c.nextSeq()
			ef := WSEventFrame{
				ChatEvent: evt,
				SessionID: sessionID,
				Seq:       seq,
			}
			replayBuf.Append(ef)
			c.sendJSON(ef)
		}

		responseText, err := eng.ChatWithEvents(ctx, msg, onEvent)
		if err != nil {
			if ctx.Err() != nil {
				// Cancelled — don't send error.
				return
			}
			c.hub.logger.Error("ws: chat error", "error", err, "session", sessionID)
			c.sendError(sessionID, "internal", "failed to process message")
			return
		}

		// Send content and done frames.
		contentSeq := c.nextSeq()
		contentFrame := WSEventFrame{
			ChatEvent: agent.ChatEvent{Type: "content", Text: responseText},
			SessionID: sessionID,
			Seq:       contentSeq,
		}
		replayBuf.Append(contentFrame)
		c.sendJSON(contentFrame)

		doneSeq := c.nextSeq()
		doneFrame := WSEventFrame{
			ChatEvent: agent.ChatEvent{Type: "done"},
			SessionID: sessionID,
			Seq:       doneSeq,
		}
		replayBuf.Append(doneFrame)
		c.sendJSON(doneFrame)
	}()
}

func (c *WSConn) handleApprovalResponse(f ApprovalResponseFrame) {
	if c.server.deps.Approvals == nil {
		c.sendError("", "internal", "approval manager not configured")
		return
	}

	approved := f.Action == "approve" || f.Action == "auto_session" || f.Action == "auto_always"

	resolveCtx, resolveCancel := context.WithTimeout(c.connCtx, 30*time.Second)
	defer resolveCancel()
	resolved, err := c.server.deps.Approvals.Resolve(resolveCtx, f.ApprovalID, approved, "ws")
	if err != nil {
		switch err {
		case approval.ErrNotFound:
			c.sendError("", "not_found", "approval not found")
		case approval.ErrAlreadyResolved:
			c.sendError("", "already_resolved", "approval already resolved")
		default:
			c.hub.logger.Error("ws: resolving approval", "id", f.ApprovalID, "error", err)
			c.sendError("", "internal", "failed to resolve approval")
		}
		return
	}

	// Create auto-approve rule if requested.
	if approved && resolved.Kind == approval.ActionKindToolCall {
		toolName := approval.ExtractToolName(resolved.Summary)
		if toolName != "" {
			switch f.Action {
			case "auto_session":
				c.server.deps.Approvals.AddSessionRule(resolved.AgentName, toolName, resolved.ConversationID, "ws")
			case "auto_always":
				if _, aaErr := c.server.deps.Approvals.AddPermanentRule(resolveCtx, resolved.AgentName, toolName, "ws"); aaErr != nil {
					c.hub.logger.Error("ws: creating auto-approve rule", "error", aaErr)
				}
			}
		}
	}

	// Notify the originating adapter channel in a goroutine so a slow
	// adapter doesn't block the readPump from processing other frames.
	action := "Denied"
	if approved {
		action = "Approved"
	}
	notifyMsg := fmt.Sprintf("%s via WebSocket: %s", action, resolved.Summary)
	go func() {
		notifyCtx, notifyCancel := context.WithTimeout(c.connCtx, 10*time.Second)
		defer notifyCancel()
		if err := c.server.deps.Dispatcher.SendVia(notifyCtx, resolved.AdapterName, adapter.OutgoingMessage{
			ExternalID: resolved.ExternalID,
			Text:       notifyMsg,
		}); err != nil {
			c.hub.logger.Warn("ws: failed to send approval notification", "id", f.ApprovalID, "error", err)
		}
	}()
}

func (c *WSConn) handleCancel(f CancelFrame) {
	if val, ok := c.sessions.Load(f.SessionID); ok {
		if cancel, ok := val.(context.CancelFunc); ok {
			cancel()
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP upgrade handler
// ---------------------------------------------------------------------------

// handleWebSocket handles GET /api/v1/ws — upgrades the HTTP connection to
// a WebSocket and spawns read/write pump goroutines.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("ws: upgrade request received", "remote", r.RemoteAddr, "upgrade", r.Header.Get("Upgrade"))

	// Authenticate: Authorization header, ?token= query param, or session cookie.
	scope := "chat"
	keyName := ""

	// Try ?token= query param first (browsers can't set custom WS headers).
	hasToken := false
	if token := r.URL.Query().Get("token"); token != "" {
		// Temporarily set the Authorization header so authenticate() works.
		r.Header.Set("Authorization", "Bearer "+token)
		hasToken = true
	}

	name, ok := s.authenticate(r.Context(), r, scope)

	// Redact the token from the URL to prevent it from appearing in logs.
	if hasToken {
		q := r.URL.Query()
		q.Del("token")
		r.URL.RawQuery = q.Encode()
	}

	if !ok {
		s.logger.Debug("ws: auth failed", "remote", r.RemoteAddr, "has_cookie", r.Header.Get("Cookie") != "", "has_token", hasToken)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	keyName = name

	// Validate Origin against CORS allowed_origins (if configured).
	if len(s.cfg.CORSOrigins) > 0 {
		origin := r.Header.Get("Origin")
		allowed := false
		for _, o := range s.cfg.CORSOrigins {
			if o == origin {
				allowed = true
				break
			}
		}
		if !allowed && origin != "" {
			http.Error(w, "Forbidden: origin not allowed", http.StatusForbidden)
			return
		}
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("ws: upgrade failed", "error", err, "key", keyName)
		return
	}

	wsc := newWSConn(conn, s.wsHub, s, keyName)

	if !s.wsHub.Register(wsc) {
		s.logger.Warn("ws: max connections reached, rejecting", "key", keyName)
		wsc.Close(websocket.CloseTryAgainLater, "max connections reached")
		return
	}

	s.logger.Info("ws: client connected", "key", keyName, "remote", conn.RemoteAddr())
	wsc.Start()
}
