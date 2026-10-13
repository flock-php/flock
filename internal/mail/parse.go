package mail

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Detail is a fully parsed message, ready for a mail viewer.
type Detail struct {
	ID          string       `json:"id"`
	Inbox       string       `json:"inbox"`
	From        string       `json:"from"`
	To          []string     `json:"to"`
	Cc          []string     `json:"cc"`
	Subject     string       `json:"subject"`
	Date        time.Time    `json:"date"`
	HTML        string       `json:"html"` // cid: references are inlined as data URIs
	Text        string       `json:"text"`
	Headers     []Header     `json:"headers"`
	Attachments []Attachment `json:"attachments"`
}

// Header is one message header, decoded.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Attachment describes a downloadable part.
type Attachment struct {
	Index       int    `json:"index"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
}

// part is a decoded leaf of the MIME tree.
type part struct {
	contentType string
	params      map[string]string
	disposition string
	filename    string
	contentID   string
	body        []byte
}

// Find locates a message by ID across all inboxes.
func Find(dir, id string) (Message, error) {
	if strings.ContainsAny(id, `/\`) {
		return Message{}, fmt.Errorf("invalid message id %q", id)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*", id+".eml"))
	if len(matches) == 0 {
		return Message{}, fmt.Errorf("no message %s", id)
	}
	return read(matches[0])
}

// Parse reads a stored message and decodes its bodies, headers and attachments.
func Parse(msg Message) (Detail, error) {
	detail := Detail{ID: msg.ID, Inbox: msg.Inbox, From: msg.From, To: msg.To, Subject: msg.Subject, Date: msg.Date}

	parsed, parts, err := load(msg.Path)
	if err != nil {
		return detail, err
	}

	for name, values := range parsed.Header {
		for _, v := range values {
			detail.Headers = append(detail.Headers, Header{Name: name, Value: decodeHeader(v)})
		}
	}
	if cc, err := parsed.Header.AddressList("Cc"); err == nil {
		for _, a := range cc {
			detail.Cc = append(detail.Cc, formatAddress(a))
		}
	}

	inline := map[string]string{}
	for _, p := range parts {
		switch {
		case isAttachment(p):
			continue
		case p.contentType == "text/plain" && detail.Text == "":
			detail.Text = decodeCharset(p)
		case p.contentType == "text/html" && detail.HTML == "":
			detail.HTML = decodeCharset(p)
		case p.contentID != "":
			inline[p.contentID] = "data:" + p.contentType + ";base64," + base64.StdEncoding.EncodeToString(p.body)
		}
	}
	for cid, uri := range inline {
		detail.HTML = strings.ReplaceAll(detail.HTML, "cid:"+cid, uri)
	}

	for i, p := range attachments(parts) {
		detail.Attachments = append(detail.Attachments, Attachment{Index: i, Filename: p.filename, ContentType: p.contentType, Size: len(p.body)})
	}
	if detail.Headers == nil {
		detail.Headers = []Header{}
	}
	if detail.Attachments == nil {
		detail.Attachments = []Attachment{}
	}

	return detail, nil
}

// AttachmentData returns the metadata and decoded bytes of attachment index.
func AttachmentData(msg Message, index int) (Attachment, []byte, error) {
	_, parts, err := load(msg.Path)
	if err != nil {
		return Attachment{}, nil, err
	}
	list := attachments(parts)
	if index < 0 || index >= len(list) {
		return Attachment{}, nil, fmt.Errorf("no attachment %d", index)
	}
	p := list[index]
	return Attachment{Index: index, Filename: p.filename, ContentType: p.contentType, Size: len(p.body)}, p.body, nil
}

func load(path string) (*mail.Message, []part, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}

	body, err := io.ReadAll(parsed.Body)
	if err != nil {
		return nil, nil, err
	}

	var parts []part
	walk(headerGetter(parsed.Header.Get), body, &parts, 0)
	return parsed, parts, nil
}

type headerGetter func(string) string

// walk flattens the MIME tree into leaves, decoding transfer encodings.
func walk(get headerGetter, body []byte, out *[]part, depth int) {
	contentType := get("Content-Type")
	if contentType == "" {
		contentType = "text/plain; charset=us-ascii"
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType, params = "application/octet-stream", map[string]string{}
	}

	if strings.HasPrefix(mediaType, "multipart/") && depth < 20 {
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			p, err := reader.NextRawPart()
			if err != nil {
				return
			}
			data, err := io.ReadAll(p)
			if err != nil {
				return
			}
			walk(p.Header.Get, data, out, depth+1)
		}
	}

	decoded := decodeTransfer(get("Content-Transfer-Encoding"), body)
	disposition, dispParams, _ := mime.ParseMediaType(get("Content-Disposition"))

	filename := decodeHeader(dispParams["filename"])
	if filename == "" {
		filename = decodeHeader(params["name"])
	}

	*out = append(*out, part{
		contentType: mediaType,
		params:      params,
		disposition: disposition,
		filename:    filename,
		contentID:   strings.Trim(get("Content-Id"), "<> "),
		body:        decoded,
	})
}

func decodeTransfer(encoding string, body []byte) []byte {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		cleaned := bytes.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, body)
		if decoded, err := base64.StdEncoding.DecodeString(string(cleaned)); err == nil {
			return decoded
		}
	case "quoted-printable":
		if decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body))); err == nil {
			return decoded
		}
	}
	return body
}

func decodeCharset(p part) string {
	charset := strings.ToLower(p.params["charset"])
	if charset == "" || charset == "utf-8" || charset == "us-ascii" || charset == "ascii" {
		return string(p.body)
	}
	if charset == "iso-8859-1" {
		charset = "latin1"
	}
	reader, err := charsetReader(charset, bytes.NewReader(p.body))
	if err != nil {
		return string(p.body)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return string(p.body)
	}
	return string(data)
}

// isAttachment: explicitly attached, or a named non-text part that is not an inline (cid) image.
func isAttachment(p part) bool {
	if p.disposition == "attachment" {
		return true
	}
	if p.contentID != "" {
		return false
	}
	return p.filename != "" || !strings.HasPrefix(p.contentType, "text/")
}

func attachments(parts []part) []part {
	var out []part
	for _, p := range parts {
		if isAttachment(p) {
			if p.filename == "" {
				p.filename = "attachment"
			}
			out = append(out, p)
		}
	}
	return out
}
