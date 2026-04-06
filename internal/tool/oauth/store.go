package oauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"golang.org/x/oauth2"
)

const schema = `
CREATE TABLE IF NOT EXISTS oauth_tokens (
    tool_name     TEXT PRIMARY KEY,
    access_token  BLOB NOT NULL,
    refresh_token BLOB,
    token_type    TEXT NOT NULL DEFAULT 'Bearer',
    expiry        DATETIME,
    scopes        TEXT NOT NULL DEFAULT '',
    client_id     TEXT NOT NULL DEFAULT '',
    client_secret BLOB,
    token_url     TEXT NOT NULL DEFAULT '',
    auth_style    INTEGER NOT NULL DEFAULT 0,
    resource_url  TEXT NOT NULL DEFAULT '',
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

// migrations adds columns that may be missing from tables created by
// earlier versions. Each statement is tried independently; "duplicate
// column name" errors are expected and ignored.
var migrations = []string{
	"ALTER TABLE oauth_tokens ADD COLUMN client_id TEXT NOT NULL DEFAULT ''",
	"ALTER TABLE oauth_tokens ADD COLUMN client_secret BLOB",
	"ALTER TABLE oauth_tokens ADD COLUMN token_url TEXT NOT NULL DEFAULT ''",
	"ALTER TABLE oauth_tokens ADD COLUMN auth_style INTEGER NOT NULL DEFAULT 0",
	"ALTER TABLE oauth_tokens ADD COLUMN resource_url TEXT NOT NULL DEFAULT ''",
	"ALTER TABLE oauth_tokens ADD COLUMN scopes TEXT NOT NULL DEFAULT ''",
}

// StoredToken holds everything needed to reconstruct an oauth2.TokenSource
// without re-authorizing. Sensitive fields are encrypted at rest.
type StoredToken struct {
	ToolName     string
	AccessToken  string
	RefreshToken string
	TokenType    string
	Expiry       *time.Time
	Scopes       []string

	// OAuth2 config for token refresh.
	ClientID     string
	ClientSecret string
	TokenURL     string
	AuthStyle    oauth2.AuthStyle
	ResourceURL  string
}

// TokenSummary is a non-sensitive view of a stored token for API responses.
type TokenSummary struct {
	HasToken    bool       `json:"has_token"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Scopes      []string   `json:"scopes,omitempty"`
	NeedsReauth bool       `json:"needs_reauth"`
}

// tokenRow is the SQLite row representation with encrypted fields.
type tokenRow struct {
	ToolName     string     `db:"tool_name"`
	AccessToken  []byte     `db:"access_token"`
	RefreshToken []byte     `db:"refresh_token"`
	TokenType    string     `db:"token_type"`
	Expiry       *time.Time `db:"expiry"`
	Scopes       string     `db:"scopes"`
	ClientID     string     `db:"client_id"`
	ClientSecret []byte     `db:"client_secret"`
	TokenURL     string     `db:"token_url"`
	AuthStyle    int        `db:"auth_style"`
	ResourceURL  string     `db:"resource_url"`
}

// TokenStore provides encrypted token persistence in SQLite.
type TokenStore struct {
	db  *sqlx.DB
	gcm cipher.AEAD
}

// NewTokenStore creates a TokenStore using the provided database and hex-encoded
// AES-256 key (at least 32 bytes). The schema is applied automatically.
func NewTokenStore(db *sqlx.DB, hexKey string) (*TokenStore, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("oauth store: invalid hex key: %w", err)
	}
	if len(key) < 32 {
		return nil, fmt.Errorf("oauth store: key must be at least 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key[:32])
	if err != nil {
		return nil, fmt.Errorf("oauth store: creating cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("oauth store: creating GCM: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("oauth store: applying schema: %w", err)
	}

	// Run migrations for tables created by earlier versions.
	// "duplicate column name" errors are expected and silently ignored.
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return nil, fmt.Errorf("oauth store: migration failed: %w", err)
		}
	}

	return &TokenStore{db: db, gcm: gcm}, nil
}

// Get retrieves a stored token for the given tool. Returns nil if not found.
func (s *TokenStore) Get(toolName string) (*StoredToken, error) {
	var row tokenRow
	err := s.db.Get(&row, "SELECT tool_name, access_token, refresh_token, token_type, expiry, scopes, client_id, client_secret, token_url, auth_style, resource_url FROM oauth_tokens WHERE tool_name = ?", toolName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("oauth store: get %q: %w", toolName, err)
	}
	return s.decryptRow(&row)
}

// Put stores or updates a token for the given tool.
func (s *TokenStore) Put(st *StoredToken) error {
	row, err := s.encryptToken(st)
	if err != nil {
		return fmt.Errorf("oauth store: encrypting token for %q: %w", st.ToolName, err)
	}

	_, err = s.db.Exec(`
		INSERT INTO oauth_tokens (tool_name, access_token, refresh_token, token_type, expiry, scopes, client_id, client_secret, token_url, auth_style, resource_url, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(tool_name) DO UPDATE SET
			access_token = excluded.access_token,
			refresh_token = excluded.refresh_token,
			token_type = excluded.token_type,
			expiry = excluded.expiry,
			scopes = excluded.scopes,
			client_id = excluded.client_id,
			client_secret = excluded.client_secret,
			token_url = excluded.token_url,
			auth_style = excluded.auth_style,
			resource_url = excluded.resource_url,
			updated_at = CURRENT_TIMESTAMP`,
		row.ToolName, row.AccessToken, row.RefreshToken, row.TokenType,
		row.Expiry, row.Scopes, row.ClientID, row.ClientSecret,
		row.TokenURL, row.AuthStyle, row.ResourceURL)
	if err != nil {
		return fmt.Errorf("oauth store: put %q: %w", st.ToolName, err)
	}
	return nil
}

