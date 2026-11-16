package telegramfake

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

const okReply = `{"ok":true,"result":{"id":"prep-9"}}`

// post sends one POST with body to the fake's path and returns the status and the answer. It reports a failure with
// Errorf, not Fatalf, so a goroutine may call it; the status is then 0.
func post(t *testing.T, base, path, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(base+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Errorf("POST %s: %v", path, err)
		return 0, ""
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Errorf("read answer: %v", err)
	}
	return resp.StatusCode, string(b)
}

func TestServerRecordsAndAnswers(t *testing.T) {
	t.Parallel()
	refused := `{"ok":false,"error_code":400,"description":"Bad Request"}`
	tests := []struct {
		name  string
		reply string
		sends []Call
	}{
		{"no call", okReply, nil},
		{"one call", okReply, []Call{{Path: "/bot1:x/sendMessage", Body: []byte(`{"chat_id":7}`)}}},
		{"two calls in order", okReply, []Call{
			{Path: "/bot1:x/getMe", Body: []byte(`{}`)},
			{Path: "/beta/bot1:x/savePreparedInlineMessage", Body: []byte(`{"user_id":4242,"result":{"id":"duo"}}`)},
		}},
		{"a refusal is answered as given", refused, []Call{{Path: "/bot1:x/sendMessage", Body: []byte(`{"a":[1,2]}`)}}},
		{"a body that is not JSON is kept as sent", okReply, []Call{{Path: "/bot1:x/sendMessage", Body: []byte(`not json`)}}},
		{"an empty body", okReply, []Call{{Path: "/bot1:x/getMe", Body: nil}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := New(tt.reply)
			t.Cleanup(s.Close)

			for _, c := range tt.sends {
				code, answer := post(t, s.URL, c.Path, string(c.Body))
				if code != http.StatusOK || answer != tt.reply {
					t.Errorf("POST %s = %d %q, want 200 %q", c.Path, code, answer, tt.reply)
				}
			}

			got := s.Calls()
			if len(got) != len(tt.sends) {
				t.Fatalf("calls = %v, want %d", got, len(tt.sends))
			}
			for i, want := range tt.sends {
				if got[i].Path != want.Path || string(got[i].Body) != string(want.Body) {
					t.Errorf("call %d = %s %q, want %s %q", i, got[i].Path, got[i].Body, want.Path, want.Body)
				}
			}
		})
	}
}

// TestCallsIsACopy pins that a caller cannot reach the server's own record through the slice it was given.
func TestCallsIsACopy(t *testing.T) {
	t.Parallel()
	s := New(okReply)
	t.Cleanup(s.Close)
	post(t, s.URL, "/bot/a", `{}`)

	first := s.Calls()
	first[0].Path = "/changed"
	post(t, s.URL, "/bot/b", `{}`)

	got := s.Calls()
	if len(got) != 2 || got[0].Path != "/bot/a" || got[1].Path != "/bot/b" {
		t.Errorf("calls = %v, want /bot/a then /bot/b", got)
	}
}

// TestServerConcurrent pins that calls arriving together are all kept (run under -race).
func TestServerConcurrent(t *testing.T) {
	t.Parallel()
	const n = 16
	s := New(okReply)
	t.Cleanup(s.Close)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			post(t, s.URL, "/bot/x", `{}`)
		}()
	}
	wg.Wait()

	if got := len(s.Calls()); got != n {
		t.Errorf("calls = %d, want %d", got, n)
	}
}
