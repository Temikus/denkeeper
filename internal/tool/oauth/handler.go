//go:build mcp_go_client_oauth

package oauth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// Provider compatibility notes:
//
// MCP OAuth providers fall into two categories:
//
//  1. Non-expiring tokens (e.g. Todoist): return expires_in=0, no refresh
//     token, and may use Dynamic Client Registration. After the initial
//     auth, the access token works indefinitely — no refresh needed. The
//     MCP SDK also returns HTTP 200 (not 201) for DCR, requiring a fixup.
//
//  2. RFC-compliant providers: return tokens with expiry and a refresh
//     token. Need the token URL and client credentials persisted so
//     refresh works across restarts.
//
// The oauthRoundTripper below handles both by capturing the token endpoint
// URL and any DCR-assigned client credentials from the HTTP requests the
// SDK makes during the authorization flow.

// oauthRoundTripper wraps the base transport with two workarounds:
//  1. DCR fix: rewrites HTTP 200 → 201 for Dynamic Client Registration
//     POSTs to /register endpoints (Todoist returns 200 instead of 201).
//  2. Token URL capture: records the URL of the token exchange POST so
//     we can persist it for token refresh across restarts.
type oauthRoundTripper struct {
	base http.RoundTripper

	mu           sync.Mutex
	tokenURL     string // captured from the token exchange request
	clientID     string // captured from the token exchange request
	clientSecret string // captured from the token exchange request
	authStyle    oauth2.AuthStyle
}

func (rt *oauthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	if req.Method != http.MethodPost {
		return resp, nil
	}

	path := req.URL.Path

	// DCR fix: rewrite 200 → 201 for registration endpoints.
	if resp.StatusCode == http.StatusOK && strings.Contains(path, "register") {
		resp.StatusCode = http.StatusCreated
		resp.Status = "201 Created"
	}

	// Capture metadata from the token exchange request.
	// The SDK POSTs to the token endpoint with grant_type in the body;
	// we detect it by the path containing "token" (but not "register").
	if strings.Contains(path, "token") && !strings.Contains(path, "register") {
		rt.captureTokenExchange(req)
	}

	return resp, nil
}

// captureTokenExchange extracts the token URL and client credentials
// from the token exchange request. This captures DCR-assigned credentials
// that aren't otherwise accessible from the SDK's internal state.
func (rt *oauthRoundTripper) captureTokenExchange(req *http.Request) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.tokenURL = req.URL.Scheme + "://" + req.URL.Host + req.URL.Path

	// Try HTTP Basic auth first (client_secret_basic).
	if user, pass, ok := req.BasicAuth(); ok && user != "" {
		rt.clientID = user
		rt.clientSecret = pass
		rt.authStyle = oauth2.AuthStyleInHeader
		return
	}

	// Fall back to form body (client_secret_post).
	// Clone the body so the original request isn't consumed.
	if req.Body != nil && req.GetBody != nil {
		if body, err := req.GetBody(); err == nil {
			buf := make([]byte, 4096)
			n, _ := body.Read(buf)
			params := string(buf[:n])
			for _, part := range strings.Split(params, "&") {
				kv := strings.SplitN(part, "=", 2)
				if len(kv) != 2 {
					continue
				}
				switch kv[0] {
				case "client_id":
					rt.clientID = kv[1]
					rt.authStyle = oauth2.AuthStyleInParams
				case "client_secret":
					rt.clientSecret = kv[1]
				}
			}
		}
	}
}

// captured returns the token endpoint URL and client credentials observed
// during the last token exchange.
func (rt *oauthRoundTripper) captured() (tokenURL, clientID, clientSecret string, authStyle oauth2.AuthStyle) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.tokenURL, rt.clientID, rt.clientSecret, rt.authStyle
}

// HandlerConfig configures the OAuth handler for a remote MCP tool.
type HandlerConfig struct {
	ToolName     string
	CallbackURL  string // e.g. "https://denkeeper.example.com/api/v1/tools/oauth/callback"
	ClientID     string // pre-registered (optional)
	ClientSecret string // pre-registered (optional)
	Scopes       []string
	HTTPClient   *http.Client // SSRF-safe client

	Store   *TokenStore
	Pending *PendingManager
	Logger  *slog.Logger
}

// Handler wraps auth.AuthorizationCodeHandler to add token persistence and
// pending authorization bridging for the web UI.
//
// It embeds *auth.AuthorizationCodeHandler to satisfy the unexported
// isOAuthHandler() method required by the auth.OAuthHandler interface.
type Handler struct {
	*auth.AuthorizationCodeHandler

	store    *TokenStore
	pending  *PendingManager
	toolName string
	logger   *slog.Logger

	// Config fields preserved for token persistence across restarts.
	clientID     string
	clientSecret string
	scopes       []string

	ctx    context.Context    // cancelled on Close(); used for token refresh
	cancel context.CancelFunc
	rt     *oauthRoundTripper // captures token URL during exchange

	mu       sync.Mutex
	cachedTS oauth2.TokenSource // from stored token, nil if none
}

