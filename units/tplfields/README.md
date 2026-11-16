# tplfields

Lists the data fields a Go template reads, from its parse tree. `Fields(src, funcs)` parses `src` with `text/template`
(`html/template` parses with the same parser, so one reader serves text and HTML sources) and returns the sorted, unique
dotted key paths, from the root data, of every field the template reads, in any branch, condition or function argument.
What it cannot follow is refused with the line, never guessed. Standard library only.

## Install

```sh
go get github.com/ydnikolaev/go-foundation/units/tplfields@v0.1.0
```

go-foundation is public: no GOPRIVATE, no token, no vendoring.

## Wire

1. Call `tplfields.Fields(src, funcs)` with the template source and the func map the template may call (only the names
   matter to the parser; every value must be a function; `nil` when it calls only builtins).
2. Use the paths to validate the data a caller must supply, or to show which fields a template needs.
3. On an error, `errors.As(err, &fe)` with `var fe *tplfields.Error` gives `Line` (1-based, 0 when unknown), `Reason`
   (`ReasonParse` or `ReasonUnsupported`) and `Msg`.

What it reads: `{{.a}}` gives `a`; `{{.a.b}}` and `{{$.a}}` give `a.b` and `a`; `{{(.a).b}}` gives `a.b`; `{{with .a}}`
reads `a` and puts the dot at `a` inside, while `{{else}}` keeps the outer dot; `{{$x := .a}}` reads `a` and binds `$x`
to it until the end of the enclosing block.

## Configure

Nothing to configure: no options, no environment. The func map is the one argument that varies.

## API

<!-- api: devkit foundation api; do not edit -->
```go
CONSTANTS
const (
	ReasonParse       = "parse"
	ReasonUnsupported = "unsupported"
)
FUNCTIONS
func Fields(src string, funcs map[string]any) ([]string, error)
TYPES
type Error struct {
	Line   int
	Reason string
	Msg    string
}
func (e *Error) Error() string
```

## Example

Read the fields of one template, then see a refusal (this is `Example`, compiled and run by the unit's tests):

```go
fields, err := tplfields.Fields(`Hi {{.user.name}}{{if .plan}}, plan {{with .plan}}{{.title}}{{end}}{{end}}`, nil)
fmt.Println(fields, err)

// What it cannot follow is refused with the line, never guessed.
_, err = tplfields.Fields("one\n{{range .items}}{{.}}{{end}}", nil)
var fe *tplfields.Error
if errors.As(err, &fe) {
	fmt.Println(fe.Line, fe.Reason)
}
// Output:
// [plan plan.title user.name] <nil>
// 2 unsupported
```

## Extension points

- `funcs` is the caller's own func map, so a template with custom functions parses.
- `*Error` carries `Line`, `Reason` and `Msg`, so a caller shows the author where the template broke.

## Do not

- Do not use it to render: it only reads the parse tree.
- Do not expect `range`, `define`, `template` or `block`: they are refused as `ReasonUnsupported`, not guessed.
- Do not read a field of a function result or of a variable a function result rebound: it is refused, because no field path names it.
- Do not copy this package. Extend it here; a copy carries `go-foundation-copy: tplfields@<sha>` and a removal row.
