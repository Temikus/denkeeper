package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

const keysSchema = `
CREATE TABLE IF NOT EXISTS api_keys (
	id           TEXT     PRIMARY KEY,
	name         TEXT     NOT NULL,
	key_hash     TEXT     NOT NULL UNIQUE,
	scopes       TEXT     NOT NULL,
	created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	last_used_at DATETIME,
	revoked_at   DATETIME
);
`

// storedKey is the SQLite row type for api_keys.
type storedKey struct {
	ID         string     `db:"id"`
	Name       string     `db:"name"`
	KeyHash    string     `db:"key_hash"`
	ScopesJSON string     `db:"scopes"`
	CreatedAt  time.Time  `db:"created_at"`
	LastUsedAt *time.Time `db:"last_used_at"`
	RevokedAt  *time.Time `db:"revoked_at"`
}

// APIKeyRecord is the public representation returned by the API (no hash exposed).
type APIKeyRecord struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Revoked    bool       `json:"revoked"`
}

// KeyStore manages API keys persisted in SQLite.
type KeyStore struct {
	db *sqlx.DB
}

// NewKeyStore opens (or creates) a SQLite DB at dbPath and applies the key schema.
// WAL mode is used so it can coexist with other connections to the same file.
func NewKeyStore(dbPath string) (*KeyStore, error) {
	db, err := sqlx.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("api keys: open db: %w", err)
	}
	if _, err := db.Exec(keysSchema); err != nil {
		return nil, fmt.Errorf("api keys: apply schema: %w", err)
	}
	return &KeyStore{db: db}, nil
}

// NewInMemoryKeyStore creates a KeyStore backed by an in-memory SQLite database.
// Intended for tests.
func NewInMemoryKeyStore() (*KeyStore, error) {
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("api keys: open in-memory db: %w", err)
	}
	if _, err := db.Exec(keysSchema); err != nil {
		return nil, fmt.Errorf("api keys: apply schema: %w", err)
	}
	return &KeyStore{db: db}, nil
}

// makeKey generates a cryptographically random API key in "dk_<base64url>" format.
// Returns the plaintext key and its SHA-256 hex hash.
func makeKey() (plaintext, keyHash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", fmt.Errorf("reading random bytes: %w", err)
	}
	plaintext = "dk_" + base64.RawURLEncoding.EncodeToString(b)
	keyHash = hashToken(plaintext)
	return plaintext, keyHash, nil
}

// hashToken returns the SHA-256 hex hash of token.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// toRecord converts a storedKey to an APIKeyRecord (no hash or internal fields exposed).
func toRecord(sk storedKey) APIKeyRecord {
	var scopes []string
	_ = json.Unmarshal([]byte(sk.ScopesJSON), &scopes)
	if scopes == nil {
		scopes = []string{}
	}
	return APIKeyRecord{
		ID:         sk.ID,
		Name:       sk.Name,
		Scopes:     scopes,
		CreatedAt:  sk.CreatedAt,
		LastUsedAt: sk.LastUsedAt,
		Revoked:    sk.RevokedAt != nil,
	}
}

// Create inserts a new API key. Returns the record and plaintext key (shown once).
func (ks *KeyStore) Create(ctx context.Context, name string, scopes []string) (APIKeyRecord, string, error) {
	plaintext, keyHash, err := makeKey()
	if err != nil {
		return APIKeyRecord{}, "", err
	}
	scopesJSON, err := json.Marshal(scopes)
	if err != nil {
		return APIKeyRecord{}, "", fmt.Errorf("marshaling scopes: %w", err)
	}
	id := generateID()
	now := time.Now().UTC()
	_, err = ks.db.ExecContext(ctx,
		`INSERT INTO api_keys (id, name, key_hash, scopes, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, name, keyHash, string(scopesJSON), now,
	)
	if err != nil {
		return APIKeyRecord{}, "", fmt.Errorf("inserting key: %w", err)
	}
	rec := APIKeyRecord{
		ID:        id,
		Name:      name,
		Scopes:    scopes,
		CreatedAt: now,
	}
	return rec, plaintext, nil
}

// List returns all key records ordered by creation date descending.
func (ks *KeyStore) List(ctx context.Context) ([]APIKeyRecord, error) {
	var rows []storedKey
	err := ks.db.SelectContext(ctx, &rows,
		`SELECT id, name, key_hash, scopes, created_at, last_used_at, revoked_at FROM api_keys ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing keys: %w", err)
	}
	recs := make([]APIKeyRecord, len(rows))
	for i, sk := range rows {
		recs[i] = toRecord(sk)
	}
	return recs, nil
}

// FindActiveByHash returns the matching active key row for a given token hash, or nil if not found.
func (ks *KeyStore) FindActiveByHash(ctx context.Context, tokenHash string) (*storedKey, error) {
	var sk storedKey
	err := ks.db.GetContext(ctx, &sk,
		`SELECT id, name, key_hash, scopes, created_at, last_used_at, revoked_at FROM api_keys WHERE key_hash = ? AND revoked_at IS NULL`,
		tokenHash,
	)
	if err != nil {
		return nil, nil // not found — treat as no match
	}
	return &sk, nil
}

// TouchLastUsed updates last_used_at for the given key ID (best-effort, non-fatal).
func (ks *KeyStore) TouchLastUsed(ctx context.Context, id string) {
	_, _ = ks.db.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = ? WHERE id = ?`,
		time.Now().UTC(), id,
	)
}

// Revoke marks a key as revoked. Returns an error if the key does not exist or is already revoked.
func (ks *KeyStore) Revoke(ctx context.Context, id string) error {
	res, err := ks.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("revoking key: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("key not found or already revoked")
	}
	return nil
}

// Rotate revokes the existing key and creates a replacement with the same name and scopes.
// Returns the new record and plaintext key.
func (ks *KeyStore) Rotate(ctx context.Context, id string) (APIKeyRecord, string, error) {
	var sk storedKey
	if err := ks.db.GetContext(ctx, &sk,
		`SELECT name, scopes FROM api_keys WHERE id = ? AND revoked_at IS NULL`, id,
	); err != nil {
		return APIKeyRecord{}, "", fmt.Errorf("key not found or already revoked")
	}
	if _, err := ks.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = ? WHERE id = ?`, time.Now().UTC(), id,
	); err != nil {
		return APIKeyRecord{}, "", fmt.Errorf("revoking old key: %w", err)
	}
	var scopes []string
	_ = json.Unmarshal([]byte(sk.ScopesJSON), &scopes)
	return ks.Create(ctx, sk.Name, scopes)
}
