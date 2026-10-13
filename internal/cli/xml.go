package cli

import (
	"encoding/xml"
	"io"
)

func xmlEscapeText(w io.Writer, s string) error {
	return xml.EscapeText(w, []byte(s))
}