// Delete removes a stored token for the given tool.
func (s *TokenStore) Delete(toolName string) error {
	_, err := s.db.Exec("DELETE FROM oauth_tokens WHERE tool_name = ?", toolName)
	if err != nil {
		return fmt.Errorf("oauth store: delete %q: %w", toolName, err)
	}
	return nil
}

// List returns a summary of all stored tokens.
func (s *TokenStore) List() ([]TokenSummary, error) {
	var rows []tokenRow
	if err := s.db.Select(&rows, "SELECT tool_name, access_token, refresh_token, token_type, expiry, scopes, client_id, client_secret, token_url, auth_style, resource_url FROM oauth_tokens"); err != nil {
		return nil, fmt.Errorf("oauth store: list: %w", err)
	}

	summaries := make([]TokenSummary, 0, len(rows))
	for i := range rows {
		st, err := s.decryptRow(&rows[i])
		if err != nil {
			// Corrupted tokens show as needing reauth.
			summaries = append(summaries, TokenSummary{
				HasToken:    false,
				NeedsReauth: true,
			})
			continue
		}
		summaries = append(summaries, st.Summary())
	}
	return summaries, nil
}

// Summary returns a non-sensitive summary of the token.
// NeedsReauth is only set when the token has expired AND has no refresh token.
// Non-expiring tokens (Expiry == nil, e.g. Todoist) never trigger NeedsReauth.
// Tokens with a refresh token are assumed refreshable even if expired.
func (st *StoredToken) Summary() TokenSummary {
	ts := TokenSummary{
		HasToken:  true,
		ExpiresAt: st.Expiry,
		Scopes:    st.Scopes,
	}
	if st.Expiry != nil && time.Now().After(*st.Expiry) && st.RefreshToken == "" {
		ts.NeedsReauth = true
	}
	return ts
}

// ToOAuth2Token converts to an oauth2.Token.
func (st *StoredToken) ToOAuth2Token() *oauth2.Token {
	t := &oauth2.Token{
		AccessToken:  st.AccessToken,
		RefreshToken: st.RefreshToken,
		TokenType:    st.TokenType,
	}
	if st.Expiry != nil {
		t.Expiry = *st.Expiry
	}
	return t
}

// ToOAuth2Config reconstructs the oauth2.Config for token refresh.
func (st *StoredToken) ToOAuth2Config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     st.ClientID,
		ClientSecret: st.ClientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL:  st.TokenURL,
			AuthStyle: st.AuthStyle,
		},
		Scopes: st.Scopes,
	}
}

// encrypt uses AES-256-GCM with a random nonce prepended to the ciphertext.
func (s *TokenStore) encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}
	return s.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decrypt extracts the nonce and decrypts.
func (s *TokenStore) decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, nil
	}
	nonceSize := s.gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return s.gcm.Open(nil, nonce, ct, nil)
}

func (s *TokenStore) encryptToken(st *StoredToken) (*tokenRow, error) {
	encAccess, err := s.encrypt([]byte(st.AccessToken))
	if err != nil {
		return nil, fmt.Errorf("encrypting access token: %w", err)
	}

	var encRefresh []byte
	if st.RefreshToken != "" {
		encRefresh, err = s.encrypt([]byte(st.RefreshToken))
		if err != nil {
			return nil, fmt.Errorf("encrypting refresh token: %w", err)
		}
	}

	var encSecret []byte
	if st.ClientSecret != "" {
		encSecret, err = s.encrypt([]byte(st.ClientSecret))
		if err != nil {
			return nil, fmt.Errorf("encrypting client secret: %w", err)
		}
	}

	return &tokenRow{
		ToolName:     st.ToolName,
		AccessToken:  encAccess,
		RefreshToken: encRefresh,
		TokenType:    st.TokenType,
		Expiry:       st.Expiry,
		Scopes:       strings.Join(st.Scopes, " "),
		ClientID:     st.ClientID,
		ClientSecret: encSecret,
		TokenURL:     st.TokenURL,
		AuthStyle:    int(st.AuthStyle),
		ResourceURL:  st.ResourceURL,
	}, nil
}

func (s *TokenStore) decryptRow(row *tokenRow) (*StoredToken, error) {
	accessPlain, err := s.decrypt(row.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("decrypting access token: %w", err)
	}

	refreshPlain, err := s.decrypt(row.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("decrypting refresh token: %w", err)
	}

	secretPlain, err := s.decrypt(row.ClientSecret)
	if err != nil {
		return nil, fmt.Errorf("decrypting client secret: %w", err)
	}

	var scopes []string
	if row.Scopes != "" {
		scopes = strings.Fields(row.Scopes)
	}

	return &StoredToken{
		ToolName:     row.ToolName,
		AccessToken:  string(accessPlain),
		RefreshToken: string(refreshPlain),
		TokenType:    row.TokenType,
		Expiry:       row.Expiry,
		Scopes:       scopes,
		ClientID:     row.ClientID,
		ClientSecret: string(secretPlain),
		TokenURL:     row.TokenURL,
		AuthStyle:    oauth2.AuthStyle(row.AuthStyle),
		ResourceURL:  row.ResourceURL,
	}, nil
}
