// Package qrsvg renders text as a QR code in an inline SVG.
package qrsvg

import (
	"fmt"
	"html"
	"strings"

	"rsc.io/qr"
)

// quiet is the light border around the code, in modules (ISO/IEC 18004).
const quiet = 4

// SVG returns a self-contained SVG image of the QR code for text. label is the
// accessible name. Dark modules are drawn in currentColor on a transparent
// background, so the code follows the page's colours and stays sharp on E-Ink.
func SVG(text, label string) (string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	size := code.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" role="img" aria-label="%s" shape-rendering="crispEdges">`,
		size, size, html.EscapeString(label))
	b.WriteString(`<path fill="currentColor" d="`)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}
