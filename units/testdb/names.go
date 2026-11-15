package testdb

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	templatePrefix = "tmpl_"
	testPrefix     = "t_"
	// maxName is Postgres's limit on an identifier, in bytes.
	maxName = 63
	masked  = "xxxxx"
)

// hash is the hex sha256 of parts joined with a NUL, so ("a", "bc") and ("ab", "c") differ.
func hash(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// namespace is 8 hex of the module root found from dir. Two checkouts of one repo sit at two roots and get two
// namespaces, so they never touch each other's databases. It is computed in process: an environment variable would
// differ per worktree and put the worktree into the test cache key.
func namespace(dir string) string { return hash(moduleRoot(dir))[:8] }

// moduleRoot is the nearest directory at or above dir that holds a go.mod; dir itself when there is none.
func moduleRoot(dir string) string {
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

// digest is 12 hex over the names and bytes of every file in migrations, in lexical order. A nil FS has the digest of
// an empty one. The digest is part of the template's name, so changing a migration builds a new template.
func digest(migrations fs.FS) (string, error) {
	h := sha256.New()
	if migrations != nil {
		err := fs.WalkDir(migrations, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(migrations, p)
			if err != nil {
				return fmt.Errorf("read %s: %w", p, err)
			}
			h.Write([]byte(p))
			h.Write([]byte{0})
			h.Write([]byte(strconv.Itoa(len(b))))
			h.Write([]byte{0})
			h.Write(b)
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("walk migrations: %w", err)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// templateFamily is the part of a template's name shared by every migration digest of one package; pkg is the
// package's directory, the working directory of its tests.
func templateFamily(pkg string) string { return templatePrefix + hash(pkg)[:8] + "_" }

func templateName(pkg, digest12 string) string { return templateFamily(pkg) + digest12 }

// testName is the database of one New call: t_<ns>_<16 hex of the package and the test>. The nth call (n > 0) of one
// test in one process gets its own name, so the second never drops the first's database.
func testName(ns, pkg, test string, n int) string {
	parts := []string{pkg, test}
	if n > 0 {
		parts = append(parts, strconv.Itoa(n))
	}
	return testPrefix + ns + "_" + hash(parts...)[:16]
}

var (
	urlPassword = regexp.MustCompile(`(?i)(\bpassword\s*=\s*)('(?:\\.|[^'])*'|\S+)`)
	dbnameParam = regexp.MustCompile(`(?i)(^|\s)dbname\s*=\s*('(?:\\.|[^'])*'|\S+)`)
)

func isURL(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}

// mask returns dsn with its password replaced, for an error message. It reads the URL form and the key=value form.
func mask(dsn string) string {
	if !isURL(dsn) {
		return urlPassword.ReplaceAllString(dsn, "${1}"+masked)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		// url.Parse's own error quotes the input; say nothing of it.
		return "<unparsable DSN>"
	}
	if _, has := u.User.Password(); has {
		u.User = url.UserPassword(u.User.Username(), masked)
	}
	if q := u.Query(); q.Has("password") {
		q.Set("password", masked)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// password is the secret in dsn, "" when it has none; redact uses it to scrub a driver's error text.
func password(dsn string) string {
	if !isURL(dsn) {
		if m := urlPassword.FindStringSubmatch(dsn); m != nil {
			return strings.Trim(m[2], "'")
		}
		return ""
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	if pw, ok := u.User.Password(); ok {
		return pw
	}
	return u.Query().Get("password")
}

// redact removes dsn's password from text.
func redact(text, dsn string) string {
	if pw := password(dsn); pw != "" {
		text = strings.ReplaceAll(text, pw, masked)
	}
	return text
}

// withDatabase returns dsn pointing at database name instead of its own. It reads the URL form and the key=value form.
func withDatabase(dsn, name string) (string, error) {
	if !isURL(dsn) {
		return strings.TrimSpace(dbnameParam.ReplaceAllString(dsn, "") + " dbname=" + name), nil
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "", errUnparsable
	}
	u.Path = "/" + name
	u.RawPath = ""
	return u.String(), nil
}
