package main

import (
	"bytes"
	"html"
)

// Release builds set version with -ldflags "-X main.version=<tag>".
// Keep local, unversioned builds distinct from published releases.
var version = "dev"

func renderIndex() []byte {
	page, err := assets.ReadFile("static/index.html")
	if err != nil {
		panic(err)
	}
	// Only substitute the build version; leave Alpine.js expressions untouched.
	return bytes.Replace(page, []byte("{{VERSION}}"), []byte(html.EscapeString(version)), 1)
}
