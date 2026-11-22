package meavatar

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// absent is an error that says it is not found, as avatars.ErrNoPhoto and ErrBadHandle do.
type absent struct{}

func (absent) Error() string  { return "absent" }
func (absent) NotFound() bool { return true }

// photos is an AvatarSource over a few users: a handle "h<user>" for each user it knows, and the photo of each handle that
// has one. It keeps the handles Photo was asked for.
type photos struct {
	known  map[int64]bool
	images map[string][]byte
	err    error
	asked  []string
}

func (p *photos) Handle(botID string, userID int64) string {
	if botID != "main" || !p.known[userID] {
		return ""
	}
	return "h" + string(rune('0'+userID))
}

func (p *photos) Photo(_ context.Context, botID, handle string) ([]byte, string, error) {
	p.asked = append(p.asked, botID+"/"+handle)
	if p.err != nil {
		return nil, "", p.err
	}
	img, ok := p.images[handle]
	if !ok {
		return nil, "", absent{}
	}
	return img, "image/jpeg", nil
}

// as is the caller user of bot main; 0 is no caller.
func as(user int64) Caller {
	return func(context.Context) (string, int64, bool) { return "main", user, user != 0 }
}

func get(t *testing.T, src AvatarSource, caller Caller, url string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET "+Path, Handler(src, caller, nil))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody))
	return w
}

// TestMeAvatarOwnOnly: the route answers the caller's own photo whatever the request names, so another user's photo is
// unreachable; a caller with no photo, or none the source can name, is 404; no caller is 401 and no source 503.
func TestMeAvatarOwnOnly(t *testing.T) {
	t.Parallel()
	mine, theirs := []byte("\xff\xd8 mine"), []byte("\xff\xd8 theirs")
	newSrc := func() *photos {
		return &photos{known: map[int64]bool{1: true, 2: true, 3: true}, images: map[string][]byte{"h1": mine, "h2": theirs}}
	}
	tests := []struct {
		name      string
		caller    Caller
		url       string
		want      int
		wantBody  []byte
		wantAsked []string
	}{
		{"the caller's photo", as(1), Path, http.StatusOK, mine, []string{"main/h1"}},
		{"another user's id in the query", as(1), Path + "?user=2&id=2", http.StatusOK, mine, []string{"main/h1"}},
		{"another user's handle in the query", as(1), Path + "?handle=h2", http.StatusOK, mine, []string{"main/h1"}},
		{"a caller with no photo", as(3), Path + "?handle=h2", http.StatusNotFound, nil, []string{"main/h3"}},
		{"a caller the source can make no handle for", as(4), Path + "?handle=h2", http.StatusNotFound, nil, nil},
		{"no caller", as(0), Path + "?handle=h2", http.StatusUnauthorized, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := newSrc()

			w := get(t, src, tt.caller, tt.url)

			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d", w.Code, tt.want)
			}
			if string(tt.wantBody) != w.Body.String() {
				t.Errorf("body = %q, want %q", w.Body.String(), tt.wantBody)
			}
			if len(src.asked) != len(tt.wantAsked) || len(src.asked) == 1 && src.asked[0] != tt.wantAsked[0] {
				t.Errorf("Photo was asked for %v, want %v", src.asked, tt.wantAsked)
			}
		})
	}
	t.Run("a path below the route is not the route", func(t *testing.T) {
		t.Parallel()
		src := newSrc()
		if w := get(t, src, as(1), Path+"/h2"); w.Code != http.StatusNotFound || len(src.asked) != 0 {
			t.Errorf("status = %d with %v asked, want 404 and nothing asked", w.Code, src.asked)
		}
	})
	t.Run("no source", func(t *testing.T) {
		t.Parallel()
		if w := get(t, nil, as(1), Path); w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", w.Code)
		}
	})
	t.Run("a fetch that fails", func(t *testing.T) {
		t.Parallel()
		src := newSrc()
		src.err = errors.New("boom")
		if w := get(t, src, as(1), Path); w.Code != http.StatusBadGateway {
			t.Errorf("status = %d, want 502", w.Code)
		}
	})
}

// TestMeAvatarCache: a photo is kept a day by the caller's client and by no shared cache; an answer with no photo is kept by
// none, so a photo added later shows on the next open.
func TestMeAvatarCache(t *testing.T) {
	t.Parallel()
	src := &photos{known: map[int64]bool{1: true, 3: true}, images: map[string][]byte{"h1": []byte("\xff\xd8 mine")}}

	ok := get(t, src, as(1), Path)
	if ok.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", ok.Code)
	}
	for k, want := range map[string]string{
		"Cache-Control":          "private, max-age=86400",
		"Content-Type":           "image/jpeg",
		"Content-Length":         "7", // the bytes of "\xff\xd8 mine"
		"X-Content-Type-Options": "nosniff",
	} {
		if got := ok.Header().Get(k); got != want {
			t.Errorf("200 %s = %q, want %q", k, got, want)
		}
	}

	none := get(t, src, as(3), Path)
	if none.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", none.Code)
	}
	if got := none.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("404 Cache-Control = %q, want no-store", got)
	}
}

// brokenWriter is a ResponseWriter whose body write fails, to reach the write-failed log line.
type brokenWriter struct{ *httptest.ResponseRecorder }

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("pipe") }

// TestMeAvatarErrorClass: the log lines of a failed fetch and a failed write carry the class the injected classifier gives
// the error, and "other" when none is given. It swaps the default logger, so it runs alone.
func TestMeAvatarErrorClass(t *testing.T) {
	classify := func(err error) string { return "class-of-" + err.Error() }
	tests := []struct {
		name      string
		class     func(error) string
		fetchFail bool
		want      string
	}{
		{"a classifier on a failed fetch", classify, true, "class=class-of-boom"},
		{"a classifier on a failed write", classify, false, "class=class-of-pipe"},
		{"no classifier on a failed fetch", nil, true, "class=other"},
		{"no classifier on a failed write", nil, false, "class=other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(prev) })
			src := &photos{known: map[int64]bool{1: true}, images: map[string][]byte{"h1": []byte("\xff\xd8 mine")}}
			var w http.ResponseWriter = brokenWriter{httptest.NewRecorder()}
			if tt.fetchFail {
				src.err = errors.New("boom")
				w = httptest.NewRecorder()
			}

			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, Path, http.NoBody)

			Handler(src, as(1), tt.class).ServeHTTP(w, r)

			if got := logs.String(); !strings.Contains(got, tt.want) || !strings.Contains(got, "bot_id=main") {
				t.Errorf("log = %q, want it to hold %q and bot_id=main", got, tt.want)
			}
		})
	}
}
