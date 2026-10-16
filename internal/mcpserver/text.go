package mcpserver

import (
	"html"
	"os"
	"regexp"
	"slices"
	"strings"
)

// truncate shortens s to at most n bytes (on a rune boundary), saying so.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n… (truncated)"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// tailFile returns the last n lines of a file, reading at most its final 1 MiB.
func tailFile(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	const window = 1 << 20
	offset := max(info.Size()-window, 0)
	buf := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return "", err
	}

	lines := strings.Split(strings.TrimRight(string(buf), "\r\n"), "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line is probably cut in half
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}

var (
	hrefRe      = regexp.MustCompile(`(?i)href\s*=\s*["']([^"']+)["']`)
	dropRe      = regexp.MustCompile(`(?is)<(script|style|head)\b.*?</(script|style|head)>`)
	breakRe     = regexp.MustCompile(`(?i)<(br|/p|/div|/tr|/h[1-6]|/li|/table)\b[^>]*>`)
	tagRe       = regexp.MustCompile(`<[^>]*>`)
	blankRe     = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankLineRe = regexp.MustCompile(`\n\s*\n\s*\n+`)
)

// links returns the distinct http(s) links of an HTML mail, in order.
func links(htmlBody string) []string {
	var out []string
	for _, m := range hrefRe.FindAllStringSubmatch(htmlBody, -1) {
		link := html.UnescapeString(strings.TrimSpace(m[1]))
		lower := strings.ToLower(link)
		if (strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")) && !slices.Contains(out, link) {
			out = append(out, link)
		}
	}
	return out
}

// htmlText turns an HTML mail into readable text, for mails without a text part.
func htmlText(htmlBody string) string {
	s := dropRe.ReplaceAllString(htmlBody, "")
	s = breakRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = blankRe.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	s = blankLineRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return strings.TrimSpace(s)
}
