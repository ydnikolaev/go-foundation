// Package telegramfake is a fake Telegram Bot API server for tests: it keeps each call it receives (the URL path and the
// JSON body) and answers every call with one reply the caller gave. It imports nothing of this project and nothing
// but the standard library; the caller points its Bot API client at URL and reads Calls back.
package telegramfake

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

// Call is one request the fake received: the URL path (for example "/bot<token>/sendMessage") and the body as sent.
type Call struct {
	Path string
	Body json.RawMessage
}

// Server is a running fake Bot API. Its zero value is not usable; start one with New.
type Server struct {
	// URL is the base URL of the fake, with no trailing slash: the Bot API client's API root.
	URL string

	srv   *httptest.Server
	mu    sync.Mutex
	calls []Call
}

// New starts a fake that answers every call with reply (a Telegram JSON body, written as is). The caller stops it
// with Close.
func New(reply string) *Server {
	s := &Server{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A body that fails to read is kept as far as it was read; the caller's check of Calls shows it.
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.calls = append(s.calls, Call{Path: r.URL.Path, Body: b})
		s.mu.Unlock()
		_, _ = w.Write([]byte(reply))
	}))
	s.URL = s.srv.URL
	return s
}

// Calls returns a copy of the calls received so far, oldest first.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Close stops the server and waits for the calls in flight.
func (s *Server) Close() { s.srv.Close() }
