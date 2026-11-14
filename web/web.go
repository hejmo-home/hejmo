// Package web embeds the server-rendered templates and static assets.
package web

import "embed"

// Templates holds the HTML templates (html/template syntax).
//
//go:embed templates/*.html
var Templates embed.FS

// Static holds CSS, fonts and scripts served under /static/.
//
//go:embed static
var Static embed.FS
