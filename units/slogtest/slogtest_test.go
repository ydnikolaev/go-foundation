package slogtest_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"testing"

	"github.com/ydnikolaev/go-foundation/units/slogtest"
)

func emit() {
	slog.Info("first", "k", 1)
	slog.Warn("second")
	slog.Info("first", "k", 3)
}

func TestCaptureLog(t *testing.T) {
	buf := slogtest.CaptureLog(t)
	emit()
	if got, want := buf.String(), "level=INFO msg=first k=1\nlevel=WARN msg=second\nlevel=INFO msg=first k=3\n"; got != want {
		t.Errorf("log = %q, want %q", got, want)
	}
}

func TestCaptureJSONLogAndLines(t *testing.T) {
	tests := []struct {
		name  string
		emit  func()
		lines int
		msg   string
		want  int
	}{
		{"two of three", emit, 3, "first", 2},
		{"one of three", emit, 3, "second", 1},
		{"absent message", emit, 3, "absent", 0},
		{"nothing logged", func() {}, 0, "first", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := slogtest.CaptureJSONLog(t)
			tt.emit()
			lines := slogtest.JSONLines[any](t, buf)
			if len(lines) != tt.lines {
				t.Fatalf("lines = %d, want %d", len(lines), tt.lines)
			}
			if got := slogtest.WithMsg(lines, tt.msg); len(got) != tt.want {
				t.Errorf("WithMsg(%q) = %d lines, want %d", tt.msg, len(got), tt.want)
			}
		})
	}
}

func TestWithMsgKeepsFields(t *testing.T) {
	buf := slogtest.CaptureJSONLog(t)
	emit()
	got := slogtest.WithMsg(slogtest.JSONLines[any](t, buf), "first")
	if len(got) != 2 || got[0]["k"] != 1.0 || got[1]["k"] != 3.0 {
		t.Errorf("lines = %v", got)
	}
}

func TestWithMsgIgnoresNonStringMsg(t *testing.T) {
	lines := []slogtest.LogLine[any]{{"msg": 7.0}, {"other": "first"}, {"msg": "first"}}
	if got := slogtest.WithMsg(lines, "first"); len(got) != 1 {
		t.Errorf("lines = %v", got)
	}
}

func TestCaptureRestoresDefault(t *testing.T) {
	tests := []struct {
		name    string
		capture func(testing.TB) *bytes.Buffer
	}{
		{"text", slogtest.CaptureLog},
		{"json", slogtest.CaptureJSONLog},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := slog.Default()
			t.Run("inner", func(t *testing.T) { tt.capture(t) })
			if slog.Default() != before {
				t.Error("the default logger was not restored")
			}
		})
	}
}

// failTB records Fatalf instead of failing the real test.
type failTB struct {
	testing.TB
	msg string
}

func (f *failTB) Helper() {}

func (f *failTB) Fatalf(format string, args ...any) {
	f.msg = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func TestJSONLinesFailsOnNonJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "level=INFO msg=x\n", "is not JSON"},
		{"second line bad", "{\"msg\":\"a\"}\nnope\n", `"nope" is not JSON`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &failTB{TB: t}
			done := make(chan struct{})
			go func() {
				defer close(done)
				slogtest.JSONLines[any](f, bytes.NewBufferString(tt.in))
			}()
			<-done
			if !strings.Contains(f.msg, tt.want) {
				t.Errorf("Fatalf message = %q, want it to contain %q", f.msg, tt.want)
			}
		})
	}
}
