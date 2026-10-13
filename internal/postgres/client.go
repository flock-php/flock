package postgres

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Error is an ErrorResponse returned by the server.
type Error struct {
	Code    string // SQLSTATE, e.g. 42P04
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("postgres: %s (SQLSTATE %s)", e.Message, e.Code)
}

const sqlstateDuplicateDatabase = "42P04"

// CreateDatabase creates database name as user postgres; an existing database is not an error.
func CreateDatabase(ctx context.Context, port int, name string) error {
	err := Exec(ctx, port, "postgres", "CREATE DATABASE "+quoteIdent(name))

	var pgErr *Error
	if errors.As(err, &pgErr) && pgErr.Code == sqlstateDuplicateDatabase {
		return nil
	}

	return err
}

// Exec runs sql (simple query protocol, results discarded) against database on 127.0.0.1:port
// as user postgres. It only supports trust authentication, which is what InitDB configures.
func Exec(ctx context.Context, port int, database, sql string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}

	r := bufio.NewReader(conn)

	if _, err := conn.Write(startupMessage("postgres", database)); err != nil {
		return err
	}
	if err := readUntilReady(r); err != nil {
		return err
	}

	if _, err := conn.Write(message('Q', []byte(sql+"\x00"))); err != nil {
		return err
	}
	if err := readUntilReady(r); err != nil {
		return err
	}

	_, _ = conn.Write(message('X', nil))

	return nil
}

func startupMessage(user, database string) []byte {
	body := binary.BigEndian.AppendUint32(nil, 196608) // protocol 3.0
	for _, kv := range []string{"user", user, "database", database} {
		body = append(body, kv...)
		body = append(body, 0)
	}
	body = append(body, 0)

	return append(binary.BigEndian.AppendUint32(nil, uint32(len(body)+4)), body...)
}

func message(kind byte, body []byte) []byte {
	msg := append([]byte{kind}, binary.BigEndian.AppendUint32(nil, uint32(len(body)+4))...)
	return append(msg, body...)
}

// readUntilReady consumes messages until ReadyForQuery, returning the first ErrorResponse seen.
func readUntilReady(r *bufio.Reader) error {
	var firstErr error

	for {
		kind, err := r.ReadByte()
		if err != nil {
			return err
		}

		var size uint32
		if err := binary.Read(r, binary.BigEndian, &size); err != nil {
			return err
		}
		body := make([]byte, size-4)
		if _, err := io.ReadFull(r, body); err != nil {
			return err
		}

		switch kind {
		case 'R':
			if code := binary.BigEndian.Uint32(body); code != 0 {
				return fmt.Errorf("postgres: unsupported authentication method %d (only trust is supported)", code)
			}
		case 'E':
			pgErr := parseError(body)
			if firstErr == nil {
				firstErr = pgErr
			}
			// An error during startup ends the connection without ReadyForQuery.
			if pgErr.Code == "" || strings.HasPrefix(pgErr.Code, "28") || strings.HasPrefix(pgErr.Code, "3D") {
				return firstErr
			}
		case 'Z':
			return firstErr
		}
	}
}

func parseError(body []byte) *Error {
	e := &Error{}
	for _, field := range strings.Split(string(body), "\x00") {
		if field == "" {
			continue
		}
		switch field[0] {
		case 'C':
			e.Code = field[1:]
		case 'M':
			e.Message = field[1:]
		}
	}
	return e
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
