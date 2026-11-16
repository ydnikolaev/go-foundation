// Package tplfields lists the data fields a Go template reads, from its parse tree.
//
// Contract: Fields(src, funcs) parses src with text/template and funcs (html/template parses with the same parser,
// so one reader serves text and HTML sources) and returns the sorted, unique dotted key paths, from the root data,
// of every field the template reads, in any branch, condition or function argument:
//
//   - {{.a}} gives "a"; {{.a.b}} gives "a.b"; {{$.a}} gives "a"; {{(.a).b}} gives "a.b".
//   - {{with .a}} reads "a" and puts the dot at "a" inside; {{else}} and {{else with}} keep the outer dot.
//   - {{$x := .a}} reads "a" and binds $x to "a" until the end of the enclosing block; {{$x = .b}} rebinds it.
//     {{.}} or {{$x}} under a re-rooted dot or variable reads that path.
//
// Anything it cannot follow is refused as an *Error with the line and a Reason, never guessed: a parse error
// (ReasonParse); define, template, block and range; {{.}} at the root or $ alone; a field read through a dot or a
// variable that a function result rebound (ReasonUnsupported). It depends on the standard library only.
package tplfields

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"
)

// The reasons of an *Error.
const (
	ReasonParse       = "parse"
	ReasonUnsupported = "unsupported"
)

// Error is a template Fields refuses: Line is 1-based (0 when unknown), Reason is ReasonParse or ReasonUnsupported
// and Msg says what was refused.
type Error struct {
	Line   int
	Reason string
	Msg    string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s: %s", e.Line, e.Reason, e.Msg)
	}
	return e.Reason + ": " + e.Msg
}

// treeName is the name the source is parsed under; it appears in parse errors and node locations.
const treeName = "src"

var parseLine = regexp.MustCompile(`^template: ` + treeName + `:(\d+):\s*(.*)$`)

// Fields returns the sorted, unique fields src reads (see the package doc). funcs are the template funcs src may
// call besides the builtins; only their names matter to the parser, and every value must be a function.
func Fields(src string, funcs map[string]any) ([]string, error) {
	t, err := template.New(treeName).Funcs(funcs).Parse(src)
	if err != nil {
		e := &Error{Reason: ReasonParse, Msg: err.Error()}
		if m := parseLine.FindStringSubmatch(err.Error()); m != nil {
			e.Line, _ = strconv.Atoi(m[1])
			e.Msg = m[2]
		}
		return nil, e
	}
	for _, def := range t.Templates() {
		if def.Name() != treeName && def.Tree != nil {
			return nil, failAt(def.Tree, def.Tree.Root, fmt.Sprintf("define or block %q is not supported", def.Name()))
		}
	}
	if t.Tree == nil || t.Tree.Root == nil {
		return nil, nil
	}
	w := &walker{tree: t.Tree, fields: map[string]bool{}, scopes: []map[string]ref{{"$": rootRef}}}
	if err := w.list(t.Tree.Root, rootRef); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(w.fields))
	for f := range w.fields {
		out = append(out, f)
	}
	slices.Sort(out)
	return out, nil
}

// ref is what a dot or a variable stands for: a field path from the root ("" is the root itself), or, when known is
// false, a value no field path names (a function result or a constant).
type ref struct {
	path  string
	known bool
}

var (
	rootRef    = ref{known: true}
	unknownRef = ref{}
)

func (r ref) join(idents []string) string {
	if r.path == "" {
		return strings.Join(idents, ".")
	}
	return r.path + "." + strings.Join(idents, ".")
}

type walker struct {
	tree   *parse.Tree
	fields map[string]bool
	scopes []map[string]ref // variable scopes, innermost last
}

func failAt(tree *parse.Tree, n parse.Node, msg string) *Error {
	e := &Error{Reason: ReasonUnsupported, Msg: msg}
	loc, _ := tree.ErrorContext(n)
	// loc is "name:line:col".
	if parts := strings.Split(loc, ":"); len(parts) >= 3 {
		e.Line, _ = strconv.Atoi(parts[len(parts)-2])
	}
	return e
}

func (w *walker) fail(n parse.Node, msg string) error { return failAt(w.tree, n, msg) }

func (w *walker) push() { w.scopes = append(w.scopes, map[string]ref{}) }
func (w *walker) pop()  { w.scopes = w.scopes[:len(w.scopes)-1] }

func (w *walker) lookup(name string) (ref, bool) {
	for i := len(w.scopes) - 1; i >= 0; i-- {
		if r, ok := w.scopes[i][name]; ok {
			return r, true
		}
	}
	return ref{}, false
}

