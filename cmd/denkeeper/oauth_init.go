//go:build mcp_go_client_oauth

package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"github.com/Temikus/denkeeper/internal/api"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/tool"
	"github.com/Temikus/denkeeper/internal/tool/oauth"
)

// oauthState holds OAuth infrastructure created during initialization.
type oauthState struct {
	tokenStore *oauth.TokenStore
	pendingMgr *oauth.PendingManager
	db         *sqlx.DB
}

func (s *oauthState) Close() {
	if s == nil {
		return
	}
	if s.db != nil {
		_ = s.db.Close()
	}
}

// initOAuthSupport creates the OAuth token store and pending manager,
// wires them into the tool manager, and returns deps for the API server.
// The session secret is always available at this point (auto-generated at
// startup if not configured), so OAuth support is always initialized.
func initOAuthSupport(ctx context.Context, cfg *config.Config, toolMgr *tool.Manager, logger *slog.Logger) (*oauthState, *api.OAuthDeps, error) {
	secret := cfg.API.Auth.SessionSecret

	// Open a dedicated DB connection for OAuth tokens (same file, separate conn).
	db, err := sqlx.Open("sqlite", cfg.Memory.DBPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, nil, fmt.Errorf("opening OAuth token database: %w", err)
	}

	store, err := oauth.NewTokenStore(db, secret)
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("creating OAuth token store: %w", err)
	}

	pending := oauth.NewPendingManager(logger)

	// Construct the callback URL.
	callbackURL := buildCallbackURL(cfg)

	// Wire OAuth into the tool manager.
	factory := tool.NewOAuthHandlerFactory(store, pending, callbackURL, logger)
	toolMgr.SetOAuthSupport(&tool.OAuthSupport{
		HandlerFactory: factory,
		CallbackURL:    callbackURL,
	})

	go pending.StartCleanup(ctx, time.Minute)

	logger.Info("oauth: support initialized",
		slog.String("callback_url", callbackURL))

	state := &oauthState{
		tokenStore: store,
		pendingMgr: pending,
		db:         db,
	}

	apiDeps := &api.OAuthDeps{
		TokenStore: store,
		PendingMgr: pending,
	}

	return state, apiDeps, nil
}

// buildCallbackURL constructs the OAuth callback URL from config.
func buildCallbackURL(cfg *config.Config) string {
	if cfg.API.ExternalURL != "" {
		base := strings.TrimRight(cfg.API.ExternalURL, "/")
		return base + "/api/v1/tools/oauth/callback"
	}

	// Fallback: construct from listen address.
	scheme := "http"
	if cfg.API.TLS {
		scheme = "https"
	}
	listen := cfg.API.Listen
	if listen == "" {
		listen = ":8080"
	}
	// Replace leading ":" with "localhost:".
	if strings.HasPrefix(listen, ":") {
		listen = "localhost" + listen
	}
	return fmt.Sprintf("%s://%s/api/v1/tools/oauth/callback", scheme, listen)
}
