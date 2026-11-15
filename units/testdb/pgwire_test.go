package testdb

// A minimal Postgres wire client, registered as the database/sql driver "testpg", so these tests reach a real server
// without a third-party driver: the unit is standard library only. It speaks the simple query protocol (no arguments,
// no transactions) and authenticates with trust, a cleartext or md5 password, or SCRAM-SHA-256. Rows come back as text.

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const testDriver = "testpg"

func init() { sql.Register(testDriver, pgDriver{}) }

type pgDriver struct{}

func (pgDriver) Open(dsn string) (driver.Conn, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, errors.New("testpg: want a postgres:// URL")
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "5432")
	}
	nc, err := net.Dial("tcp", host)
	if err != nil {
		return nil, fmt.Errorf("testpg: dial %s: %w", host, err)
	}
	pw, _ := u.User.Password()
	c := &pgConn{nc: nc, r: bufio.NewReader(nc), user: u.User.Username(), password: pw}
	if err := c.startup(strings.TrimPrefix(u.Path, "/")); err != nil {
		_ = nc.Close()
		return nil, err
	}
	return c, nil
}

type pgConn struct {
	nc       net.Conn
	r        *bufio.Reader
	user     string
	password string
}

// pgError is a server ErrorResponse.
type pgError struct{ code, msg string }

func (e *pgError) Error() string { return fmt.Sprintf("%s (SQLSTATE %s)", e.msg, e.code) }

func (c *pgConn) send(typ byte, payload []byte) error {
	buf := make([]byte, 0, 5+len(payload))
	if typ != 0 {
		buf = append(buf, typ)
	}
	buf = binary.BigEndian.AppendUint32(buf, uint32(4+len(payload)))
	buf = append(buf, payload...)
	if _, err := c.nc.Write(buf); err != nil {
		return fmt.Errorf("testpg: write: %w", err)
	}
	return nil
}

func (c *pgConn) recv() (byte, []byte, error) {
	typ, err := c.r.ReadByte()
	if err != nil {
		return 0, nil, fmt.Errorf("testpg: read: %w", err)
	}
	var n [4]byte
	if _, err := io.ReadFull(c.r, n[:]); err != nil {
		return 0, nil, fmt.Errorf("testpg: read: %w", err)
	}
	body := make([]byte, binary.BigEndian.Uint32(n[:])-4)
	if _, err = io.ReadFull(c.r, body); err != nil {
		return 0, nil, fmt.Errorf("testpg: read: %w", err)
	}
	return typ, body, nil
}

func (c *pgConn) startup(database string) error {
	p := binary.BigEndian.AppendUint32(nil, 196608)
	for _, kv := range [][2]string{{"user", c.user}, {"database", database}} {
		p = append(append(append(p, kv[0]...), 0), append([]byte(kv[1]), 0)...)
	}
	if err := c.send(0, append(p, 0)); err != nil {
		return err
	}
	for {
		typ, body, err := c.recv()
		if err != nil {
			return err
		}
		switch typ {
		case 'R':
			if err := c.auth(body); err != nil {
				return err
			}
		case 'E':
			return parseError(body)
		case 'Z':
			return nil
		}
	}
}

func (c *pgConn) auth(body []byte) error {
	switch code := binary.BigEndian.Uint32(body); code {
	case 0:
		return nil
	case 3:
		return c.send('p', append([]byte(c.password), 0))
	case 5:
		inner := md5.Sum([]byte(c.password + c.user))
		outer := md5.Sum(append([]byte(hex.EncodeToString(inner[:])), body[4:8]...))
		return c.send('p', append([]byte("md5"+hex.EncodeToString(outer[:])), 0))
	case 10:
		return c.scram()
	default:
		return fmt.Errorf("testpg: unsupported authentication method %d", code)
	}
}

func (c *pgConn) scram() error {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("testpg: nonce: %w", err)
	}
	nonce := base64.StdEncoding.EncodeToString(raw)
	firstBare := "n=" + c.user + ",r=" + nonce
	first := "n,," + firstBare
	initial := append([]byte("SCRAM-SHA-256\x00"), binary.BigEndian.AppendUint32(nil, uint32(len(first)))...)
	if err := c.send('p', append(initial, first...)); err != nil {
		return err
	}
	serverFirst, err := c.saslData(11)
	if err != nil {
		return err
	}
	attrs := parseAttrs(serverFirst)
	salt, err := base64.StdEncoding.DecodeString(attrs["s"])
	if err != nil {
		return fmt.Errorf("testpg: SCRAM salt: %w", err)
	}
	iter, err := strconv.Atoi(attrs["i"])
	if err != nil || !strings.HasPrefix(attrs["r"], nonce) {
		return errors.New("testpg: bad SCRAM server-first message")
	}
	salted, err := pbkdf2.Key(sha256.New, c.password, salt, iter, 32)
	if err != nil {
		return fmt.Errorf("testpg: SCRAM key: %w", err)
	}
	clientKey := mac(salted, "Client Key")
	stored := sha256.Sum256(clientKey)
	finalNoProof := "c=biws,r=" + attrs["r"]
	authMsg := firstBare + "," + serverFirst + "," + finalNoProof
	sig := mac(stored[:], authMsg)
	proof := make([]byte, len(clientKey))
	for i := range proof {
		proof[i] = clientKey[i] ^ sig[i]
	}
	if err := c.send('p', []byte(finalNoProof+",p="+base64.StdEncoding.EncodeToString(proof))); err != nil {
		return err
	}
	serverFinal, err := c.saslData(12)
	if err != nil {
		return err
	}
	want := "v=" + base64.StdEncoding.EncodeToString(mac(mac(salted, "Server Key"), authMsg))
	if serverFinal != want {
		return errors.New("testpg: SCRAM server signature mismatch")
	}
	return nil
}