// NewHandler creates an OAuth handler for a remote MCP tool.
// If a stored token exists, it initializes cachedTS for immediate use.
func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.ToolName == "" {
		return nil, fmt.Errorf("oauth handler: tool name is required")
	}
	if cfg.CallbackURL == "" {
		return nil, fmt.Errorf("oauth handler: callback URL is required")
	}
	if cfg.Store == nil {
		return nil, fmt.Errorf("oauth handler: token store is required")
	}
	if cfg.Pending == nil {
		return nil, fmt.Errorf("oauth handler: pending manager is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())
	h := &Handler{
		store:        cfg.Store,
		pending:      cfg.Pending,
		toolName:     cfg.ToolName,
		logger:       cfg.Logger,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		scopes:       cfg.Scopes,
		ctx:          ctx,
		cancel:       cancel,
	}

	// Try to load a cached token for immediate use.
	stored, err := cfg.Store.Get(cfg.ToolName)
	if err != nil {
		cfg.Logger.Warn("oauth: failed to load cached token",
			slog.String("tool", cfg.ToolName),
			slog.String("error", err.Error()))
	} else if stored != nil {
		h.cachedTS = h.tokenSourceFromStored(stored)
		cfg.Logger.Info("oauth: loaded cached token",
			slog.String("tool", cfg.ToolName),
			slog.Bool("has_refresh", stored.RefreshToken != ""))
	}

	// Build the inner AuthorizationCodeHandler.
	innerCfg := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL: cfg.CallbackURL,
		AuthorizationCodeFetcher: func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			return h.fetchAuthorizationCode(ctx, args)
		},
	}

	// Wrap the HTTP client with our round tripper that fixes DCR status codes
	// (e.g. Todoist returns 200 instead of 201) and captures the token URL.
	baseClient := cfg.HTTPClient
	if baseClient == nil {
		baseClient = http.DefaultClient
	}
	baseTransport := baseClient.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	h.rt = &oauthRoundTripper{base: baseTransport}
	innerCfg.Client = &http.Client{
		Transport:     h.rt,
		Timeout:       baseClient.Timeout,
		CheckRedirect: baseClient.CheckRedirect,
		Jar:           baseClient.Jar,
	}

	// Configure client registration.
	if cfg.ClientID != "" && cfg.ClientSecret != "" {
		cfg.Logger.Debug("oauth: using pre-registered client",
			slog.String("tool", cfg.ToolName),
			slog.String("client_id", cfg.ClientID))
		innerCfg.PreregisteredClientConfig = &auth.PreregisteredClientConfig{
			ClientSecretAuthConfig: &auth.ClientSecretAuthConfig{
				ClientID:     cfg.ClientID,
				ClientSecret: cfg.ClientSecret,
			},
		}
	} else {
		cfg.Logger.Debug("oauth: using dynamic client registration",
			slog.String("tool", cfg.ToolName),
			slog.String("callback_url", cfg.CallbackURL))
		innerCfg.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				ClientName:   "Denkeeper",
				RedirectURIs: []string{cfg.CallbackURL},
			},
		}
	}

	inner, err := auth.NewAuthorizationCodeHandler(innerCfg)
	if err != nil {
		return nil, fmt.Errorf("oauth handler: creating auth handler for %q: %w", cfg.ToolName, err)
	}
	h.AuthorizationCodeHandler = inner

	return h, nil
}

// TokenSource returns a cached token source if available, otherwise nil.
// When nil is returned, the transport will send a request without auth,
// receive a 401, and call Authorize().
func (h *Handler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	h.mu.Lock()
	ts := h.cachedTS
	h.mu.Unlock()
	h.logger.Debug("oauth: TokenSource called",
		slog.String("tool", h.toolName),
		slog.Bool("has_cached", ts != nil))
	return ts, nil
}

// Authorize delegates to the inner handler, then persists the resulting token.
func (h *Handler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	h.logger.Info("oauth: starting authorization flow",
		slog.String("tool", h.toolName))

	h.logger.Debug("oauth: Authorize called",
		slog.String("tool", h.toolName),
		slog.Int("resp_status", resp.StatusCode),
		slog.String("resp_url", req.URL.Host))

	if err := h.AuthorizationCodeHandler.Authorize(ctx, req, resp); err != nil {
		h.logger.Error("oauth: authorization failed",
			slog.String("tool", h.toolName),
			slog.String("error", err.Error()))
		return err
	}

	// After successful authorization, grab the token from the inner handler
	// and persist it.
	ts, err := h.AuthorizationCodeHandler.TokenSource(ctx)
	if err != nil || ts == nil {
		h.logger.Warn("oauth: no token source after authorization",
			slog.String("tool", h.toolName))
		return nil
	}

	tok, err := ts.Token()
	if err != nil {
		h.logger.Warn("oauth: failed to get token after authorization",
			slog.String("tool", h.toolName),
			slog.String("error", err.Error()))
		return nil
	}

	h.persistToken(tok, ts)

	h.logger.Info("oauth: authorization complete, token stored",
		slog.String("tool", h.toolName))

	return nil
}

