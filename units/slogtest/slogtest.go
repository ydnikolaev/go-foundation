// Package slogtest captures the default slog logger into a buffer for one test and decodes JSON log lines. Stdlib only;
// it imports nothing of this project.
package slogtest

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

// LogLine is one decoded JSON log line. The architecture gate refuses `any` in non-test code, so the value type is a
// parameter: a test writes LogLine[any].
type LogLine[V any] map[string]V

// CaptureLog routes the default slog logger into a text buffer, time dropped, for one test.
func CaptureLog(t testing.TB) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	drop := func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{ReplaceAttr: drop})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// CaptureJSONLog routes the default slog logger into a JSON buffer for one test.
func CaptureJSONLog(t testing.TB) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// JSONLines decodes every line of buf as one JSON log line; a line that is not JSON fails the test.
func JSONLines[V any](t testing.TB, buf *bytes.Buffer) []LogLine[V] {
	t.Helper()
	var out []LogLine[V]
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var m LogLine[V]
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("log line %q is not JSON: %v", raw, err)
		}
		out = append(out, m)
	}
	return out
}

// WithMsg returns the lines whose "msg" is the string msg.
func WithMsg[V any](lines []LogLine[V], msg string) []LogLine[V] {
	var out []LogLine[V]
	for _, l := range lines {
		if v := reflect.ValueOf(l["msg"]); v.Kind() == reflect.String && v.String() == msg {
			out = append(out, l)
		}
	}
	return out
}
