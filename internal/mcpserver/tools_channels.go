package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type channelListInput struct{}

func (s *Server) registerChannelTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "channel_list",
		Description: "List all configured channels with their agent, adapter bindings, and " +
			"delivery mode. Requires 'channels:read' scope.",
	}, s.handleChannelList)
}

func (s *Server) handleChannelList(ctx context.Context, _ *mcp.CallToolRequest, _ channelListInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "channels:read"); err != nil {
		return err, nil, nil
	}

	type channelInfo struct {
		Name     string   `json:"name"`
		Agent    string   `json:"agent"`
		Adapters []string `json:"adapters"`
		Delivery string   `json:"delivery,omitempty"`
		Implicit bool     `json:"implicit"`
	}

	channels := s.deps.Dispatcher.Channels()
	result := make([]channelInfo, 0, len(channels))
	for _, ch := range channels {
		result = append(result, channelInfo{
			Name:     ch.Name,
			Agent:    ch.AgentName,
			Adapters: ch.Adapters,
			Delivery: ch.Delivery,
			Implicit: ch.Implicit,
		})
	}

	r, err := toolJSON(result)
	return r, nil, err
}
