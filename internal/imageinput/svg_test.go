package imageinput

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSVG(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write svg: %v", err)
	}
	return path
}

func TestLoadSVGReturnsMarkup(t *testing.T) {
	const markup = `<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"><rect width="4" height="4"/></svg>`
	path := writeSVG(t, "icon.svg", markup)
	got, err := LoadSVG(path, "")
	if err != nil {
		t.Fatalf("LoadSVG: %v", err)
	}
	if strings.TrimSpace(got) != markup {
		t.Fatalf("LoadSVG = %q, want the markup unchanged", got)
	}
}

func TestLoadSVGAcceptsXMLPrologue(t *testing.T) {
	path := writeSVG(t, "icon.svg", "<?xml version=\"1.0\"?>\n<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>")
	if _, err := LoadSVG(path, ""); err != nil {
		t.Fatalf("LoadSVG with XML prologue: %v", err)
	}
}

func TestLoadSVGRejectsNonSVG(t *testing.T) {
	path := writeSVG(t, "fake.svg", "this is not an svg")
	_, err := LoadSVG(path, "")
	if err == nil {
		t.Fatal("expected error for non-SVG content")
	}
	if !strings.Contains(err.Error(), "not an SVG") {
		t.Fatalf("error = %q, want an explicit not-an-SVG message", err.Error())
	}
}

func TestLoadSVGRejectsBinary(t *testing.T) {
	// An .svg path pointing at binary must be refused rather than shipped as text.
	path := writeSVG(t, "fake.svg", "<svg>\x00\x01\x02</svg>")
	if _, err := LoadSVG(path, ""); err == nil {
		t.Fatal("expected error for binary content in an svg")
	}
}

func TestLoadSVGRejectsNonSVGRoot(t *testing.T) {
	// A file that merely starts like XML (declaration, doctype, comment) but has a
	// non-<svg> root element is not an SVG and must be refused.
	for name, content := range map[string]string{
		"html-after-prologue": "<?xml version=\"1.0\"?>\n<html><body>hi</body></html>",
		"doctype-then-html":   "<!DOCTYPE html><html></html>",
		"comment-then-text":   "<!-- note -->\nplain text, no svg here",
		"svg-prefixed-name":   "<svgset><svg></svgset>",
	} {
		path := writeSVG(t, "fake.svg", content)
		if _, err := LoadSVG(path, ""); err == nil {
			t.Errorf("%s: expected error for a non-<svg> root", name)
		}
	}
}

func TestIsSVGPath(t *testing.T) {
	cases := map[string]bool{
		"icon.svg":     true,
		"ICON.SVG":     true,
		"icon.svg.png": false,
		"icon.png":     false,
		"icon":         false,
	}
	for path, want := range cases {
		if got := IsSVGPath(path); got != want {
			t.Errorf("IsSVGPath(%q) = %v, want %v", path, got, want)
		}
	}
}
