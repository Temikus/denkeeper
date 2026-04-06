package api

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"time"

	"github.com/Temikus/denkeeper/internal/tool"
	"github.com/Temikus/denkeeper/internal/tool/oauth"
)

// OAuthDeps holds OAuth-specific dependencies for the API server.
type OAuthDeps struct {
	TokenStore *oauth.TokenStore
	PendingMgr *oauth.PendingManager
}

// handleOAuthCallback handles the OAuth provider redirect after user authorization.
// This endpoint requires NO authentication (browser redirect from external provider).
func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if s.deps.OAuthDeps == nil || s.deps.OAuthDeps.PendingMgr == nil {
		s.serveCallbackHTML(w, false, "OAuth support is not configured")
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		errMsg := r.URL.Query().Get("error")
		errDesc := r.URL.Query().Get("error_description")
		if errMsg != "" {
			s.serveCallbackHTML(w, false, fmt.Sprintf("Authorization denied: %s — %s", errMsg, errDesc))
		} else {
			s.serveCallbackHTML(w, false, "Missing code or state parameter")
		}
		return
	}

	if err := s.deps.OAuthDeps.PendingMgr.CompleteByState(state, code); err != nil {
		s.logger.Warn("oauth: callback failed",
			slog.String("error", err.Error()))
		s.serveCallbackHTML(w, false, "Authorization failed. Please close this window and try again from the dashboard.")
		return
	}

	s.logger.Info("oauth: callback completed successfully")
	s.serveCallbackHTML(w, true, "")
}

// handleToolOAuthStatus returns the OAuth token status for a tool.
func (s *Server) handleToolOAuthStatus(w http.ResponseWriter, r *http.Request) {
	if s.deps.OAuthDeps == nil || s.deps.OAuthDeps.TokenStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "OAuth support is not configured"})
		return
	}

	name := r.PathValue("name")
	stored, err := s.deps.OAuthDeps.TokenStore.Get(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if stored == nil {
		writeJSON(w, http.StatusOK, oauth.TokenSummary{
			HasToken:    false,
			NeedsReauth: true,
		})
		return
	}

	writeJSON(w, http.StatusOK, stored.Summary())
}

// handleToolOAuthConnect initiates an OAuth flow for a tool.
// It triggers tool reconnection which creates a pending auth, then returns
// the auth URL for the UI to open in a popup.
func (s *Server) handleToolOAuthConnect(w http.ResponseWriter, r *http.Request) {
	if s.deps.OAuthDeps == nil || s.deps.OAuthDeps.PendingMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "OAuth support is not configured"})
		return
	}
	if !s.lifecycleRequired(w) {
		return
	}

	name := r.PathValue("name")
	mgr := s.deps.LifecycleMgr.ToolManager()

	s.logger.Debug("oauth: connect requested",
		slog.String("tool", name))

	info, ok := mgr.ServerInfo(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tool not found"})
		return
	}
	if info.AuthType != "oauth" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tool does not use OAuth authentication"})
		return
	}

	s.logger.Debug("oauth: tool info retrieved",
		slog.String("tool", name),
		slog.String("status", info.Status),
		slog.Bool("has_token", info.OAuthStatus != nil && info.OAuthStatus.HasToken))

	// Check if there's already a pending auth for this tool.
	if existing := s.deps.OAuthDeps.PendingMgr.GetByToolName(name); existing != nil && existing.AuthURL != "" {
		s.logger.Debug("oauth: returning existing pending auth",
			slog.String("tool", name),
			slog.String("pending_id", existing.ID))
		writeJSON(w, http.StatusOK, map[string]any{
			"pending_id": existing.ID,
			"auth_url":   existing.AuthURL,
			"status":     "pending",
		})
		return
	}

	// Trigger tool reconnection in a background goroutine.
	// Uses a detached context because the HTTP response is written before
	// the OAuth flow completes — r.Context() would be cancelled prematurely.
	s.logger.Debug("oauth: starting reconnect goroutine",
		slog.String("tool", name))
	s.startOAuthReconnect(name, mgr)

	// Wait for the pending auth to appear (the fetcher creates it).
	var pending *oauth.PendingAuth
	deadline := time.After(30 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{
				"error": "Timed out waiting for OAuth flow to start. Check tool configuration.",
			})
			return
		case <-ticker.C:
			pending = s.deps.OAuthDeps.PendingMgr.GetByToolName(name)
			if pending != nil && pending.AuthURL != "" {
				writeJSON(w, http.StatusOK, map[string]any{
					"pending_id": pending.ID,
					"auth_url":   pending.AuthURL,
					"status":     "pending",
				})
				return
			}
		}
	}
}

