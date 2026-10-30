package hub

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

// QR is text as a QR code, one string a row, '1' for a dark module and '0'
// for a light one, without the quiet zone around it: whoever draws it adds
// that. It is AgentBox's internal/lan.QR, the shape hubapi.HubPairing.QR has.
func QR(text string) ([]string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	rows := make([]string, code.Size)
	var b strings.Builder
	for y := range code.Size {
		b.Reset()
		for x := range code.Size {
			if code.Black(x, y) {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
		rows[y] = b.String()
	}
	return rows, nil
}

// TerminalQR draws a QR code from QR for a terminal, two rows to a line with
// half blocks, with the quiet zone QR leaves out. The light modules are the
// ones drawn, in the terminal's foreground colour, so the code is right on a
// dark terminal; on a light one it comes out inverted, which phones' cameras
// read as well.
func TerminalQR(rows []string) string {
	const quiet = 2
	n := len(rows)
	dark := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return y >= 0 && y < n && x >= 0 && x < n && rows[y][x] == '1'
	}
	var b strings.Builder
	for y := 0; y < n+2*quiet; y += 2 {
		for x := range n + 2*quiet {
			top, bottom := dark(x, y), dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteString(" ")
			case top:
				b.WriteString("▄")
			case bottom:
				b.WriteString("▀")
			default:
				b.WriteString("█")
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// svgQR draws a QR code from QR as an SVG, dark on white with its quiet zone,
// for the hub's pages.
func svgQR(rows []string) string {
	const quiet = 4
	n := len(rows) + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n, n, n)
	for y, row := range rows {
		for x, c := range row {
			if c == '1' {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}
