package tool

import (
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/tool/oauth"
)

// setTransportOAuthHandler assigns the OAuth handler to the StreamableClientTransport.
func setTransportOAuthHandler(t *mcp.StreamableClientTransport, handler any) {
	if h, ok := handler.(auth.OAuthHandler); ok {
		t.OAuthHandler = h
	}
}

// NewOAuthHandlerFactory creates an OAuthHandlerFactory using the provided
// token store and pending manager. Called during wiring in main.go.
func NewOAuthHandlerFactory(store *oauth.TokenStore, pending *oauth.PendingManager, callbackURL string, logger *slog.Logger) OAuthHandlerFactory {
	return func(name string, cfg config.ToolConfig, httpClient *http.Client) (oauthHandler, any, error) {
		h, err := oauth.NewHandler(oauth.HandlerConfig{
			ToolName:     name,
			CallbackURL:  callbackURL,
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Scopes:       cfg.Scopes,
			HTTPClient:   httpClient,
			Store:        store,
			Pending:      pending,
			Logger:       logger,
		})
		if err != nil {
			return nil, nil, err
		}
		// h satisfies both oauthHandler (ToolName, HasToken, ClearToken)
		// and auth.OAuthHandler (via embedded AuthorizationCodeHandler).
		return h, h, nil
	}
}
