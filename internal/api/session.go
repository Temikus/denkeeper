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

// SessionManager handles AES-256-GCM encrypted session cookies.
type SessionManager struct {
	gcm    cipher.AEAD
	maxAge time.Duration
	secure bool // set Secure flag on cookies (should be true in production)
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

// Create encrypts the session and sets it as an HttpOnly cookie.
func (sm *SessionManager) Create(w http.ResponseWriter, s Session) error {
	if s.ExpiresAt == 0 {
		s.ExpiresAt = time.Now().Add(sm.maxAge).Unix()
	}

	plaintext, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("session: marshal: %w", err)
	}

	nonce := make([]byte, sm.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("session: generating nonce: %w", err)
	}

	ciphertext := sm.gcm.Seal(nonce, nonce, plaintext, nil)
	encoded := base64.URLEncoding.EncodeToString(ciphertext)

	http.SetCookie(w, &http.Cookie{
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

// Read decrypts and validates the session cookie from the request.
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
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   sm.secure,
		SameSite: http.SameSiteLaxMode,
	})
}