// fetchAuthorizationCode is the callback provided to AuthorizationCodeHandler.
// It creates a pending auth, publishes the URL, and blocks until the callback
// endpoint resolves the pending auth.
func (h *Handler) fetchAuthorizationCode(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
	h.logger.Debug("oauth: fetchAuthorizationCode callback invoked",
		slog.String("tool", h.toolName),
		slog.Bool("has_auth_url", args.URL != ""))

	pa := h.pending.Create(h.toolName)

	if err := h.pending.SetAuthURL(pa.ID, args.URL); err != nil {
		return nil, fmt.Errorf("oauth: setting auth URL: %w", err)
	}

	h.logger.Info("oauth: waiting for user authorization",
		slog.String("tool", h.toolName),
		slog.String("pending_id", pa.ID))

	code, state, err := h.pending.WaitForCompletion(ctx, pa.ID)
	if err != nil {
		h.logger.Debug("oauth: WaitForCompletion returned error",
			slog.String("tool", h.toolName),
			slog.String("error", err.Error()))
		return nil, err
	}

	return &auth.AuthorizationResult{
		Code:  code,
		State: state,
	}, nil
}

// persistToken stores the token (including OAuth config metadata needed for
// token refresh across restarts) and updates the cached token source.
func (h *Handler) persistToken(tok *oauth2.Token, ts oauth2.TokenSource) {
	st := &StoredToken{
		ToolName:     h.toolName,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		ClientID:     h.clientID,
		ClientSecret: h.clientSecret,
		Scopes:       h.scopes,
	}
	if !tok.Expiry.IsZero() {
		exp := tok.Expiry
		st.Expiry = &exp
	}

	// The MCP SDK constructs the oauth2.Config (with TokenURL, ClientID, etc.)
	// as a local variable inside Authorize() — we can't access it directly.
	// Our round tripper intercepts the token exchange HTTP request to capture
	// these values. This is essential for two cases:
	//  - TokenURL: needed for token refresh across restarts (RFC providers).
	//  - ClientID/Secret: needed for DCR flows (e.g. Todoist) where the
	//    credentials are assigned dynamically and h.clientID is empty.
	// For non-expiring providers like Todoist (no refresh token), these
	// fields are stored but unused — the token is static.
	capturedURL, capturedID, capturedSecret, capturedStyle := h.rt.captured()
	if capturedURL != "" {
		st.TokenURL = capturedURL
	}
	if st.ClientID == "" && capturedID != "" {
		st.ClientID = capturedID
		st.ClientSecret = capturedSecret
		st.AuthStyle = capturedStyle
	}
	if st.RefreshToken != "" && st.TokenURL == "" {
		h.logger.Warn("oauth: token URL not available, refresh will not survive restart",
			slog.String("tool", h.toolName))
	}

	h.logger.Debug("oauth: persisting token",
		slog.String("tool", h.toolName),
		slog.Bool("has_access_token", st.AccessToken != ""),
		slog.Bool("has_refresh_token", st.RefreshToken != ""),
		slog.Bool("has_expiry", st.Expiry != nil),
		slog.String("token_url", st.TokenURL),
		slog.Bool("has_client_id", st.ClientID != ""),
		slog.Bool("has_client_secret", st.ClientSecret != ""),
		slog.Int("scopes_count", len(st.Scopes)))

	if err := h.store.Put(st); err != nil {
		h.logger.Error("oauth: failed to persist token",
			slog.String("tool", h.toolName),
			slog.String("error", err.Error()))
		return
	}

	h.mu.Lock()
	h.cachedTS = ts
	h.mu.Unlock()
}

// tokenSourceFromStored creates an oauth2.TokenSource from a stored token.
// For RFC-compliant providers with a refresh token and token URL, it creates
// a ReuseTokenSource that auto-refreshes 5 minutes before expiry.
// For non-expiring providers like Todoist (no refresh token or no token URL),
// it returns a static source — the access token is used as-is.
func (h *Handler) tokenSourceFromStored(st *StoredToken) oauth2.TokenSource {
	tok := st.ToOAuth2Token()

	if st.RefreshToken != "" && st.TokenURL != "" {
		cfg := st.ToOAuth2Config()
		return oauth2.ReuseTokenSourceWithExpiry(tok, cfg.TokenSource(h.ctx, tok), 5*time.Minute)
	}

	// No refresh capability — return a static token source.
	return oauth2.StaticTokenSource(tok)
}

// ToolName returns the tool name this handler is for.
func (h *Handler) ToolName() string {
	return h.toolName
}

// HasToken returns whether this handler has a cached token.
func (h *Handler) HasToken() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cachedTS != nil
}

// Close cancels any background token refresh operations.
func (h *Handler) Close() {
	h.cancel()
}

// ClearToken removes the cached token and stored token.
func (h *Handler) ClearToken() error {
	h.mu.Lock()
	h.cachedTS = nil
	h.mu.Unlock()

	if err := h.store.Delete(h.toolName); err != nil {
		return fmt.Errorf("oauth: clearing token for %q: %w", h.toolName, err)
	}

	h.logger.Info("oauth: token cleared",
		slog.String("tool", h.toolName))
	return nil
}
