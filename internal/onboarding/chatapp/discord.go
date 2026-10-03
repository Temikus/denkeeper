package chatapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Discord implements Prober over the REST API. Pairing needs a gateway
// session to see a DM, so v1 leaves it to the user: WaitForSender returns
// ErrUnsupported and the wizard asks for the user ID instead.
type Discord struct {
	BaseURL string       // default https://discord.com/api/v10
	HTTP    *http.Client // default: no timeout (ctx bounds each call); redirects always refused
}

// discordInvitePerms is View Channels + Send Messages + Read Message History.
const discordInvitePerms = "68608"

// Verify calls GET /users/@me with the bot token.
func (d Discord) Verify(ctx context.Context, token string) (BotInfo, error) {
	base := d.BaseURL
	if base == "" {
		base = "https://discord.com/api/v10"
	}
	hc := &http.Client{}
	if d.HTTP != nil {
		clone := *d.HTTP
		hc = &clone
	}
	// Refuse redirects: one would carry the token header with it.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/users/@me", nil)
	if err != nil {
		return BotInfo{}, err
	}
	req.Header.Set("Authorization", "Bot "+token)
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return BotInfo{}, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return BotInfo{}, ErrRejected
	default:
		return BotInfo{}, fmt.Errorf("discord: unexpected status %d", resp.StatusCode)
	}
	var me struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return BotInfo{}, errors.New("discord: unexpected response")
	}
	name := me.GlobalName
	if name == "" {
		name = me.Username
	}
	return BotInfo{
		ID:          me.ID,
		Username:    me.Username,
		DisplayName: name,
		// A bot's user ID is its application ID, which the invite needs.
		InviteURL: "https://discord.com/oauth2/authorize?client_id=" + url.QueryEscape(me.ID) +
			"&scope=bot&permissions=" + discordInvitePerms,
	}, nil
}

// WaitForSender is not supported for Discord yet.
func (Discord) WaitForSender(context.Context, string, string) (Sender, string, error) {
	return Sender{}, "", ErrUnsupported
}

// Notify is not supported for Discord yet; it is a no-op.
func (Discord) Notify(context.Context, string, string, string) error { return nil }
