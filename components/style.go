package components

// styleCSS is the site stylesheet, inlined into <head> so first paint needs
// no separate render-blocking CSS request. Installed once at startup from
// the embedded filesystem (see main.go).
var styleCSS string

// SetStyleCSS installs the embedded stylesheet for inlining.
func SetStyleCSS(css string) { styleCSS = css }

// styleHead returns the stylesheet as a <style> element when inlining is
// enabled, or the external <link> fallback otherwise. It is emitted via
// templ.Raw because templ treats content inside <style>/<script> as
// plaintext and would not process an expression there.
func styleHead() string {
	if styleCSS == "" {
		return `<link rel="stylesheet" href="/static/css/style.css"/>`
	}
	return "<style>" + styleCSS + "</style>"
}
