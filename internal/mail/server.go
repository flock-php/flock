// Package mail is Flock's mail catcher: a receive-only SMTP server that stores
// every message as a raw .eml file instead of delivering it.
//
// The AUTH username picks the inbox, so apps sending with
// MAIL_USERNAME=${APP_NAME} get their own inbox. Any credentials are accepted.
package mail

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// MaxMessageBytes is the largest message the catcher accepts.
const MaxMessageBytes = 25 << 20

// DefaultInbox receives mail sent without (meaningful) credentials.
const DefaultInbox = "default"

// Server is the SMTP catcher.
type Server struct {
	addr string
	dir  string
	smtp *smtp.Server
}

// New creates a catcher listening on addr (e.g. "127.0.0.1:2525") that stores mail under dir.
func New(addr, dir string) *Server {
	return &Server{addr: addr, dir: dir}
}

// Start binds the listener and serves in the background.
func (s *Server) Start() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create mail dir: %w", err)
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("smtp listen: %w", err)
	}
	s.addr = ln.Addr().String()

	srv := smtp.NewServer(smtp.BackendFunc(func(*smtp.Conn) (smtp.Session, error) {
		return &session{dir: s.dir, inbox: DefaultInbox}, nil
	}))
	srv.Domain = "flock.local"
	srv.AllowInsecureAuth = true
	srv.MaxMessageBytes = MaxMessageBytes
	srv.MaxRecipients = 1000
	srv.ReadTimeout = time.Minute
	srv.WriteTimeout = time.Minute
	s.smtp = srv

	go func() { _ = srv.Serve(ln) }()

	return nil
}

// Addr is the bound address (resolved after Start).
func (s *Server) Addr() string {
	return s.addr
}

// Shutdown closes the listener and all open connections.
func (s *Server) Shutdown() error {
	if s.smtp == nil {
		return nil
	}
	return s.smtp.Close()
}

type session struct {
	dir   string
	inbox string
}

func (s *session) AuthMechanisms() []string {
	return []string{sasl.Plain, sasl.Login}
}

func (s *session) Auth(mech string) (sasl.Server, error) {
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(_, username, _ string) error {
			s.inbox = InboxName(username)
			return nil
		}), nil
	case sasl.Login:
		return &loginServer{done: func(username string) { s.inbox = InboxName(username) }}, nil
	}
	return nil, smtp.ErrAuthUnknownMechanism
}

func (s *session) Mail(string, *smtp.MailOptions) error { return nil }
func (s *session) Rcpt(string, *smtp.RcptOptions) error { return nil }
func (s *session) Reset()                               {}
func (s *session) Logout() error                        { return nil }

// Data writes the message to a temp file and renames it into place, so List never sees partial mail.
func (s *session) Data(r io.Reader) error {
	inboxDir := filepath.Join(s.dir, s.inbox)
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(inboxDir, ".incoming-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), filepath.Join(inboxDir, newID()+".eml"))
}

var seq atomic.Uint64

// newID is unique and sorts chronologically: zero-padded nanoseconds plus a per-process sequence.
func newID() string {
	return fmt.Sprintf("%019d-%06d", time.Now().UnixNano(), seq.Add(1)%1_000_000)
}

// InboxName turns an SMTP username into a safe directory name.
func InboxName(username string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(username)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}

	name := strings.Trim(b.String(), ".-")
	if len(name) > 64 {
		name = name[:64]
	}
	if name == "" || name == "null" {
		return DefaultInbox
	}
	return name
}

// loginServer implements the (obsolete but widely used) LOGIN SASL mechanism, which go-sasl only provides as a client.
type loginServer struct {
	step     int
	username string
	done     func(username string)
}

var errLoginState = errors.New("unexpected LOGIN step")

func (l *loginServer) Next(response []byte) ([]byte, bool, error) {
	switch l.step {
	case 0:
		if response == nil {
			l.step = 1
			return []byte("Username:"), false, nil
		}
		l.username, l.step = string(response), 2
		return []byte("Password:"), false, nil
	case 1:
		l.username, l.step = string(response), 2
		return []byte("Password:"), false, nil
	case 2:
		l.step = 3
		l.done(l.username)
		return nil, true, nil
	}
	return nil, false, errLoginState
}
