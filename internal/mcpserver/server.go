package mcpserver

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server is the HTTP-facing MCP server that exposes Denkeeper tools to
// external MCP clients via Streamable HTTP or SSE transport.
type Server struct {
	mcpServer *mcp.Server
	deps      Deps
	cfg       config.APIMCPServerConfig
}

// New creates the MCP server and registers all tools. The returned server is
// not yet listening; call Handler() to get the http.Handler to mount.
func New(cfg config.APIMCPServerConfig, deps Deps) *Server {
	instructions := "Denkeeper personal AI agent. " +
		"Use the chat tool to send messages to agents. " +
		"Use agent_list to discover available agents. " +
		"All tools require an API key with appropriate scopes."

	s := &Server{
		mcpServer: mcp.NewServer(
			&mcp.Implementation{Name: "denkeeper", Version: deps.Version},
			&mcp.ServerOptions{Instructions: instructions, Logger: deps.Logger},
		),
		deps: deps,
		cfg:  cfg,
	}
	s.registerTools()
	return s
}

// Handler returns an http.Handler that serves MCP over the configured
// transport, with Bearer token auth middleware.
func (s *Server) Handler() http.Handler {
	var handler http.Handler
	switch s.cfg.Transport {
	case "sse":
		handler = mcp.NewSSEHandler(
			func(*http.Request) *mcp.Server { return s.mcpServer },
			nil,
		)
	default:
		apiCfg := config.APIConfig{MCPServer: s.cfg}
		timeout := apiCfg.MCPServerSessionTimeout()
		handler = mcp.NewStreamableHTTPHandler(
			func(*http.Request) *mcp.Server { return s.mcpServer },
			&mcp.StreamableHTTPOptions{
				Stateless:      s.cfg.Stateless,
				SessionTimeout: timeout,
				Logger:         s.deps.Logger,
			},
		)
	}
	return s.authMiddleware(handler)
}

// authMiddleware extracts a Bearer token and performs two-tier key lookup:
// 1. SQLite-managed keys via KeyStore.FindScopesByToken
// 2. TOML-configured keys (backward compatible)
// On match, scopes and key name are injected into the request context.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if token == "" {
			writeJSONError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}

		name, scopes, found := s.lookupToken(r, token)
		if !found {
			writeJSONError(w, http.StatusUnauthorized, "invalid API key")
			return
		}

		ctx := withScopes(r.Context(), scopes)
		ctx = withKeyName(ctx, name)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) lookupToken(r *http.Request, token string) (string, []string, bool) {
	if s.deps.KeyStore != nil {
		name, scopes, found := s.deps.KeyStore.FindScopesByToken(r.Context(), token)
		if found {
			return name, scopes, true
		}
	}

	for _, k := range s.deps.TOMLKeys {
		if subtle.ConstantTimeCompare([]byte(token), []byte(k.Key)) == 1 {
			return k.Name, k.Scopes, true
		}
	}

	return "", nil, false
}

func extractBearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(h, "Bearer ")
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) registerTools() {
	s.registerAgentTools()
	s.registerSessionTools()
	s.registerChannelTools()
	s.registerTelemetryTools()
	s.registerSafetyTools()
	s.registerChatTools()
	s.registerSkillTools()
	s.registerScheduleTools()
	s.registerKVTools()
	s.registerApprovalTools()
	s.registerToolMgmtTools()
	s.registerAuditTools()
}
