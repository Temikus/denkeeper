package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const oidcStateCookieName = "dk_oidc_state"

// oidcStateCookie holds the OIDC flow parameters encrypted in a short-lived cookie.
type oidcStateCookie struct {
	State    string `json:"state"`
	Verifier string `json:"verifier"` // PKCE code verifier
	Nonce    string `json:"nonce"`
}

// OIDCProvider wraps the OIDC discovery provider and OAuth2 config.
type OIDCProvider struct {
	provider      *oidc.Provider
	verifier      *oidc.IDTokenVerifier
	oauth2Config  oauth2.Config
	allowedEmails map[string]bool
	sessions      *SessionManager
	logger        *slog.Logger
}

// NewOIDCProvider creates an OIDCProvider by performing OIDC discovery.
func NewOIDCProvider(ctx context.Context, issuer, clientID, clientSecret, redirectURL string, scopes, allowedEmails []string, sessions *SessionManager, logger *slog.Logger) (*OIDCProvider, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery failed for %s: %w", issuer, err)
	}

	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "email", "profile"}
	}

	emailSet := make(map[string]bool, len(allowedEmails))
	for _, e := range allowedEmails {
		emailSet[strings.ToLower(e)] = true
	}

	return &OIDCProvider{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
		oauth2Config: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       scopes,
		},
		allowedEmails: emailSet,
		sessions:      sessions,
		logger:        logger,
	}, nil
}

// HandleLogin starts the OIDC authorization code flow with PKCE and nonce.
func (op *OIDCProvider) HandleLogin(w http.ResponseWriter, r *http.Request) {
	state := randomString(32)
	nonce := randomString(32)
	verifier := oauth2.GenerateVerifier()

	// Encrypt state+verifier+nonce in a short-lived cookie.
	stateData := oidcStateCookie{State: state, Verifier: verifier, Nonce: nonce}
	cookieJSON, _ := json.Marshal(stateData)

	// Use the session manager's encryption for the state cookie.
	sm := op.sessions
	w2 := &cookieCapture{ResponseWriter: w}
	_ = sm.Create(w2, Session{
		Email:     string(cookieJSON), // abuse Email field for state data
		ExpiresAt: time.Now().Add(5 * time.Minute).Unix(),
	})
	// Override cookie name and maxAge for the state cookie.
	for _, c := range w2.cookies {
		c.Name = oidcStateCookieName
		c.MaxAge = 300 // 5 minutes
		http.SetCookie(w, c)
	}

	authURL := op.oauth2Config.AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("nonce", nonce),
	)

	http.Redirect(w, r, authURL, http.StatusFound)
}

// HandleCallback completes the OIDC authorization code flow.
func (op *OIDCProvider) HandleCallback(w http.ResponseWriter, r *http.Request) {
	// Read and decrypt the state cookie.
	stateCookie, err := r.Cookie(oidcStateCookieName)
	if err != nil {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}

	// Decrypt the state cookie using session manager.
	fakeReq := &http.Request{Header: http.Header{}}
	fakeReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: stateCookie.Value}) // #nosec G124 -- internal cookie for reading, not sent to client
	sess, err := op.sessions.Read(fakeReq)
	if err != nil {
		op.logger.Warn("oidc: invalid state cookie", "error", err)
		http.Error(w, "invalid or expired state cookie", http.StatusBadRequest)
		return
	}

	var stateData oidcStateCookie
	if err := json.Unmarshal([]byte(sess.Email), &stateData); err != nil {
		http.Error(w, "corrupt state cookie", http.StatusBadRequest)
		return
	}

	// Verify state parameter matches.
	if r.URL.Query().Get("state") != stateData.State {
		op.logger.Warn("oidc: state mismatch")
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}

	// Exchange authorization code with PKCE verifier.
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	token, err := op.oauth2Config.Exchange(r.Context(), code,
		oauth2.VerifierOption(stateData.Verifier))
	if err != nil {
		op.logger.Error("oidc: code exchange failed", "error", err)
		http.Error(w, "code exchange failed", http.StatusInternalServerError)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "no id_token in response", http.StatusInternalServerError)
		return
	}

	idToken, err := op.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		op.logger.Error("oidc: id_token verification failed", "error", err)
		http.Error(w, "token verification failed", http.StatusUnauthorized)
		return
	}

	// Verify nonce.
	if idToken.Nonce != stateData.Nonce {
		op.logger.Warn("oidc: nonce mismatch")
		http.Error(w, "nonce mismatch", http.StatusUnauthorized)
		return
	}

	// Extract claims.
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "failed to parse claims", http.StatusInternalServerError)
		return
	}

	// Require verified email.
	if !claims.EmailVerified {
		op.logger.Warn("oidc: unverified email", "email", claims.Email)
		http.Error(w, "email not verified by provider", http.StatusForbidden)
		return
	}

	// Check allowed emails (case-insensitive).
	email := strings.ToLower(claims.Email)
	if !op.allowedEmails[email] {
		op.logger.Warn("oidc: email not in allowlist", "email", email)
		http.Error(w, "email not authorized", http.StatusForbidden)
		return
	}

	// Clear the state cookie.
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is set dynamically via op.sessions.secure
		Name:     oidcStateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   op.sessions.secure,
		SameSite: http.SameSiteLaxMode,
	})

	// Create session cookie.
	if err := op.sessions.Create(w, Session{
		Email:  email,
		Scopes: adminScopes(),
	}); err != nil {
		op.logger.Error("oidc: failed to create session", "error", err)
		http.Error(w, "session creation failed", http.StatusInternalServerError)
		return
	}

	op.logger.Info("oidc: login successful", "email", email)
	http.Redirect(w, r, "/#/overview", http.StatusFound)
}

// cookieCapture captures cookies set via http.SetCookie for rewriting.
type cookieCapture struct {
	http.ResponseWriter
	cookies []*http.Cookie
}

func (cc *cookieCapture) Header() http.Header {
	return cc.ResponseWriter.Header()
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)[:n]
}
