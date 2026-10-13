package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const complexMessage = "From: Shop <shop@example.test>\r\n" +
	"To: you@example.com\r\n" +
	"Cc: \"Boss\" <boss@example.com>\r\n" +
	"Subject: =?UTF-8?B?T3JkZXIg4pyU?=\r\n" +
	"Date: Tue, 13 Oct 2026 10:00:00 +0200\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=outer\r\n" +
	"\r\n" +
	"--outer\r\n" +
	"Content-Type: multipart/related; boundary=rel\r\n" +
	"\r\n" +
	"--rel\r\n" +
	"Content-Type: multipart/alternative; boundary=alt\r\n" +
	"\r\n" +
	"--alt\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"Thanks for your order =E2=9C=94\r\n" +
	"--alt\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<p>Thanks</p><img src=\"cid:logo@shop\">\r\n" +
	"--alt--\r\n" +
	"--rel\r\n" +
	"Content-Type: image/png\r\n" +
	"Content-ID: <logo@shop>\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"iVBORw0KGgo=\r\n" +
	"--rel--\r\n" +
	"--outer\r\n" +
	"Content-Type: application/pdf; name=\"invoice.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"invoice.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"JVBERi0xLjQ=\r\n" +
	"--outer--\r\n"

func storeMessage(t *testing.T, raw string) (string, Message) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "default", "1-000001.eml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	msg, err := Find(dir, "1-000001")
	if err != nil {
		t.Fatal(err)
	}
	return dir, msg
}

func TestParseDecodesBodiesInlineImagesAndAttachments(t *testing.T) {
	_, msg := storeMessage(t, complexMessage)

	d, err := Parse(msg)
	if err != nil {
		t.Fatal(err)
	}

	if d.Subject != "Order ✔" || strings.TrimSpace(d.Text) != "Thanks for your order ✔" {
		t.Fatalf("subject/text: %q %q", d.Subject, d.Text)
	}
	if !strings.Contains(d.HTML, `src="data:image/png;base64,iVBORw0KGgo="`) {
		t.Fatalf("cid image not inlined: %s", d.HTML)
	}
	if len(d.Cc) != 1 || d.Cc[0] != "Boss <boss@example.com>" {
		t.Fatalf("cc: %v", d.Cc)
	}
	if len(d.Attachments) != 1 || d.Attachments[0].Filename != "invoice.pdf" || d.Attachments[0].ContentType != "application/pdf" {
		t.Fatalf("attachments: %+v", d.Attachments)
	}

	att, data, err := AttachmentData(msg, 0)
	if err != nil || att.Filename != "invoice.pdf" || string(data) != "%PDF-1.4" {
		t.Fatalf("attachment data: %+v %q %v", att, data, err)
	}
}

func TestParsePlainMessage(t *testing.T) {
	_, msg := storeMessage(t, "From: a@b.test\r\nSubject: hi\r\n\r\nplain body\r\n")

	d, err := Parse(msg)
	if err != nil || strings.TrimSpace(d.Text) != "plain body" || d.HTML != "" || len(d.Attachments) != 0 {
		t.Fatalf("got %+v %v", d, err)
	}
}

func TestFindRejectsPathTraversal(t *testing.T) {
	if _, err := Find(t.TempDir(), "../etc/passwd"); err == nil {
		t.Fatal("expected error")
	}
}
