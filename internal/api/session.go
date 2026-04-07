package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const sessionCookieName = "dk_session"

// Session represents a dashboard login session stored in an encrypted cookie.
type Session struct {
	Email     string   `json:"email"`
	Scopes    []string `json:"scopes"`
	ExpiresAt int64    `json:"exp"` // Unix timestamp
}

// sessionCookiePayload is the minimal payload stored in the encrypted cookie
// when server-tracked sessions are enabled. Only the session ID and expiry are
// stored; full session data lives in SQLite.
type sessionCookiePayload struct {
	ID        string `json:"id"`
	ExpiresAt int64  `json:"exp"`
}

// SessionManager handles AES-256-GCM encrypted session cookies.
// When Store is non-nil, sessions are server-tracked in SQLite and the cookie
// contains only a session ID. When Store is nil, legacy mode is used where the
// full session data is encrypted in the cookie.
type SessionManager struct {
	gcm    cipher.AEAD
	maxAge time.Duration
	secure bool // set Secure flag on cookies (should be true in production)
	Store  *SessionStore
}

// NewSessionManager creates a SessionManager from a hex-encoded AES key (≥32 bytes).
func NewSessionManager(hexKey string, maxAge time.Duration, secure bool) (*SessionManager, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("session: invalid hex key: %w", err)
	}
	if len(key) < 32 {
		return nil, fmt.Errorf("session: key must be at least 32 bytes, got %d", len(key))
	}
	// Use exactly 32 bytes for AES-256.
	block, err := aes.NewCipher(key[:32])
	if err != nil {
		return nil, fmt.Errorf("session: creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("session: creating GCM: %w", err)
	}
	return &SessionManager{gcm: gcm, maxAge: maxAge, secure: secure}, nil
}

// CreateWithRequest encrypts the session and sets it as an HttpOnly cookie.
// When server-tracked sessions are enabled, the session is also stored in SQLite
// and the cookie contains only the session ID.
func (sm *SessionManager) CreateWithRequest(w http.ResponseWriter, r *http.Request, s Session) error {
	if s.ExpiresAt == 0 {
		s.ExpiresAt = time.Now().Add(sm.maxAge).Unix()
	}

	var plaintext []byte
	var err error

	if sm.Store != nil && r != nil {
		// Server-tracked mode: store full session in SQLite, cookie has only ID+exp.
		expiresAt := time.Unix(s.ExpiresAt, 0)
		userAgent := r.UserAgent()
		ip := clientIP(r)
		id, createErr := sm.Store.Create(r.Context(), s.Email, s.Scopes, userAgent, ip, expiresAt)
		if createErr != nil {
			return fmt.Errorf("session: store create: %w", createErr)
		}
		payload := sessionCookiePayload{ID: id, ExpiresAt: s.ExpiresAt}
		plaintext, err = json.Marshal(payload)
	} else {
		// Legacy mode: full session in cookie.
		plaintext, err = json.Marshal(s)
	}
	if err != nil {
		return fmt.Errorf("session: marshal: %w", err)
	}

	nonce := make([]byte, sm.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("session: generating nonce: %w", err)
	}

	ciphertext := sm.gcm.Seal(nonce, nonce, plaintext, nil)
	encoded := base64.URLEncoding.EncodeToString(ciphertext)

	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is set dynamically via sm.secure
		Name:     sessionCookieName,
		Value:    encoded,
		Path:     "/",
		MaxAge:   int(sm.maxAge.Seconds()),
		HttpOnly: true,
		Secure:   sm.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// Create encrypts the session and sets it as an HttpOnly cookie (legacy, no request context).
func (sm *SessionManager) Create(w http.ResponseWriter, s Session) error {
	return sm.CreateWithRequest(w, nil, s)
}

// Read decrypts and validates the session cookie from the request.
// When server-tracked sessions are enabled, it looks up the session in SQLite.
// Falls back to legacy full-session decryption for rolling upgrades.
func (sm *SessionManager) Read(r *http.Request) (*Session, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, fmt.Errorf("session: no cookie: %w", err)
	}

	ciphertext, err := base64.URLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return nil, fmt.Errorf("session: invalid base64: %w", err)
	}

	nonceSize := sm.gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("session: ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := sm.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("session: decryption failed: %w", err)
	}

	// Try server-tracked session first (cookie contains {id, exp}).
	if sm.Store != nil {
		var payload sessionCookiePayload
		if json.Unmarshal(plaintext, &payload) == nil && payload.ID != "" {
			rec, lookupErr := sm.Store.Get(r.Context(), payload.ID)
			if lookupErr == nil {
				// Touch asynchronously to avoid blocking reads.
				go func() { _ = sm.Store.Touch(r.Context(), payload.ID) }()
				return &Session{
					Email:     rec.Email,
					Scopes:    ScopesFromRecord(rec),
					ExpiresAt: rec.ExpiresAt.Unix(),
				}, nil
			}
			// Fall through to legacy decryption for rolling upgrade compatibility.
		}
	}

	// Legacy mode: full session in cookie.
	var s Session
	if err := json.Unmarshal(plaintext, &s); err != nil {
		return nil, fmt.Errorf("session: unmarshal: %w", err)
	}

	if time.Now().Unix() > s.ExpiresAt {
		return nil, fmt.Errorf("session: expired")
	}

	return &s, nil
}

// Clear removes the session cookie.
func (sm *SessionManager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is set dynamically via sm.secure
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   sm.secure,
		SameSite: http.SameSiteLaxMode,
	})
}
