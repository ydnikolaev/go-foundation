package alert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTelegramURL is the Bot API origin.
const DefaultTelegramURL = "https://api.telegram.org"

// TelegramConfig configures the Telegram adapter.
type TelegramConfig struct {
	Token  string // the bot token; the caller reads it from its own configuration
	ChatID string // the chat the alerts go to

	BaseURL string       // nil-value = DefaultTelegramURL; tests point it at a fake server
	Client  *http.Client // nil = a client with a 15 s timeout
}

// Telegram sends alerts through the Bot API sendMessage method: form fields chat_id and text only (plain text, no
// parse_mode, previews off). The token is part of the request URL, so no error leaves this type carrying a URL: a
// transport failure would otherwise print `bot<token>` into the logs (*url.Error does exactly that).
type Telegram struct {
	endpoint string
	token    string
	chatID   string
	client   *http.Client
}

var _ Notifier = (*Telegram)(nil)

// NewTelegram validates cfg and returns the adapter.
func NewTelegram(cfg TelegramConfig) (*Telegram, error) {
	if cfg.Token == "" || cfg.ChatID == "" {
		return nil, errors.New("telegram alerts need a token and a chat id")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultTelegramURL
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Telegram{endpoint: base + "/bot" + cfg.Token + "/sendMessage", token: cfg.Token, chatID: cfg.ChatID, client: client}, nil
}

// telegramReply is the part of the Bot API answer the adapter reads.
type telegramReply struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// Notify implements Notifier. A 429 returns *RateLimitError; every other failure is an error without the token.
func (t *Telegram) Notify(ctx context.Context, ev Event) error {
	form := url.Values{
		"chat_id":                  {t.chatID},
		"text":                     {ev.Text()},
		"disable_web_page_preview": {"true"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return t.scrub(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client.Do(req)
	if err != nil {
		return t.scrub(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var reply telegramReply
	_ = json.Unmarshal(body, &reply) // a non-JSON body leaves the zero value: the status is still reported
	if resp.StatusCode == http.StatusTooManyRequests {
		return &RateLimitError{RetryAfter: time.Duration(reply.Parameters.RetryAfter) * time.Second}
	}
	return t.scrub(fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(reply.Description, maxFieldLen)))
}

// scrub turns err into a plain error that cannot hold the request URL or the token. The *url.Error wrapper is
// dropped (it prints the URL) and the token is masked in what remains.
func (t *Telegram) scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("telegram sendMessage: %s", strings.ReplaceAll(err.Error(), t.token, "<token>"))
}
