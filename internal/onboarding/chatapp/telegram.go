package chatapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Telegram implements Prober over the Bot API with long polling.
type Telegram struct {
	BaseURL string       // default https://api.telegram.org
	HTTP    *http.Client // default: a client without a timeout; ctx bounds each call
}

// maxPollSeconds caps one getUpdates long poll.
const maxPollSeconds = 50

type tgResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		From *tgUser `json:"from"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

// call invokes a Bot API method. Errors never contain the request URL.
func (t Telegram) call(ctx context.Context, token, method string, params url.Values, out any) error {
	base := t.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	hc := t.HTTP
	if hc == nil {
		hc = &http.Client{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/bot"+token+"/"+method+"?"+params.Encode(), nil)
	if err != nil {
		return errors.New("telegram: building request failed") // the error text would include the token
	}
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drop the URL, keep the cause
		}
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var r tgResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("telegram: unexpected response (status %d)", resp.StatusCode)
	}
	if !r.OK {
		switch r.ErrorCode {
		case http.StatusUnauthorized, http.StatusNotFound:
			return ErrRejected
		case http.StatusConflict:
			return ErrConflict
		}
		return fmt.Errorf("telegram: %s (code %d)", r.Description, r.ErrorCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(r.Result, out); err != nil {
		return errors.New("telegram: unexpected result shape")
	}
	return nil
}

// Verify calls getMe.
func (t Telegram) Verify(ctx context.Context, token string) (BotInfo, error) {
	var me tgUser
	if err := t.call(ctx, token, "getMe", url.Values{}, &me); err != nil {
		return BotInfo{}, err
	}
	return BotInfo{ID: strconv.FormatInt(me.ID, 10), Username: me.Username, DisplayName: me.FirstName}, nil
}

// WaitForSender long-polls getUpdates. The first call (empty cursor) starts
// after the newest pending update, so a message sent before pairing began,
// possibly by someone else, is never captured. A captured update is
// acknowledged so the adapter does not replay it to the agent after restart.
func (t Telegram) WaitForSender(ctx context.Context, token, cursor string) (Sender, string, error) {
	offset, err := t.startOffset(ctx, token, cursor)
	if err != nil {
		return Sender{}, cursor, err
	}
	params := url.Values{
		"offset":          {strconv.FormatInt(offset, 10)},
		"timeout":         {strconv.Itoa(pollSeconds(ctx))},
		"allowed_updates": {`["message"]`},
	}
	var updates []tgUpdate
	if err := t.call(ctx, token, "getUpdates", params, &updates); err != nil {
		if ctx.Err() != nil {
			return Sender{}, strconv.FormatInt(offset, 10), ErrNoSender
		}
		return Sender{}, strconv.FormatInt(offset, 10), err
	}
	for _, u := range updates {
		offset = u.UpdateID + 1
		m := u.Message
		if m == nil || m.From == nil || m.From.IsBot || m.Chat.Type != "private" {
			continue
		}
		next := strconv.FormatInt(offset, 10)
		ack := url.Values{"offset": {next}, "timeout": {"0"}}
		_ = t.call(context.WithoutCancel(ctx), token, "getUpdates", ack, nil) // best effort
		return Sender{
			ID:        strconv.FormatInt(m.From.ID, 10),
			Username:  m.From.Username,
			FirstName: m.From.FirstName,
			ChatID:    strconv.FormatInt(m.Chat.ID, 10),
			Text:      trimText(m.Text),
		}, next, nil
	}
	return Sender{}, strconv.FormatInt(offset, 10), ErrNoSender
}

// startOffset parses cursor, or on the first call returns the offset just
// past the newest pending update (0 when there is none).
func (t Telegram) startOffset(ctx context.Context, token, cursor string) (int64, error) {
	if cursor != "" {
		n, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil {
			return 0, errors.New("chatapp: invalid cursor")
		}
		return n, nil
	}
	var latest []tgUpdate
	if err := t.call(ctx, token, "getUpdates", url.Values{"offset": {"-1"}, "timeout": {"0"}}, &latest); err != nil {
		return 0, err
	}
	if len(latest) == 0 {
		return 0, nil
	}
	return latest[len(latest)-1].UpdateID + 1, nil
}

// pollSeconds is the long-poll timeout that ends just before ctx does.
func pollSeconds(ctx context.Context) int {
	dl, ok := ctx.Deadline()
	if !ok {
		return maxPollSeconds
	}
	s := int(time.Until(dl).Seconds()) - 2
	return max(0, min(s, maxPollSeconds))
}

// Notify calls sendMessage.
func (t Telegram) Notify(ctx context.Context, token, chatID, text string) error {
	return t.call(ctx, token, "sendMessage", url.Values{"chat_id": {chatID}, "text": {text}}, nil)
}
