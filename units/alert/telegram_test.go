package alert

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

const testToken = "123456:SECRET-token-value"

// fakeTelegram records the sendMessage requests it gets.
type fakeTelegram struct {
	*httptest.Server
	path  string
	forms []url.Values
}

func newFakeTelegram(t *testing.T, status int, body string) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.path = r.URL.Path
		f.forms = append(f.forms, r.PostForm)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)
	return f
}

func newTestTelegram(t *testing.T, baseURL string) *Telegram {
	t.Helper()
	tg, err := NewTelegram(TelegramConfig{Token: testToken, ChatID: "-100500", BaseURL: baseURL})
	if err != nil {
		t.Fatalf("new telegram: %v", err)
	}
	return tg
}

func TestTelegramSendsChatIDAndTextOnly(t *testing.T) {
	t.Parallel()
	f := newFakeTelegram(t, http.StatusOK, `{"ok":true}`)
	tg := newTestTelegram(t, f.URL)
	ev := httpEvent("GET /v1/x")
	if err := tg.Notify(context.Background(), ev); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if want := "/bot" + testToken + "/sendMessage"; f.path != want {
		t.Errorf("path = %q, want %q", f.path, want)
	}
	if len(f.forms) != 1 {
		t.Fatalf("requests = %d, want 1", len(f.forms))
	}
	form := f.forms[0]
	if form.Get("chat_id") != "-100500" || form.Get("text") != ev.Text() {
		t.Errorf("form = %v, want chat_id and the event text", form)
	}
	if _, ok := form["parse_mode"]; ok {
		t.Errorf("parse_mode was sent: %v", form)
	}
	var keys []string
	for k := range form {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"chat_id", "disable_web_page_preview", "text"}; !slices.Equal(keys, want) {
		t.Errorf("form fields = %v, want %v", keys, want)
	}
}

func TestTelegramRateLimitCarriesRetryAfter(t *testing.T) {
	t.Parallel()
	f := newFakeTelegram(t, http.StatusTooManyRequests,
		`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`)
	err := newTestTelegram(t, f.URL).Notify(context.Background(), httpEvent("GET /v1/x"))
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 7*time.Second {
		t.Fatalf("err = %v, want a RateLimitError of 7s", err)
	}
}

func TestTelegramErrorStatusNamesTheReasonNotTheToken(t *testing.T) {
	t.Parallel()
	f := newFakeTelegram(t, http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	err := newTestTelegram(t, f.URL).Notify(context.Background(), httpEvent("GET /v1/x"))
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("err = %v, want the status and the description", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error carries the token: %v", err)
	}
}

// A transport error is a *url.Error, which prints the request URL and with it `bot<token>`. The adapter's error and
// the log line written for it must hold neither.
func TestTelegramTransportErrorNeverCarriesTheToken(t *testing.T) {
	t.Parallel()
	f := newFakeTelegram(t, http.StatusOK, `{"ok":true}`)
	base := f.URL
	f.Close() // nothing listens any more: the dial fails

	tg := newTestTelegram(t, base)
	err := tg.Notify(context.Background(), httpEvent("GET /v1/x"))
	if err == nil {
		t.Fatal("notify to a closed server succeeded")
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error carries the token: %v", err)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		t.Errorf("a *url.Error is reachable through the chain: %v", err)
	}

	var buf bytes.Buffer
	a := New(tg, Config{Log: slog.New(slog.NewJSONHandler(&buf, nil)), Sleep: func(context.Context, time.Duration) error { return nil }})
	a.Submit(httpEvent("GET /v1/x"))
	closeAlerter(t, a)
	if !strings.Contains(buf.String(), "alert: send failed") {
		t.Fatalf("the failure was not logged:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "SECRET") || strings.Contains(buf.String(), testToken) {
		t.Errorf("the log carries the token:\n%s", buf.String())
	}
}

func TestTelegramNeedsItsKeys(t *testing.T) {
	t.Parallel()
	if _, err := NewTelegram(TelegramConfig{Token: "x"}); err == nil {
		t.Error("a missing chat id was accepted")
	}
	if _, err := NewTelegram(TelegramConfig{ChatID: "x"}); err == nil {
		t.Error("a missing token was accepted")
	}
}

func TestLogNotifierWritesOneInfoLine(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	n := NewLog(slog.New(slog.NewJSONHandler(&buf, nil)))
	if err := n.Notify(context.Background(), httpEvent("GET /v1/x")); err != nil {
		t.Fatalf("notify: %v", err)
	}
	out := strings.TrimSpace(buf.String())
	if strings.Count(out, "\n") != 0 || !strings.Contains(out, `"level":"INFO"`) || !strings.Contains(out, `"msg":"alert"`) ||
		!strings.Contains(out, "route: GET /v1/x") {
		t.Errorf("log line = %s", out)
	}
}
