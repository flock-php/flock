package share

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"

	"rsc.io/qr"
)

// quiet is the white border around a QR code, in modules, that scanners need.
const quiet = 4

// logoPNG is the app icon's tile (rendered by design/icongen), drawn in the middle of QRPNG's code.
//
//go:embed logo.png
var logoPNG []byte

// QRPNG renders text as a QR code, a PNG with scale pixels per module, base64-encoded for JSON.
// Flock's app icon covers its middle: the highest error correction level restores what it hides.
func QRPNG(text string, scale int) (string, error) {
	code, err := qr.Encode(text, qr.H)
	if err != nil {
		return "", err
	}
	side := (code.Size + 2*quiet) * scale
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			c := color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
			if code.Black(x/scale-quiet, y/scale-quiet) {
				c = color.RGBA{0, 0, 0, 0xFF}
			}
			img.SetRGBA(x, y, c)
		}
	}
	if err := drawLogo(img, code.Size, scale); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// drawLogo puts the logo in the middle of a code of size modules, on a white square a module wider
// on each side. It spans about a fifth of the code (some 7% of its area with the margin, well
// within the 30% that level H restores) and an odd number of modules, so it sits on the grid.
func drawLogo(img *image.RGBA, size, scale int) error {
	logo, err := png.Decode(bytes.NewReader(logoPNG))
	if err != nil {
		return err
	}
	modules := size/5 | 1
	offset := (quiet + (size-modules)/2) * scale
	for y := offset - scale; y < offset+(modules+1)*scale; y++ {
		for x := offset - scale; x < offset+(modules+1)*scale; x++ {
			img.SetRGBA(x, y, color.RGBA{0xFF, 0xFF, 0xFF, 0xFF})
		}
	}

	// Scaled down by averaging each pixel's area of the logo, then laid over the white.
	n, src := modules*scale, logo.Bounds()
	for y := range n {
		y0, y1 := src.Min.Y+y*src.Dy()/n, src.Min.Y+(y+1)*src.Dy()/n
		for x := range n {
			x0, x1 := src.Min.X+x*src.Dx()/n, src.Min.X+(x+1)*src.Dx()/n
			var r, g, b, a, count uint32
			for sy := y0; sy < max(y1, y0+1); sy++ {
				for sx := x0; sx < max(x1, x0+1); sx++ {
					pr, pg, pb, pa := logo.At(sx, sy).RGBA() // premultiplied, 16 bits
					r, g, b, a, count = r+pr, g+pg, b+pb, a+pa, count+1
				}
			}
			r, g, b, a = r/count, g/count, b/count, a/count
			white := 0xFFFF - a
			img.SetRGBA(offset+x, offset+y, color.RGBA{
				R: uint8((r + white) >> 8), G: uint8((g + white) >> 8), B: uint8((b + white) >> 8), A: 0xFF,
			})
		}
	}
	return nil
}

// QRText renders text as a QR code for a terminal: two modules per character with half blocks,
// dark on light, whatever the terminal's colours (scanners need the light border).
func QRText(text string) (string, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for y := -quiet; y < code.Size+quiet; y += 2 {
		// Black foreground on a white background; the half blocks draw the dark modules.
		b.WriteString("  \x1b[30;107m")
		for x := -quiet; x < code.Size+quiet; x++ {
			switch top, bottom := code.Black(x, y), code.Black(x, y+1); {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String(), nil
}
