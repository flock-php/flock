package mail

import (
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func startServer(t *testing.T) (*Server, string) {
	t.Helper()

	dir := t.TempDir()
	srv := New("127.0.0.1:0", dir)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown() })

	return srv, dir
}

func send(t *testing.T, srv *Server, auth smtp.Auth, subject string) {
	t.Helper()

	body := "From: App <app@example.com>\r\n" +
		"To: Jane <jane@example.com>, bob@example.com\r\n" +
		"Subject: " + subject + "\r\n" +
		"Date: Mon, 12 Oct 2026 10:00:00 +0200\r\n" +
		"\r\n" +
		".leading dot survives dot-stuffing\r\n"

	if err := smtp.SendMail(srv.Addr(), auth, "app@example.com", []string{"jane@example.com", "bob@example.com"}, []byte(body)); err != nil {
		t.Fatalf("send %q: %v", subject, err)
	}
}

func TestRoutesMailToInboxByAuthUsername(t *testing.T) {
	srv, dir := startServer(t)
	host, _, _ := net.SplitHostPort(srv.Addr())

	send(t, srv, nil, "no auth")
	send(t, srv, smtp.PlainAuth("", "Watchtower App", "secret", host), "plain auth")
	send(t, srv, smtp.PlainAuth("", "null", "", host), "null username")

	messages, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}

	inboxes := map[string]string{}
	for _, m := range messages {
		inboxes[m.Subject] = m.Inbox
	}
	want := map[string]string{"no auth": "default", "plain auth": "watchtower-app", "null username": "default"}
	for subject, inbox := range want {
		if inboxes[subject] != inbox {
			t.Fatalf("%q: want inbox %q, got %q (all: %v)", subject, inbox, inboxes[subject], inboxes)
		}
	}
}

func TestLoginMechanismRoutesToInbox(t *testing.T) {
	srv, dir := startServer(t)

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	c, err := smtp.NewClient(conn, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if ok, mechs := c.Extension("AUTH"); !ok || !strings.Contains(mechs, "LOGIN") || !strings.Contains(mechs, "PLAIN") {
		t.Fatalf("AUTH not advertised correctly: %q", mechs)
	}
	if err := c.Auth(loginAuth{"shop", "pw"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Mail("a@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt("b@example.com"); err != nil {
		t.Fatal(err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("Subject: login\r\n\r\nhi\r\n"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_ = c.Quit()

	messages, _ := List(dir)
	if len(messages) != 1 || messages[0].Inbox != "shop" {
		t.Fatalf("unexpected messages: %+v", messages)
	}
}

// loginAuth is a minimal LOGIN client, which net/smtp lacks.
type loginAuth struct{ username, password string }

func (a loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }

func (a loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	if string(challenge) == "Username:" {
		return []byte(a.username), nil
	}
	return []byte(a.password), nil
}

func TestListParsesHeadersNewestFirstAndKeepsRawBody(t *testing.T) {
	srv, dir := startServer(t)

	send(t, srv, nil, "first")
	send(t, srv, nil, "=?UTF-8?B?UMWZw61wcmF2YSDinJM=?=")
	send(t, srv, nil, "=?ISO-8859-1?Q?caf=E9?=")

	messages, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("want 3 messages, got %d", len(messages))
	}

	if got := []string{messages[0].Subject, messages[1].Subject, messages[2].Subject}; got[0] != "café" || got[1] != "Příprava ✓" || got[2] != "first" {
		t.Fatalf("unexpected order/decoding: %q", got)
	}

	m := messages[2]
	if m.From != "App <app@example.com>" || len(m.To) != 2 || m.To[0] != "Jane <jane@example.com>" || m.Date.Year() != 2026 || m.Size == 0 {
		t.Fatalf("unexpected headers: %+v", m)
	}

	raw, err := os.ReadFile(m.Path)
	if err != nil || !strings.Contains(string(raw), "\r\n.leading dot survives") {
		t.Fatalf("raw body not preserved: %q, %v", raw, err)
	}
	if filepath.Dir(m.Path) != filepath.Join(dir, "default") {
		t.Fatalf("unexpected path %s", m.Path)
	}
}

func TestClearSingleInboxAndAll(t *testing.T) {
	srv, dir := startServer(t)
	host, _, _ := net.SplitHostPort(srv.Addr())

	send(t, srv, nil, "a")
	send(t, srv, smtp.PlainAuth("", "shop", "x", host), "b")

	if err := Clear(dir, "shop"); err != nil {
		t.Fatal(err)
	}
	if messages, _ := List(dir); len(messages) != 1 || messages[0].Inbox != "default" {
		t.Fatalf("want only default left, got %+v", messages)
	}

	if err := Clear(dir, ""); err != nil {
		t.Fatal(err)
	}
	if messages, _ := List(dir); len(messages) != 0 {
		t.Fatalf("want empty, got %+v", messages)
	}
}

func TestInboxName(t *testing.T) {
	for in, want := range map[string]string{"": "default", "null": "default", " NULL ": "default", "../etc": "etc", "My App": "my-app"} {
		if got := InboxName(in); got != want {
			t.Fatalf("InboxName(%q) = %q, want %q", in, got, want)
		}
	}
}
