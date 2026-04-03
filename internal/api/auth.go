package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// rateBucket tracks login attempts for a single IP.
type rateBucket struct {
	count   int
	resetAt time.Time
}

// loginRateLimiter provides IP-based rate limiting for password login.
type loginRateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*rateBucket
	limit    int
	window   time.Duration
	stopOnce sync.Once
	stopCh   chan struct{}
}

func newLoginRateLimiter(limit int, window time.Duration) *loginRateLimiter {
	rl := &loginRateLimiter{
		buckets: make(map[string]*rateBucket),
		limit:   limit,
		window:  window,
		stopCh:  make(chan struct{}),
	}
	go rl.cleanup()
	return rl
}

// allow returns true if the IP has not exceeded the rate limit.
func (rl *loginRateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[ip]
	if !ok || time.Now().After(b.resetAt) {
		rl.buckets[ip] = &rateBucket{count: 1, resetAt: time.Now().Add(rl.window)}
		return true
	}
	b.count++
	return b.count <= rl.limit
}

// retryAfter returns seconds until the rate limit window resets for this IP.
func (rl *loginRateLimiter) retryAfter(ip string) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[ip]
	if !ok {
		return 0
	}
	secs := int(time.Until(b.resetAt).Seconds())
	if secs < 1 {
		return 1
	}
	return secs
}

func (rl *loginRateLimiter) cleanup() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for ip, b := range rl.buckets {
				if now.After(b.resetAt) {
					delete(rl.buckets, ip)
				}
			}
			rl.mu.Unlock()
		case <-rl.stopCh:
			return
		}
	}
}

func (rl *loginRateLimiter) stop() {
	rl.stopOnce.Do(func() { close(rl.stopCh) })
}

// handleAuthConfig returns which auth methods are available (no auth required).
func (s *Server) handleAuthConfig(w http.ResponseWriter, _ *http.Request) {
	resp := map[string]bool{
		"password_enabled": s.passwordHash != "",
		"oidc_enabled":     s.oidcProvider != nil,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// handlePasswordLogin authenticates with bcrypt password and creates a session cookie.
func (s *Server) handlePasswordLogin(w http.ResponseWriter, r *http.Request) {
	if s.passwordHash == "" {
		http.Error(w, `{"error":"password login not configured"}`, http.StatusNotFound)
		return
	}

	// Rate limiting.
	ip := clientIP(r)
	if !s.loginLimiter.allow(ip) {
		retryAfter := s.loginLimiter.retryAfter(ip)
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		s.logger.Warn("login rate limited", "ip", ip)
		http.Error(w, `{"error":"too many login attempts"}`, http.StatusTooManyRequests)
		return
	}

	// CSRF: verify Origin header.
	if origin := r.Header.Get("Origin"); origin != "" {
		if !s.isValidOrigin(origin) {
			s.logger.Warn("login CSRF check failed", "ip", ip, "origin", origin)
			http.Error(w, `{"error":"origin not allowed"}`, http.StatusForbidden)
			return
		}
	}

	var input struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(s.passwordHash), []byte(input.Password)); err != nil {
		s.logger.Warn("failed login attempt", "ip", ip)
		http.Error(w, `{"error":"invalid password"}`, http.StatusUnauthorized)
		return
	}

	if s.sessions == nil {
		http.Error(w, `{"error":"session manager not configured"}`, http.StatusInternalServerError)
		return
	}

	sess := Session{
		Email:  "admin",
		Scopes: adminScopes(),
	}
	if err := s.sessions.Create(w, sess); err != nil {
		s.logger.Error("creating session", "error", err)
		http.Error(w, `{"error":"failed to create session"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "email": sess.Email}) //nolint:errcheck
}

// handleLogout clears the session cookie.
func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	if s.sessions != nil {
		s.sessions.Clear(w)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
}

// handleSessionCheck verifies the current session cookie.
func (s *Server) handleSessionCheck(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		json.NewEncoder(w).Encode(map[string]any{"authenticated": false}) //nolint:errcheck
		return
	}

	sess, err := s.sessions.Read(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"authenticated": false}) //nolint:errcheck
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "email": sess.Email}) //nolint:errcheck
}

// adminScopes returns the full set of scopes for a dashboard password/OIDC login.
func adminScopes() []string {
	return []string{
		"admin", "chat", "sessions:read", "costs:read",
		"skills:read", "skills:write", "schedules:read", "schedules:write",
		"approvals:read", "approvals:write", "tools:read", "tools:write",
		"browser:read", "browser:write", "kv:read", "kv:write",
		"agents:read", "agents:write",
	}
}

// clientIP extracts the client IP from the request.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-Ip"); xri != "" {
		return xri
	}
	// Strip port from RemoteAddr.
	ip := r.RemoteAddr
	if i := strings.LastIndex(ip, ":"); i > 0 {
		ip = ip[:i]
	}
	return ip
}

// isValidOrigin checks if the origin is allowed by CORS config or matches the server host.
func (s *Server) isValidOrigin(origin string) bool {
	for _, o := range s.cfg.CORSOrigins {
		if o == "*" || o == origin {
			return true
		}
	}
	// If no CORS origins are configured, allow same-origin (origin matches listen addr).
	return len(s.cfg.CORSOrigins) == 0
}