func (w *walker) assign(name string, r ref) {
	for i := len(w.scopes) - 1; i >= 0; i-- {
		if _, ok := w.scopes[i][name]; ok {
			w.scopes[i][name] = r
			return
		}
	}
	w.scopes[len(w.scopes)-1][name] = r
}

func (w *walker) record(path string) {
	if path != "" {
		w.fields[path] = true
	}
}

// block walks a list in a scope of its own: variables declared in it end with it.
func (w *walker) block(l *parse.ListNode, dot ref) error {
	if l == nil {
		return nil
	}
	w.push()
	defer w.pop()
	return w.list(l, dot)
}

func (w *walker) list(l *parse.ListNode, dot ref) error {
	for _, n := range l.Nodes {
		if err := w.node(n, dot); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) node(n parse.Node, dot ref) error {
	switch n := n.(type) {
	case *parse.TextNode, *parse.CommentNode:
		return nil
	case *parse.ActionNode:
		_, err := w.pipe(n.Pipe, dot, true)
		return err
	case *parse.IfNode:
		return w.branch(&n.BranchNode, dot, false)
	case *parse.WithNode:
		return w.branch(&n.BranchNode, dot, true)
	case *parse.RangeNode:
		return w.fail(n, "range is not supported")
	case *parse.TemplateNode:
		return w.fail(n, fmt.Sprintf("template %q is not supported", n.Name))
	default:
		return w.fail(n, fmt.Sprintf("%s is not supported", n))
	}
}

// branch walks an if or a with: the pipeline's variables live until its end; a with's list sees the dot at the
// pipeline's value, its else list the outer dot.
func (w *walker) branch(b *parse.BranchNode, dot ref, with bool) error {
	w.push()
	defer w.pop()
	val, err := w.pipe(b.Pipe, dot, true)
	if err != nil {
		return err
	}
	inner := dot
	if with {
		inner = val
	}
	if err := w.block(b.List, inner); err != nil {
		return err
	}
	return w.block(b.ElseList, dot)
}

// pipe walks a pipeline and returns its value: a field path when it is one operand, else unknownRef. record says
// whether a lone operand's own path counts as a read (a chain's base does not: (.a).b reads a.b).
func (w *walker) pipe(p *parse.PipeNode, dot ref, record bool) (ref, error) {
	if p == nil {
		return unknownRef, nil
	}
	val := unknownRef
	single := len(p.Cmds) == 1 && len(p.Cmds[0].Args) == 1
	for _, cmd := range p.Cmds {
		for _, arg := range cmd.Args {
			r, err := w.operand(arg, dot, record || !single)
			if err != nil {
				return unknownRef, err
			}
			if single {
				val = r
			}
		}
	}
	for _, v := range p.Decl {
		if p.IsAssign {
			w.assign(v.Ident[0], val)
		} else {
			w.scopes[len(w.scopes)-1][v.Ident[0]] = val
		}
	}
	return val, nil
}

func (w *walker) operand(n parse.Node, dot ref, record bool) (ref, error) {
	switch n := n.(type) {
	case *parse.FieldNode:
		if !dot.known {
			return unknownRef, w.fail(n, fmt.Sprintf("%s reads a field of a function result", n))
		}
		r := ref{path: dot.join(n.Ident), known: true}
		if record {
			w.record(r.path)
		}
		return r, nil
	case *parse.VariableNode:
		name, fields := n.Ident[0], n.Ident[1:]
		if name == "$" && len(fields) == 0 {
			return unknownRef, w.fail(n, "$ alone reads the whole data")
		}
		v, ok := w.lookup(name)
		if !ok {
			v = unknownRef
		}
		if len(fields) == 0 {
			if record && v.known {
				w.record(v.path)
			}
			return v, nil
		}
		if !v.known {
			return unknownRef, w.fail(n, fmt.Sprintf("%s reads a field of a function result", n))
		}
		r := ref{path: v.join(fields), known: true}
		if record {
			w.record(r.path)
		}
		return r, nil
	case *parse.DotNode:
		if dot.known && dot.path == "" {
			return unknownRef, w.fail(n, "{{.}} at the root reads the whole data")
		}
		if record && dot.known {
			w.record(dot.path)
		}
		return dot, nil
	case *parse.ChainNode:
		base, err := w.operand(n.Node, dot, false)
		if err != nil {
			return unknownRef, err
		}
		if !base.known {
			return unknownRef, w.fail(n, fmt.Sprintf("%s reads a field of a function result", n))
		}
		r := ref{path: base.join(n.Field), known: true}
		if record {
			w.record(r.path)
		}
		return r, nil
	case *parse.PipeNode:
		return w.pipe(n, dot, record)
	default:
		// A function name, a constant or nil: no field is read and no path is named.
		return unknownRef, nil
	}
}
