package imageinput

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// IsSVGPath reports whether a path has an ".svg" extension (case-insensitive).
// It is only a routing hint for drag-drop detection, which runs before the file
// is opened; LoadSVG re-verifies the real content.
func IsSVGPath(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".svg")
}

// isSVG reports whether data is an SVG document: after an optional UTF-8 BOM, an
// optional XML prologue (declaration, doctype, or comment), and leading
// whitespace, its root element must be <svg>. A file that merely starts like XML
// but has a different root (e.g. an HTML page) is not an SVG.
func isSVG(data []byte) bool {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	for len(trimmed) > 0 && trimmed[0] == '<' {
		switch {
		case bytes.HasPrefix(trimmed, []byte("<!--")):
			end := bytes.Index(trimmed, []byte("-->"))
			if end < 0 {
				return false
			}
			trimmed = bytes.TrimLeft(trimmed[end+3:], " \t\r\n")
		case bytes.HasPrefix(trimmed, []byte("<?")), bytes.HasPrefix(trimmed, []byte("<!")):
			end := bytes.IndexByte(trimmed, '>')
			if end < 0 {
				return false
			}
			trimmed = bytes.TrimLeft(trimmed[end+1:], " \t\r\n")
		default:
			return hasSVGRoot(trimmed)
		}
	}
	return false
}

// hasSVGRoot reports whether data begins with a <svg> root element tag rather
// than a longer name like <svgfoo>.
func hasSVGRoot(data []byte) bool {
	const root = "<svg"
	if !bytes.HasPrefix(data, []byte(root)) {
		return false
	}
	rest := data[len(root):]
	if len(rest) == 0 {
		return true
	}
	switch rest[0] {
	case '>', '/', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// LoadSVG reads the SVG at path (resolved against workspaceRoot when relative)
// and returns its markup as text. Vision providers do not accept image/svg+xml,
// and an SVG is source a model can read directly, so it is attached as text
// rather than rasterized. The size is bounded by the shared document cap and the
// bytes are validated as XML text, so a mis-extensioned binary cannot be
// smuggled through as text.
func LoadSVG(path string, workspaceRoot string) (string, error) {
	data, err := readDocumentBytes(path, workspaceRoot)
	if err != nil {
		return "", err
	}
	if !isSVG(data) {
		return "", fmt.Errorf("%s is not an SVG (expected an <svg> or XML document)", path)
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", fmt.Errorf("%s is not valid SVG text", path)
	}
	text, _ := capDocumentText(string(data))
	return text, nil
}
