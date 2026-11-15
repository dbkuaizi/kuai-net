package main

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestWebFooterVersion(t *testing.T) {
	old := version
	version = "v1.2.3-test+build.4"
	t.Cleanup(func() { version = old })
	mux := securityHeaders(newMux())
	for _, path := range []string{"/", "/static/", "/static/index.html"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
		body := rec.Body.String()
		start, end := strings.Index(body, "<footer"), strings.Index(body, "</footer>")
		if start < 0 || end < start {
			t.Fatal("missing footer")
		}
		footer := html.UnescapeString(body[start:end])
		want := `x-text="L.version">版本</span> ` + version
		if !strings.Contains(footer, want) || strings.Index(footer, version) < strings.Index(footer, "License MIT") {
			t.Fatalf("%s must show build version after the footer links: %s", path, footer)
		}
		if strings.Contains(body, "{{VERSION}}") || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s did not serve rendered HTML", path)
		}
		// Version substitution must preserve the Alpine.js expressions.
		for _, expression := range []string{`x-data="speedTest()"`, `x-text="L.creditAuthor"`, `x-text="L.version"`} {
			if !strings.Contains(body, expression) {
				t.Fatalf("version substitution changed Alpine expression %s", expression)
			}
		}
	}
}

func TestWebVersionIsEscaped(t *testing.T) {
	old := version
	version = `<script>alert("version")</script>`
	t.Cleanup(func() { version = old })
	body := string(renderIndex())
	if strings.Contains(body, version) || !strings.Contains(body, html.EscapeString(version)) {
		t.Fatal("build version must be HTML-escaped")
	}
}

func TestClientTitleVersion(t *testing.T) {
	m := newClientModel(context.Background())
	for _, screen := range []clientScreen{screenLanguage, screenTest, screenHistory, screenDetail} {
		m.screen = screen
		if screen == screenHistory {
			m.buildHistory()
		}
		view := ansi.Strip(m.View())
		lines := strings.Split(strings.TrimSpace(view), "\n")
		if !strings.Contains(lines[0], version) {
			t.Errorf("screen %d title does not include version %q: %s", screen, version, lines[0])
		}
	}
	m.width, m.height = 30, 10
	if !strings.Contains(ansi.Strip(m.View()), "KuaiNet  "+version) {
		t.Fatal("small-terminal title is missing version")
	}
}
