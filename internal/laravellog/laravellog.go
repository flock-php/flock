// Package laravellog reads a Laravel application's storage/logs as structured entries.
package laravellog

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Entry is one log record.
type Entry struct {
	Time    time.Time `json:"time"`
	Env     string    `json:"env"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Details string    `json:"details"`
}

// maxRead bounds how much of a (possibly huge) log file is parsed: only the tail matters.
const maxRead = 2 << 20

var header = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?)\] ([\w-]+)\.(\w+): ?(.*)$`)

// Files returns the site's .log files, most recently modified first.
func Files(sitePath string) []string {
	matches, _ := filepath.Glob(filepath.Join(sitePath, "storage", "logs", "*.log"))
	sort.Slice(matches, func(i, j int) bool { return modTime(matches[i]).After(modTime(matches[j])) })
	return matches
}

// Read parses the newest log file of a site and returns up to limit entries, newest first.
func Read(sitePath string, limit int) ([]Entry, error) {
	files := Files(sitePath)
	if len(files) == 0 {
		return []Entry{}, nil
	}
	data, err := tail(files[0], maxRead)
	if err != nil {
		return nil, err
	}

	entries := Parse(string(data))
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// Clear truncates every log file of a site.
func Clear(sitePath string) error {
	for _, f := range Files(sitePath) {
		if err := os.Truncate(f, 0); err != nil {
			return err
		}
	}
	return nil
}

// Parse splits log text into entries in file order. Lines before the first header
// (e.g. the tail of an entry cut off by the read limit) are dropped.
func Parse(text string) []Entry {
	entries := []Entry{}
	var current *Entry
	var details []string

	flush := func() {
		if current != nil {
			current.Details = strings.TrimRight(strings.Join(details, "\n"), "\n")
			entries = append(entries, *current)
		}
	}

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		m := header.FindStringSubmatch(line)
		if m == nil {
			if current != nil {
				details = append(details, line)
			}
			continue
		}

		flush()
		current = &Entry{Time: parseTime(m[1]), Env: m[2], Level: strings.ToLower(m[3]), Message: m[4]}
		details = nil

		// Exception context is a long JSON blob on the first line; keep the message readable.
		if i := strings.Index(m[4], ` {"`); i > 0 {
			current.Message = m[4][:i]
			details = append(details, m[4][i+1:])
		}
	}
	flush()

	return entries
}

func parseTime(s string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05.999999Z07:00", "2006-01-02 15:04:05.999999", "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

func tail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > n {
		if _, err := f.Seek(info.Size()-n, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(f)
}

func modTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
