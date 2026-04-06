package oauth

import (
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
	"golang.org/x/oauth2"
)

func testKey() string {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return hex.EncodeToString(key)
}

func testStore(t *testing.T) *TokenStore {
	t.Helper()
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store, err := NewTokenStore(db, testKey())
	if err != nil {
		t.Fatalf("creating token store: %v", err)
	}
	return store
}

func TestTokenStore_PutGet_RoundTrip(t *testing.T) {
	store := testStore(t)
	expiry := time.Now().Add(time.Hour).Truncate(time.Second)

	st := &StoredToken{
		ToolName:     "github-mcp",
		AccessToken:  "access-token-12345",
		RefreshToken: "refresh-token-67890",
		TokenType:    "Bearer",
		Expiry:       &expiry,
		Scopes:       []string{"repo", "read:org"},
		ClientID:     "client-id-abc",
		ClientSecret: "client-secret-xyz",
		TokenURL:     "https://github.com/login/oauth/access_token",
		AuthStyle:    oauth2.AuthStyleInParams,
		ResourceURL:  "https://github-mcp.example.com/mcp",
	}

	if err := store.Put(st); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := store.Get("github-mcp")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("get returned nil")
	}

	if got.AccessToken != st.AccessToken {
		t.Errorf("access token: got %q, want %q", got.AccessToken, st.AccessToken)
	}
	if got.RefreshToken != st.RefreshToken {
		t.Errorf("refresh token: got %q, want %q", got.RefreshToken, st.RefreshToken)
	}
	if got.TokenType != st.TokenType {
		t.Errorf("token type: got %q, want %q", got.TokenType, st.TokenType)
	}
	if got.ClientID != st.ClientID {
		t.Errorf("client id: got %q, want %q", got.ClientID, st.ClientID)
	}
	if got.ClientSecret != st.ClientSecret {
		t.Errorf("client secret: got %q, want %q", got.ClientSecret, st.ClientSecret)
	}
	if got.TokenURL != st.TokenURL {
		t.Errorf("token url: got %q, want %q", got.TokenURL, st.TokenURL)
	}
	if got.AuthStyle != st.AuthStyle {
		t.Errorf("auth style: got %d, want %d", got.AuthStyle, st.AuthStyle)
	}
	if got.ResourceURL != st.ResourceURL {
		t.Errorf("resource url: got %q, want %q", got.ResourceURL, st.ResourceURL)
	}
	if len(got.Scopes) != 2 || got.Scopes[0] != "repo" || got.Scopes[1] != "read:org" {
		t.Errorf("scopes: got %v, want [repo read:org]", got.Scopes)
	}
}

