package gitfixture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Hygiene scans every .go file under root and fails t on each raw git spawn: a call to exec.Command("git", ...) or
// exec.CommandContext(ctx, "git", ...) whose arguments are not routed through gitfixture.Args. Such a call is the
// source of the teardown flake (see the package comment); the scan kills the class instead of waiting for the next
// instance.
//
// allow lists the places a raw spawn is meant: slash-separated paths relative to root, each a file or a directory (a
// directory allows every file under it). Put the unit's own helpers there, never a test that merely forgot Args.
//
// Only a literal "git" command is seen: a spawn through a variable or a wrapper is not. Directories named testdata,
// vendor and node_modules and any directory starting with a dot are skipped, and a root with no .go file at all fails
// too, so a moved tree cannot turn the gate vacuous.
func Hygiene(t testing.TB, root string, allow ...string) {
	t.Helper()
	res, err := scan(root, allow)
	if err != nil {
		t.Fatalf("gitfixture.Hygiene: %v", err)
		return
	}
	if res.files == 0 {
		t.Errorf("gitfixture.Hygiene: no .go file under %s: the scan would pass vacuously", root)
		return
	}
	for _, v := range res.violations {
		t.Errorf("gitfixture.Hygiene: %s", v)
	}
}

type scanResult struct {
	files      int
	violations []string
}

// scan walks root, in lexical order, and collects one violation line per raw git spawn outside allow.
func scan(root string, allow []string) (scanResult, error) {
	var res scanResult
	allowed := cleanAllow(allow)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return fmt.Errorf("relative path of %s: %w", p, err)
		}
		rel = filepath.ToSlash(rel)
		res.files++
		if isAllowed(rel, allowed) {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		lines, err := rawSpawns(rel, src)
		if err != nil {
			res.violations = append(res.violations, err.Error())
			return nil
		}
		for _, line := range lines {
			res.violations = append(res.violations, fmt.Sprintf(
				"%s:%d: raw git spawn: route its arguments through gitfixture.Args, or allow the file", rel, line))
		}
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("walk %s: %w", root, err)
	}
	return res, nil
}

func skipDir(name string) bool {
	switch name {
	case "testdata", "vendor", "node_modules":
		return true
	}
	return strings.HasPrefix(name, ".")
}

func cleanAllow(allow []string) []string {
	out := make([]string, 0, len(allow))
	for _, a := range allow {
		a = path.Clean(filepath.ToSlash(a))
		if a != "." {
			out = append(out, a)
		}
	}
	return out
}

func isAllowed(rel string, allowed []string) bool {
	for _, a := range allowed {
		if rel == a || strings.HasPrefix(rel, a+"/") {
			return true
		}
	}
	return false
}

// rawSpawns returns the line of each exec spawn of git in one file that is not routed through Args.
func rawSpawns(name string, src []byte) ([]int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("%s: cannot parse: %w", name, err)
	}
	execName := importName(file, func(p string) bool { return p == "os/exec" }, "exec")
	fixtureName := importName(file, func(p string) bool { return p == "gitfixture" || strings.HasSuffix(p, "/gitfixture") }, "gitfixture")
	var lines []int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		cmdIdx, ok := execCommandIndex(call, execName)
		if !ok || !isGitLiteral(call, cmdIdx) {
			return true
		}
		if !routedThroughArgs(call.Args[cmdIdx+1:], fixtureName) {
			lines = append(lines, fset.Position(call.Pos()).Line)
		}
		return true
	})
	return lines, nil
}

// importName is the identifier a file uses for the import whose path match accepts: its alias, else def. An absent,
// blank or dot import has no name, "" here, which matches no selector.
func importName(file *ast.File, match func(string) bool, def string) string {
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || !match(p) {
			continue
		}
		if imp.Name == nil {
			return def
		}
		if imp.Name.Name == "_" || imp.Name.Name == "." {
			return ""
		}
		return imp.Name.Name
	}
	return ""
}

// execCommandIndex returns the index of the command argument when call is exec.Command (0) or exec.CommandContext (1).
func execCommandIndex(call *ast.CallExpr, execName string) (int, bool) {
	if execName == "" {
		return 0, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !isIdent(sel.X, execName) {
		return 0, false
	}
	switch sel.Sel.Name {
	case "Command":
		return 0, true
	case "CommandContext":
		return 1, true
	}
	return 0, false
}

func isGitLiteral(call *ast.CallExpr, idx int) bool {
	if len(call.Args) <= idx {
		return false
	}
	lit, ok := call.Args[idx].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	v, err := strconv.Unquote(lit.Value)
	return err == nil && v == "git"
}

// routedThroughArgs reports whether any argument after the command is a call to <fixtureName>.Args(...).
func routedThroughArgs(args []ast.Expr, fixtureName string) bool {
	if fixtureName == "" {
		return false
	}
	for _, a := range args {
		call, ok := a.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "Args" && isIdent(sel.X, fixtureName) {
			return true
		}
	}
	return false
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}
