package components

import (
	"strconv"
	"time"
)

// currentYear returns the current calendar year for the footer copyright.
func currentYear() string {
	return strconv.Itoa(time.Now().Year())
}

// ldScript wraps a JSON-LD payload in a <script> tag. It is rendered via
// templ.Raw because script element content is treated as raw text by the
// templ parser, so expressions inside a literal <script> are not evaluated.
func ldScript(json string) string {
	return `<script type="application/ld+json">` + json + `</script>`
}
