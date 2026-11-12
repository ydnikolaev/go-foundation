package alert

import (
	"strings"
	"testing"
)

func TestTextReplacesNonPrintableRunesWithSpaces(t *testing.T) {
	t.Parallel()
	baseline := strings.Count(Event{Kind: KindHTTP, Message: "a b", RequestID: "x y", Prefix: "p q: "}.Text(), "\n")
	tests := []struct {
		name string
		r    string
	}{
		{"line separator U+2028", "\u2028"},
		{"paragraph separator U+2029", "\u2029"},
		{"right-to-left override U+202E", "\u202e"},
		{"zero width joiner U+200D", "\u200d"},
		{"newline", "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ev := Event{Kind: KindHTTP, Message: "a" + tt.r + "b", RequestID: "x" + tt.r + "y", Prefix: "p" + tt.r + "q: "}
			text := ev.Text()
			for _, want := range []string{"what: a b\n", "request: x y", "p q: http"} {
				if !strings.Contains(text, want) {
					t.Errorf("text lacks %q:\n%q", want, text)
				}
			}
			if strings.ContainsAny(text, "\u2028\u2029\u202e\u200d") {
				t.Errorf("text still holds a separator or format rune: %q", text)
			}
			if got := strings.Count(text, "\n"); got != baseline {
				t.Errorf("text has %d line breaks, want %d:\n%q", got, baseline, text)
			}
		})
	}
}
