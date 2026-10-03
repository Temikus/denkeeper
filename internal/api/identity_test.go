package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/persona"
)

func putIdentity(t *testing.T, srv *Server, agent, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agent+"/identity", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

func TestUpdateIdentity_WritesFrontmatterAndPreservesBody(t *testing.T) {
	deps := testDepsWithPersona(t)
	e := deps.Dispatcher.Agent("default")
	if err := e.SavePersonaSection("identity", "---\nname: Old\n---\n\nKeep this note."); err != nil {
		t.Fatal(err)
	}
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := putIdentity(t, srv, "default", `{"name":"Den","emoji":"🦊","theme":"helpful general-purpose assistant"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	content, _, _, _ := e.PersonaSection("identity")
	id, err := persona.ParseIdentity(content)
	if err != nil {
		t.Fatalf("written identity does not parse: %v\n%s", err, content)
	}
	if id.Name != "Den" || id.Emoji != "🦊" || id.Theme != "helpful general-purpose assistant" {
		t.Errorf("identity = %+v", *id)
	}
	if id.Body != "Keep this note." {
		t.Errorf("body = %q, want the existing body kept", id.Body)
	}
}

func TestUpdateIdentity_YAMLInjectionStaysScalar(t *testing.T) {
	deps := testDepsWithPersona(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := putIdentity(t, srv, "default", `{"name":"Den","emoji":"🦊","theme":"x\"\nemoji: \"evil"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	content, _, _, _ := deps.Dispatcher.Agent("default").PersonaSection("identity")
	id, _ := persona.ParseIdentity(content)
	if id.Emoji != "🦊" {
		t.Errorf("emoji = %q, want the theme unable to overwrite it", id.Emoji)
	}
}

func TestUpdateIdentity_NotFound(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithPersona(t), testLogger())
	if rec := putIdentity(t, srv, "nobody", `{"name":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestUpdateIdentity_TooLong(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithPersona(t), testLogger())
	long := strings.Repeat("a", identityNameMax+1)
	if rec := putIdentity(t, srv, "default", `{"name":"`+long+`"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUpdateIdentity_MultilineEmojiRejected(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithPersona(t), testLogger())
	if rec := putIdentity(t, srv, "default", `{"name":"Den","emoji":"a\nb"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
