package mail

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Message is the summary of a caught email.
type Message struct {
	ID      string    `json:"id"`
	Inbox   string    `json:"inbox"`
	From    string    `json:"from"`
	To      []string  `json:"to"`
	Subject string    `json:"subject"`
	Date    time.Time `json:"date"`
	Size    int64     `json:"size"`
	Path    string    `json:"path"`
}

// List returns every caught message across all inboxes, newest first.
func List(dir string) ([]Message, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*", "*.eml"))
	if err != nil {
		return nil, err
	}

	messages := make([]Message, 0, len(paths))
	for _, path := range paths {
		msg, err := read(path)
		if err != nil {
			continue
		}
		messages = append(messages, msg)
	}

	sort.Slice(messages, func(i, j int) bool { return messages[i].ID > messages[j].ID })

	return messages, nil
}

// Clear deletes all messages in inbox, or in every inbox when inbox is empty.
func Clear(dir, inbox string) error {
	if inbox == "" {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() {
				if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
					return err
				}
			}
		}
		return nil
	}

	return os.RemoveAll(filepath.Join(dir, InboxName(inbox)))
}

func read(path string) (Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return Message{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return Message{}, err
	}

	msg := Message{
		ID:    strings.TrimSuffix(filepath.Base(path), ".eml"),
		Inbox: filepath.Base(filepath.Dir(path)),
		Size:  info.Size(),
		Path:  path,
		Date:  info.ModTime(),
	}

	parsed, err := mail.ReadMessage(f)
	if err != nil {
		return msg, nil // keep unparseable mail visible rather than hiding it
	}

	h := parsed.Header
	msg.Subject = decodeHeader(h.Get("Subject"))
	if date, err := h.Date(); err == nil {
		msg.Date = date
	}

	parser := mail.AddressParser{WordDecoder: wordDecoder}
	if from, err := parser.Parse(h.Get("From")); err == nil {
		msg.From = formatAddress(from)
	} else {
		msg.From = decodeHeader(h.Get("From"))
	}
	if to, err := parser.ParseList(h.Get("To")); err == nil {
		for _, a := range to {
			msg.To = append(msg.To, formatAddress(a))
		}
	}

	return msg, nil
}

func formatAddress(a *mail.Address) string {
	if a.Name == "" {
		return a.Address
	}
	return a.Name + " <" + a.Address + ">"
}

var wordDecoder = &mime.WordDecoder{CharsetReader: charsetReader}

// decodeHeader decodes RFC 2047 encoded words, falling back to the raw value.
func decodeHeader(value string) string {
	decoded, err := wordDecoder.DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

// charsetReader handles the charsets mime doesn't decode itself (it covers utf-8, us-ascii and iso-8859-1).
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "latin1", "windows-1252", "cp1252":
		data, err := io.ReadAll(input)
		if err != nil {
			return nil, err
		}
		runes := make([]rune, len(data))
		for i, b := range data {
			runes[i] = rune(b)
		}
		return bytes.NewReader([]byte(string(runes))), nil
	}
	return nil, fmt.Errorf("unsupported charset %q", charset)
}
