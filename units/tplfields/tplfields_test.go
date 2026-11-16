package tplfields

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// funcs stands in for a caller's func map: the parser only needs the names.
var funcs = map[string]any{
	"money": func(any, string) string { return "" },
	"date":  func(string) string { return "" },
	"mso":   func(string) string { return "" },
}

func TestFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want []string
	}{
		{"no actions", "plain text", nil},
		{"root field", "{{.a}}", []string{"a"}},
		{"nested field", "{{.plan.name}}", []string{"plan.name"}},
		{"root variable", "{{$.a}}", []string{"a"}},
		{"chain on a parenthesised field", "{{(.a).b}}", []string{"a.b"}},
		{"chain on a parenthesised variable", "{{$x := .a}}{{($x).b}}", []string{"a", "a.b"}},
		{"sorted and unique", "{{.b}} {{.a}} {{.b}}", []string{"a", "b"}},
		{"function arguments", "{{money .amount_minor .currency}}", []string{"amount_minor", "currency"}},
		{"condition", `{{if eq .step "x"}}y{{end}}`, []string{"step"}},
		{"index argument", `{{index .m "k"}}`, []string{"m"}},
		{"every branch", "{{if .a}}{{.b}}{{else if .c}}{{.d}}{{else}}{{.e}}{{end}}", []string{"a", "b", "c", "d", "e"}},
		{"pipeline into a function", "{{.a | printf \"%v\"}}", []string{"a"}},
		{"constant function call", `{{mso "<!--[if mso]>"}}`, nil},
		{"with re-roots the dot", "{{with .a}}{{.b}}{{end}}", []string{"a", "a.b"}},
		{"with dot alone", "{{with .a}}{{.}}{{end}}", []string{"a"}},
		{"with else keeps the outer dot", "{{with .a}}{{.b}}{{else}}{{.c}}{{end}}", []string{"a", "a.b", "c"}},
		{"else with", "{{with .a}}{{.x}}{{else with .b}}{{.y}}{{end}}", []string{"a", "a.x", "b", "b.y"}},
		{"nested with", "{{with .a}}{{with .b}}{{.c}}{{end}}{{end}}", []string{"a", "a.b", "a.b.c"}},
		{"root variable inside with", "{{with .a}}{{$.b}}{{end}}", []string{"a", "b"}},
		{"variable bound to a field", "{{$x := .a}}{{$x.b}}", []string{"a", "a.b"}},
		{"variable alone", "{{$x := .a}}{{$x}}", []string{"a"}},
		{"variable reassigned", "{{$x := .a}}{{$x = .b}}{{$x.c}}", []string{"a", "b", "b.c"}},
		{"variable declared in with", "{{with $x := .a}}{{$x.b}}{{.c}}{{end}}", []string{"a", "a.b", "a.c"}},
		{"variable from a function, read alone", `{{$x := money .a "RUB"}}{{$x}}`, []string{"a"}},
		{"variable scoped to its block", "{{if .a}}{{$x := .b}}{{$x.c}}{{end}}{{$x := .d}}{{$x.e}}", []string{"a", "b", "b.c", "d", "d.e"}},
		{"comment", "{{/* .hidden */}}{{.a}}", []string{"a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Fields(tc.src, funcs)
			if err != nil {
				t.Fatalf("Fields(%q): %v", tc.src, err)
			}
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Fields(%q) = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}

func TestFieldsRefuses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		src    string
		reason string
		line   int
	}{
		{"parse error", "line one\n{{.link", ReasonParse, 2},
		{"unknown function", "{{nope .a}}", ReasonParse, 1},
		{"define", "a\n{{define \"x\"}}y{{end}}", ReasonUnsupported, 2},
		{"template", "{{template \"x\"}}", ReasonUnsupported, 1},
		{"block", "{{block \"x\" .}}y{{end}}", ReasonUnsupported, 1},
		{"range over a field", "{{range .xs}}{{.title}}{{end}}", ReasonUnsupported, 1},
		{"range over a number", "{{range 5}}x{{end}}", ReasonUnsupported, 1},
		{"range with variables", "{{range $i, $e := .xs}}{{$e.title}}{{end}}", ReasonUnsupported, 1},
		{"dot at the root", "\n\n{{.}}", ReasonUnsupported, 3},
		{"dollar alone", "{{$}}", ReasonUnsupported, 1},
		{"dot bound to the root variable", "{{$x := .}}", ReasonUnsupported, 1},
		{"field under a function-rebound dot", `{{with money .a "RUB"}}{{.b}}{{end}}`, ReasonUnsupported, 1},
		{"field of a function-bound variable", `{{$x := index .m "k"}}{{$x.b}}`, ReasonUnsupported, 1},
		{"field of a function result", `{{(index .m "k").b}}`, ReasonUnsupported, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Fields(tc.src, funcs)
			var fe *Error
			if !errors.As(err, &fe) {
				t.Fatalf("Fields(%q) = %v, want an *Error", tc.src, err)
			}
			if fe.Reason != tc.reason || fe.Line != tc.line {
				t.Errorf("Fields(%q) = reason %q line %d (%s), want %q line %d", tc.src, fe.Reason, fe.Line, fe.Msg, tc.reason, tc.line)
			}
			if fe.Msg == "" || !strings.Contains(fe.Error(), fe.Msg) {
				t.Errorf("Error() = %q, want it to carry the message %q", fe.Error(), fe.Msg)
			}
		})
	}
}

// The builtins are known to the parser without the caller's map.
func TestFieldsNilFuncs(t *testing.T) {
	got, err := Fields(`{{if and .a (not .b)}}{{len .c}}{{end}}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
