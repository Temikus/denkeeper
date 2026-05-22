package api

import (
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) handleLLMsTxt(w http.ResponseWriter, _ *http.Request) {
	var b strings.Builder

	b.WriteString(`# Denkeeper

> A single-binary personal AI agent. Connects to Telegram/Discord,
> routes through Anthropic/OpenAI/OpenRouter/Ollama, and exposes a
> full REST API for programmatic control.

`)

	if s.cfg.ExternalURL != "" {
		fmt.Fprintf(&b, "## Base URL\n\n%s\n\n", s.cfg.ExternalURL)
	}

	b.WriteString(`## Authentication

All API endpoints (except /api/v1/health, /llms.txt, /api/v1/openapi.json)
require a Bearer token:

    Authorization: Bearer <api-key>

API keys are scoped. Required scopes are noted per endpoint below.

## Key Endpoints

- POST /api/v1/chat                    (scope: chat)            Send a message; stream with Accept: text/event-stream
- GET  /api/v1/ws                      (scope: chat)            WebSocket for bidirectional streaming
- GET  /api/v1/sessions                (scope: sessions:read)   List conversations
- GET  /api/v1/agents                  (scope: agents:read)     List configured agents
- GET  /api/v1/approvals               (scope: approvals:read)  Pending tool-call approvals
- POST /api/v1/approvals/{id}/approve  (scope: approvals:write) Approve a pending tool call
- POST /api/v1/panic                   (scope: admin)           Emergency stop all in-flight requests

## Full API Reference

- OpenAPI 2.0 spec: GET /api/v1/openapi.json  (no auth required)
`)

	if s.deps.Config != nil && s.deps.Config.API.IsMCPServerEnabled() {
		fmt.Fprintf(&b, "## MCP Server\n\nAn MCP (Model Context Protocol) server is available at:\n\n    %s\n\n", s.mcpServerEndpoint())
		b.WriteString("Authenticate with a Bearer token (same API keys). Supports tools for\n")
		b.WriteString("agent chat, skill/schedule/KV CRUD, session management, and telemetry.\n\n")
	}

	if s.deps.Dispatcher != nil && s.deps.Config != nil {
		names := s.deps.Dispatcher.Agents()
		if len(names) > 0 {
			descMap := make(map[string]string, len(s.deps.Config.Agents))
			for _, ac := range s.deps.Config.Agents {
				descMap[ac.Name] = ac.Description
			}
			b.WriteString("## Agents\n\n")
			for _, name := range names {
				if desc := descMap[name]; desc != "" {
					fmt.Fprintf(&b, "- %s — %s\n", name, desc)
				} else {
					fmt.Fprintf(&b, "- %s\n", name)
				}
			}
			b.WriteString("\n")
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}
