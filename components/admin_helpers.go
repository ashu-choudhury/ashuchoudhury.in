package components

import (
	"strconv"
	"strings"

	"github.com/ashu-choudhury/portfolio/store"
)

// itoa converts an int to a string.
func itoa(n int) string {
	return strconv.Itoa(n)
}

// chartSVG renders a server-side SVG bar chart of daily page views.
// No client-side JavaScript is involved — the admin panel stays true to
// the zero-JS philosophy of the stack.
func chartSVG(daily []store.DailyStat) string {
	if len(daily) == 0 {
		return "<p class=\"form-note\">No data yet.</p>"
	}
	const (
		w    = 600
		h    = 180
		padL = 10
		padB = 24
		padT = 10
	)
	max := int64(1)
	for _, d := range daily {
		if d.Count > max {
			max = d.Count
		}
	}
	n := len(daily)
	slot := float64(w-padL) / float64(n)
	barW := slot * 0.6

	var b strings.Builder
	b.WriteString(`<svg viewBox="0 0 ` + strconv.Itoa(w) + ` ` + strconv.Itoa(h) + `" xmlns="http://www.w3.org/2000/svg" class="chart-svg" preserveAspectRatio="none">`)
	// Grid lines
	for i := 0; i <= 3; i++ {
		y := padT + float64(i)*(float64(h-padT-padB)/3)
		b.WriteString(`<line x1="0" y1="` + f(y) + `" x2="` + strconv.Itoa(w-padL) + `" y2="` + f(y) + `" class="chart-grid"/>`)
	}
	for i, d := range daily {
		x := padL + float64(i)*slot + (slot-barW)/2
		barH := float64(d.Count) / float64(max) * float64(h-padT-padB)
		y := float64(h-padB) - barH
		b.WriteString(`<rect x="` + f(x) + `" y="` + f(y) + `" width="` + f(barW) + `" height="` + f(barH) + `" rx="2" class="chart-bar"/>`)
		if i%2 == 0 || i == n-1 {
			b.WriteString(`<text x="` + f(x+barW/2) + `" y="` + strconv.Itoa(h-8) + `" class="chart-label" text-anchor="middle">` + d.Date[len(d.Date)-5:] + `</text>`)
		}
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// f formats a float for SVG output without trailing zeros.
func f(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}