func TestTokenStore_Get_NotFound(t *testing.T) {
	store := testStore(t)

	got, err := store.Get("nonexistent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestTokenStore_Put_Upsert(t *testing.T) {
	store := testStore(t)

	st := &StoredToken{
		ToolName:    "todoist",
		AccessToken: "token-v1",
		TokenType:   "Bearer",
	}
	if err := store.Put(st); err != nil {
		t.Fatalf("put v1: %v", err)
	}

	st.AccessToken = "token-v2"
	if err := store.Put(st); err != nil {
		t.Fatalf("put v2: %v", err)
	}

	got, err := store.Get("todoist")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AccessToken != "token-v2" {
		t.Errorf("access token: got %q, want %q", got.AccessToken, "token-v2")
	}
}

func TestTokenStore_Delete(t *testing.T) {
	store := testStore(t)

	st := &StoredToken{
		ToolName:    "todoist",
		AccessToken: "token-123",
		TokenType:   "Bearer",
	}
	if err := store.Put(st); err != nil {
		t.Fatalf("put: %v", err)
	}

	if err := store.Delete("todoist"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := store.Get("todoist")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}

func TestTokenStore_Delete_NonExistent(t *testing.T) {
	store := testStore(t)

	if err := store.Delete("nonexistent"); err != nil {
		t.Fatalf("delete should not error for nonexistent: %v", err)
	}
}

func TestTokenStore_List(t *testing.T) {
	store := testStore(t)

	expiry := time.Now().Add(time.Hour).Truncate(time.Second)
	for _, name := range []string{"tool-a", "tool-b"} {
		st := &StoredToken{
			ToolName:    name,
			AccessToken: "tok-" + name,
			TokenType:   "Bearer",
			Expiry:      &expiry,
			Scopes:      []string{"read"},
		}
		if err := store.Put(st); err != nil {
			t.Fatalf("put %s: %v", name, err)
		}
	}

	summaries, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}
	for _, s := range summaries {
		if !s.HasToken {
			t.Error("expected has_token to be true")
		}
		if s.NeedsReauth {
			t.Error("expected needs_reauth to be false for valid token")
		}
	}
}

func TestTokenStore_Summary_ExpiredNoRefresh(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	st := &StoredToken{
		ToolName:    "expired-tool",
		AccessToken: "expired",
		TokenType:   "Bearer",
		Expiry:      &past,
	}
	summary := st.Summary()
	if !summary.NeedsReauth {
		t.Error("expected needs_reauth for expired token without refresh")
	}
}

func TestTokenStore_Summary_ExpiredWithRefresh(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	st := &StoredToken{
		ToolName:     "refresh-tool",
		AccessToken:  "expired",
		RefreshToken: "refresh-123",
		TokenType:    "Bearer",
		Expiry:       &past,
	}
	summary := st.Summary()
	if summary.NeedsReauth {
		t.Error("should not need reauth when refresh token is available")
	}
}

func TestTokenStore_ToOAuth2Token(t *testing.T) {
	expiry := time.Now().Add(time.Hour)
	st := &StoredToken{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		TokenType:    "Bearer",
		Expiry:       &expiry,
	}
	tok := st.ToOAuth2Token()
	if tok.AccessToken != "access-123" {
		t.Errorf("access token: got %q", tok.AccessToken)
	}
	if tok.RefreshToken != "refresh-456" {
		t.Errorf("refresh token: got %q", tok.RefreshToken)
	}
}

func TestNewTokenStore_ShortKey(t *testing.T) {
	db, _ := sqlx.Open("sqlite", ":memory:")
	defer db.Close()

	_, err := NewTokenStore(db, "abcd")
	if err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestNewTokenStore_InvalidHex(t *testing.T) {
	db, _ := sqlx.Open("sqlite", ":memory:")
	defer db.Close()

	_, err := NewTokenStore(db, "not-hex")
	if err == nil {
		t.Fatal("expected error for invalid hex")
	}
}

func TestTokenStore_EmptyOptionalFields(t *testing.T) {
	store := testStore(t)

	st := &StoredToken{
		ToolName:    "minimal-tool",
		AccessToken: "tok",
		TokenType:   "Bearer",
	}
	if err := store.Put(st); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := store.Get("minimal-tool")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RefreshToken != "" {
		t.Errorf("expected empty refresh token, got %q", got.RefreshToken)
	}
	if got.ClientSecret != "" {
		t.Errorf("expected empty client secret, got %q", got.ClientSecret)
	}
	if len(got.Scopes) != 0 {
		t.Errorf("expected no scopes, got %v", got.Scopes)
	}
}

func TestTokenStore_MigratesOldSchema(t *testing.T) {
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	defer db.Close()

	// Create an old-schema table missing the new columns.
	oldSchema := `CREATE TABLE oauth_tokens (
		tool_name     TEXT PRIMARY KEY,
		access_token  BLOB NOT NULL,
		refresh_token BLOB,
		token_type    TEXT NOT NULL DEFAULT 'Bearer',
		expiry        DATETIME,
		created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
	)`
	if _, err := db.Exec(oldSchema); err != nil {
		t.Fatalf("creating old schema: %v", err)
	}

	// Open a TokenStore — should migrate the schema by adding missing columns.
	store, err := NewTokenStore(db, testKey())
	if err != nil {
		t.Fatalf("creating token store with old schema: %v", err)
	}

	// After migration, we should be able to store and retrieve tokens
	// with the new columns (client_id, client_secret, token_url, etc.).
	expiry := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := store.Put(&StoredToken{
		ToolName:     "migrated-tool",
		AccessToken:  "access",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		Expiry:       &expiry,
		ClientID:     "migrated-client",
		ClientSecret: "migrated-secret",
		TokenURL:     "https://auth.example.com/token",
		AuthStyle:    oauth2.AuthStyleInParams,
	}); err != nil {
		t.Fatalf("put after migration: %v", err)
	}

	got, err := store.Get("migrated-tool")
	if err != nil {
		t.Fatalf("get after migration: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil token")
	}
	if got.ClientID != "migrated-client" {
		t.Errorf("client id: got %q, want %q", got.ClientID, "migrated-client")
	}
	if got.ClientSecret != "migrated-secret" {
		t.Errorf("client secret: got %q, want %q", got.ClientSecret, "migrated-secret")
	}
	if got.TokenURL != "https://auth.example.com/token" {
		t.Errorf("token url: got %q", got.TokenURL)
	}
	if got.AuthStyle != oauth2.AuthStyleInParams {
		t.Errorf("auth style: got %d, want %d", got.AuthStyle, oauth2.AuthStyleInParams)
	}
}
