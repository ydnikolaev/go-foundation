package slogtest_test

import (
	"fmt"
	"log/slog"
	"testing"

	"github.com/ydnikolaev/go-foundation/units/slogtest"
)

// exampleTB stands in for the *testing.T a real test passes: an Example has none. It runs the cleanups by hand.
type exampleTB struct {
	testing.TB
	cleanups []func()
}

func (e *exampleTB) Helper()          {}
func (e *exampleTB) Cleanup(f func()) { e.cleanups = append(e.cleanups, f) }
func (e *exampleTB) Fatalf(format string, args ...any) {
	panic(fmt.Sprintf(format, args...))
}

func Example() {
	t := &exampleTB{}
	defer func() {
		for _, f := range t.cleanups {
			f()
		}
	}()
	buf := slogtest.CaptureJSONLog(t)

	slog.Info("saved", "id", 7)
	slog.Warn("slow")
	slog.Info("saved", "id", 8)

	lines := slogtest.JSONLines[any](t, buf)
	for _, l := range slogtest.WithMsg(lines, "saved") {
		fmt.Println(l["msg"], l["id"])
	}
	// Output:
	// saved 7
	// saved 8
}