// saslData reads one authentication message of the given code and returns its text.
func (c *pgConn) saslData(code uint32) (string, error) {
	typ, body, err := c.recv()
	if err != nil {
		return "", err
	}
	if typ == 'E' {
		return "", parseError(body)
	}
	if typ != 'R' || binary.BigEndian.Uint32(body) != code {
		return "", fmt.Errorf("testpg: unexpected message %q during SCRAM", typ)
	}
	return string(body[4:]), nil
}

func mac(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

func parseAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(part, "="); ok {
			out[k] = v
		}
	}
	return out
}

func parseError(body []byte) error {
	e := &pgError{}
	for len(body) > 1 {
		field := body[0]
		val, rest, _ := strings.Cut(string(body[1:]), "\x00")
		body = []byte(rest)
		switch field {
		case 'C':
			e.code = val
		case 'M':
			e.msg = val
		}
	}
	return e
}

// query runs one simple query and reads to ReadyForQuery. It returns the column names and the text rows.
func (c *pgConn) query(ctx context.Context, q string) ([]string, [][]driver.Value, error) {
	if dl, ok := ctx.Deadline(); ok {
		_ = c.nc.SetDeadline(dl)
	}
	if err := c.send('Q', append([]byte(q), 0)); err != nil {
		return nil, nil, driver.ErrBadConn
	}
	var (
		cols    []string
		rows    [][]driver.Value
		queryEr error
	)
	for {
		typ, body, err := c.recv()
		if err != nil {
			return nil, nil, driver.ErrBadConn
		}
		switch typ {
		case 'T':
			cols = parseColumns(body)
		case 'D':
			rows = append(rows, parseRow(body))
		case 'E':
			queryEr = parseError(body)
		case 'Z':
			return cols, rows, queryEr
		}
	}
}

func parseColumns(body []byte) []string {
	n := int(binary.BigEndian.Uint16(body))
	body = body[2:]
	cols := make([]string, 0, n)
	for range n {
		name, rest, _ := strings.Cut(string(body), "\x00")
		cols = append(cols, name)
		body = []byte(rest)[18:]
	}
	return cols
}

func parseRow(body []byte) []driver.Value {
	n := int(binary.BigEndian.Uint16(body))
	body = body[2:]
	row := make([]driver.Value, n)
	for i := range row {
		l := int32(binary.BigEndian.Uint32(body))
		body = body[4:]
		if l < 0 {
			continue
		}
		row[i] = string(body[:l])
		body = body[l:]
	}
	return row
}

func noArgs(args []driver.NamedValue) error {
	if len(args) > 0 {
		return errors.New("testpg: arguments are not supported")
	}
	return nil
}

func (c *pgConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if err := noArgs(args); err != nil {
		return nil, err
	}
	_, _, err := c.query(ctx, q)
	return driver.ResultNoRows, err
}

func (c *pgConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := noArgs(args); err != nil {
		return nil, err
	}
	cols, rows, err := c.query(ctx, q)
	if err != nil {
		return nil, err
	}
	return &pgRows{cols: cols, rows: rows}, nil
}

func (c *pgConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("testpg: no prepared statements")
}

func (c *pgConn) Begin() (driver.Tx, error) { return nil, errors.New("testpg: no transactions") }

func (c *pgConn) Close() error {
	_ = c.send('X', nil)
	if err := c.nc.Close(); err != nil {
		return fmt.Errorf("testpg: close: %w", err)
	}
	return nil
}

type pgRows struct {
	cols []string
	rows [][]driver.Value
}

func (r *pgRows) Columns() []string { return r.cols }

func (r *pgRows) Close() error { return nil }

func (r *pgRows) Next(dest []driver.Value) error {
	if len(r.rows) == 0 {
		return io.EOF
	}
	copy(dest, r.rows[0])
	r.rows = r.rows[1:]
	return nil
}
