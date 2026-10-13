package cli

import (
	"bytes"
	"encoding/xml"
	"io"
)

func xmlEscapeText(w io.Writer, s string) error {
	return xml.EscapeText(w, []byte(s))
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xmlEscapeText(&b, s)
	return b.String()
}