// startOAuthReconnect triggers an OAuth reconnection flow in a background
// goroutine. The AuthorizationCodeFetcher creates a pending auth that the
// caller polls for.
func (s *Server) startOAuthReconnect(name string, mgr *tool.Manager) {
	bgCtx, bgCancel := context.WithTimeout(context.Background(), 6*time.Minute)
	go func() {
		defer bgCancel()

		cfg, cfgOK := mgr.ServerToolConfig(name)
		if !cfgOK {
			s.logger.Error("oauth: tool config not found for reconnection",
				slog.String("tool", name))
			return
		}

		s.logger.Debug("oauth: clearing token for reconnect",
			slog.String("tool", name))

		if oh := mgr.GetOAuthHandler(name); oh != nil {
			_ = oh.ClearToken()
		}

		// Do NOT unregister first — the existing map entry is needed so
		// registerSSE detects this as a re-registration and skips the
		// pending_auth short-circuit, proceeding to Connect() instead.
		s.logger.Debug("oauth: re-registering to trigger authorization flow",
			slog.String("tool", name))

		if err := mgr.RegisterServer(bgCtx, name, cfg); err != nil {
			s.logger.Warn("oauth: reconnection failed (expected if waiting for auth)",
				slog.String("tool", name),
				slog.String("error", err.Error()))
		} else {
			s.logger.Debug("oauth: re-registration completed successfully",
				slog.String("tool", name))
		}
	}()
}

// handleToolOAuthRevoke deletes the stored OAuth token for a tool.
func (s *Server) handleToolOAuthRevoke(w http.ResponseWriter, r *http.Request) {
	if s.deps.OAuthDeps == nil || s.deps.OAuthDeps.TokenStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "OAuth support is not configured"})
		return
	}
	if !s.lifecycleRequired(w) {
		return
	}

	name := r.PathValue("name")
	mgr := s.deps.LifecycleMgr.ToolManager()

	// Clear handler's cached token.
	if oh := mgr.GetOAuthHandler(name); oh != nil {
		if err := oh.ClearToken(); err != nil {
			s.logger.Error("oauth: failed to clear token",
				slog.String("tool", name),
				slog.String("error", err.Error()))
		}
	} else {
		// Fallback: delete directly from store.
		if err := s.deps.OAuthDeps.TokenStore.Delete(name); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// handleListPendingOAuth returns all active pending OAuth authorizations.
func (s *Server) handleListPendingOAuth(w http.ResponseWriter, r *http.Request) {
	if s.deps.OAuthDeps == nil || s.deps.OAuthDeps.PendingMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "OAuth support is not configured"})
		return
	}

	pending := s.deps.OAuthDeps.PendingMgr.List()
	writeJSON(w, http.StatusOK, pending)
}

// serveCallbackHTML serves a simple HTML page after OAuth callback.
func (s *Server) serveCallbackHTML(w http.ResponseWriter, success bool, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if success {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `<!DOCTYPE html>
<html><head><title>Authorization Successful</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, sans-serif; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; background: #f5f5f5; }
.card { background: white; padding: 2rem 3rem; border-radius: 12px; box-shadow: 0 2px 8px rgba(0,0,0,0.1); text-align: center; }
h2 { color: #16a34a; margin-bottom: 0.5rem; }
p { color: #6b7280; }
</style></head>
<body><div class="card">
<h2>Authorization Successful</h2>
<p>You may close this window.</p>
<p style="font-size:0.9em;color:#9ca3af">This window will close automatically.</p>
</div>
<script>setTimeout(function(){ window.close(); }, 2000);</script>
</body></html>`)
		return
	}

	w.WriteHeader(http.StatusBadRequest)
	safeMsg := html.EscapeString(errMsg)
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><title>Authorization Failed</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, sans-serif; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; background: #f5f5f5; }
.card { background: white; padding: 2rem 3rem; border-radius: 12px; box-shadow: 0 2px 8px rgba(0,0,0,0.1); text-align: center; max-width: 480px; }
h2 { color: #dc2626; margin-bottom: 0.5rem; }
p { color: #6b7280; }
code { background: #f3f4f6; padding: 0.2rem 0.5rem; border-radius: 4px; font-size: 0.85em; }
</style></head>
<body><div class="card">
<h2>Authorization Failed</h2>
<p><code>%s</code></p>
<p>Close this window and try again from the Denkeeper dashboard.</p>
</div></body></html>`, safeMsg)
}


