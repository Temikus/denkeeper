package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/crypto/bcrypt"

	"github.com/Temikus/denkeeper/internal/tool"
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
// A limit of 0 disables rate limiting (always allows).
func (rl *loginRateLimiter) allow(ip string) bool {
	if rl.limit <= 0 {
		return true
	}

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
	if err := s.sessions.CreateWithRequest(w, r, sess); err != nil {
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

// handleListSessions returns all active sessions for the authenticated user.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil || s.sessions.Store == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server-tracked sessions not enabled"})
		return
	}

	sess, err := s.sessions.Read(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid session"})
		return
	}

	records, err := s.sessions.Store.ListByEmail(r.Context(), sess.Email)
	if err != nil {
		s.logger.Error("listing sessions", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list sessions"})
		return
	}

	writeJSON(w, http.StatusOK, records)
}

// handleRevokeSession revokes a single session by ID.
func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil || s.sessions.Store == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server-tracked sessions not enabled"})
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session ID required"})
		return
	}

	if err := s.sessions.Store.Delete(r.Context(), id); err != nil {
		s.logger.Error("revoking session", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to revoke session"})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleRevokeAllSessions revokes all sessions for the authenticated user.
func (s *Server) handleRevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil || s.sessions.Store == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server-tracked sessions not enabled"})
		return
	}

	sess, err := s.sessions.Read(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid session"})
		return
	}

	count, err := s.sessions.Store.DeleteAllByEmail(r.Context(), sess.Email)
	if err != nil {
		s.logger.Error("revoking all sessions", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to revoke sessions"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"revoked": count})
}

// handleAuthStatus returns a summary of auth configuration.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"password_enabled":   s.passwordHash != "",
		"oidc_enabled":       s.oidcProvider != nil,
		"sessions_trackable": s.sessions != nil && s.sessions.Store != nil,
	}

	if s.sessions != nil && s.sessions.Store != nil {
		count, err := s.sessions.Store.Count(r.Context())
		if err == nil {
			resp["active_session_count"] = count
		}
	}

	// OIDC details.
	if s.deps.Config != nil {
		resp["oidc_issuer"] = s.deps.Config.API.Auth.OIDC.Issuer
		resp["oidc_allowed_emails"] = s.deps.Config.API.Auth.OIDC.AllowedEmails

		pref := s.deps.Config.API.Auth.PreferredLoginMethod
		if pref == "" {
			pref = "auto"
		}
		resp["preferred_login_method"] = pref
	}

	// API key count.
	if s.deps.KeyStore != nil {
		keys, err := s.deps.KeyStore.List(r.Context())
		if err == nil {
			resp["api_keys_count"] = len(keys)
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// handlePasswordChange allows an authenticated admin to change the dashboard password.
func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	if s.passwordHash == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "password login not configured"})
		return
	}

	// Rate limiting (reuse login limiter).
	ip := clientIP(r)
	if !s.loginLimiter.allow(ip) {
		retryAfter := s.loginLimiter.retryAfter(ip)
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts"})
		return
	}

	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(s.passwordHash), []byte(input.CurrentPassword)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid current password"})
		return
	}

	if len(input.NewPassword) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new password must be at least 8 characters"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), 13)
	if err != nil {
		s.logger.Error("hashing new password", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
		return
	}

	if s.deps.ConfigPath != "" {
		if err := tool.UpdateAuthConfig(s.deps.ConfigPath, map[string]any{"password_hash": string(hash)}); err != nil {
			s.logger.Error("persisting password change", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save configuration"})
			return
		}
	}

	s.passwordHash = string(hash)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleOIDCTest performs a fresh OIDC discovery against the configured issuer.
func (s *Server) handleOIDCTest(w http.ResponseWriter, r *http.Request) {
	if s.oidcProvider == nil || s.deps.Config == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "OIDC not configured"})
		return
	}

	issuer := s.deps.Config.API.Auth.OIDC.Issuer
	if issuer == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "OIDC issuer not configured"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":    false,
			"error": fmt.Sprintf("discovery failed: %v", err),
		})
		return
	}

	endpoint := provider.Endpoint()

	// Extract userinfo_endpoint from discovery document.
	var disc struct {
		UserInfo string `json:"userinfo_endpoint"`
	}
	_ = provider.Claims(&disc)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"issuer": issuer,
		"endpoints": map[string]string{
			"authorization": endpoint.AuthURL,
			"token":         endpoint.TokenURL,
			"userinfo":      disc.UserInfo,
		},
	})
}

// handleAuthPreferences updates the preferred login method.
func (s *Server) handleAuthPreferences(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PreferredLoginMethod string `json:"preferred_login_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	switch input.PreferredLoginMethod {
	case "auto", "password", "apikey":
		// valid
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "preferred_login_method must be one of: auto, password, apikey",
		})
		return
	}

	if s.deps.ConfigPath != "" {
		if err := tool.UpdateAuthConfig(s.deps.ConfigPath, map[string]any{
			"preferred_login_method": input.PreferredLoginMethod,
		}); err != nil {
			s.logger.Error("persisting auth preferences", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save configuration"})
			return
		}
	}

	if s.deps.Config != nil {
		s.deps.Config.API.Auth.PreferredLoginMethod = input.PreferredLoginMethod
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
