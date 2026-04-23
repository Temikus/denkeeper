package configmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerChannelTools adds the channel MCP tools to the server.
// Called from registerTools when GetChannels is available.
func (s *Server) registerChannelTools() {
	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "channel_list",
		Description: "List all configured channels with their agent, adapter bindings, delivery mode, and active adapter keys.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}, s.handleChannelList)

	if s.deps.SetActiveChannel != nil {
		s.mcpServer.AddTool(&mcp.Tool{
			Name:        "channel_switch",
			Description: "Switch the active channel for an adapter key. Equivalent to the /session command.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"adapter_key":  {"type": "string", "description": "The adapter key to switch, e.g. \"telegram:12345\""},
					"channel_name": {"type": "string", "description": "The channel name to switch to"}
				},
				"required": ["adapter_key", "channel_name"]
			}`),
		}, s.handleChannelSwitch)
	}

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "channel_info",
		Description: "Get detailed information about a specific channel including agent, adapters, conversation ID, and active adapter keys.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"channel_name": {"type": "string", "description": "The channel name to look up"}
			},
			"required": ["channel_name"]
		}`),
	}, s.handleChannelInfo)
}

func (s *Server) handleChannelList(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	channels := s.deps.GetChannels()
	if len(channels) == 0 {
		return toolText("[]"), nil
	}

	type channelSummary struct {
		Name              string   `json:"name"`
		Agent             string   `json:"agent"`
		Adapters          []string `json:"adapters"`
		Delivery          string   `json:"delivery,omitempty"`
		Implicit          bool     `json:"implicit,omitempty"`
		SessionMode       string   `json:"session_mode,omitempty"`
		ActiveAdapterKeys []string `json:"active_adapter_keys,omitempty"`
	}

	summaries := make([]channelSummary, 0, len(channels))
	for _, ch := range channels {
		cs := channelSummary{
			Name:        ch.Name,
			Agent:       ch.AgentName,
			Adapters:    ch.Adapters,
			Delivery:    ch.Delivery,
			Implicit:    ch.Implicit,
			SessionMode: ch.SessionMode,
		}
		if s.deps.ActiveChannelsForChannel != nil {
			cs.ActiveAdapterKeys = s.deps.ActiveChannelsForChannel(ch.Name)
		}
		summaries = append(summaries, cs)
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Name < summaries[j].Name
	})

	data, err := json.MarshalIndent(summaries, "", "  ")
	if err != nil {
		return toolError("marshaling channels: " + err.Error()), nil
	}
	return toolText(string(data)), nil
}

func (s *Server) handleChannelSwitch(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		AdapterKey  string `json:"adapter_key"`
		ChannelName string `json:"channel_name"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if input.AdapterKey == "" {
		return toolError("adapter_key is required"), nil
	}
	if input.ChannelName == "" {
		return toolError("channel_name is required"), nil
	}

	if err := s.deps.SetActiveChannel(ctx, input.AdapterKey, input.ChannelName); err != nil {
		return toolError(fmt.Sprintf("channel_switch failed: %v", err)), nil
	}

	resp, _ := json.Marshal(map[string]string{
		"ok":          "true",
		"channel":     input.ChannelName,
		"adapter_key": input.AdapterKey,
	})
	return toolText(string(resp)), nil
}

func (s *Server) handleChannelInfo(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		ChannelName string `json:"channel_name"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if input.ChannelName == "" {
		return toolError("channel_name is required"), nil
	}

	channels := s.deps.GetChannels()
	ch, ok := channels[input.ChannelName]
	if !ok {
		return toolError(fmt.Sprintf("channel %q not found", input.ChannelName)), nil
	}

	type channelDetail struct {
		Name              string   `json:"name"`
		Agent             string   `json:"agent"`
		Adapters          []string `json:"adapters"`
		Delivery          string   `json:"delivery,omitempty"`
		Implicit          bool     `json:"implicit,omitempty"`
		SessionMode       string   `json:"session_mode,omitempty"`
		ConversationID    string   `json:"conversation_id"`
		ActiveAdapterKeys []string `json:"active_adapter_keys,omitempty"`
	}

	detail := channelDetail{
		Name:           ch.Name,
		Agent:          ch.AgentName,
		Adapters:       ch.Adapters,
		Delivery:       ch.Delivery,
		Implicit:       ch.Implicit,
		SessionMode:    ch.SessionMode,
		ConversationID: ch.ConversationID(),
	}
	if s.deps.ActiveChannelsForChannel != nil {
		detail.ActiveAdapterKeys = s.deps.ActiveChannelsForChannel(ch.Name)
	}

	data, err := json.MarshalIndent(detail, "", "  ")
	if err != nil {
		return toolError("marshaling channel info: " + err.Error()), nil
	}
	return toolText(string(data)), nil
}
