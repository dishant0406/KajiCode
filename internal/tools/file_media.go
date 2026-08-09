package tools

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// mediaKind classifies a file for read_file handling.
type mediaKind int

const (
	mediaText mediaKind = iota
	mediaImage
	mediaPDF
	mediaBinary
)

var binaryExtSet = map[string]bool{
	".zip": true, ".tar": true, ".gz": true, ".exe": true, ".dll": true,
	".so": true, ".class": true, ".jar": true, ".war": true, ".7z": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true,
	".pptx": true, ".odt": true, ".ods": true, ".odp": true, ".bin": true,
	".dat": true, ".obj": true, ".o": true, ".a": true, ".lib": true,
	".wasm": true, ".pyc": true, ".pyo": true,
}

var imageExtMedia = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".webp": "image/webp", ".gif": "image/gif", ".bmp": "image/bmp",
}

// classifyFileKind determines how read_file should present the file. It mirrors
// opencode's read.ts: image/PDF return as media, an extension denylist plus a
// non-printable-byte heuristic mark binary, everything else is text.
func classifyFileKind(path string, content []byte) mediaKind {
	ext := strings.ToLower(filepath.Ext(path))

	if media, ok := imageExtMedia[ext]; ok && media != "image/svg+xml" {
		// Keep it a text path only if the file is an actual raster image with a
		// non-text sniff; SVG is text and should render as text.
		switch ext {
		case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp":
			return mediaImage
		}
	}
	if ext == ".pdf" {
		return mediaPDF
	}
	if binaryExtSet[ext] {
		return mediaBinary
	}

	if len(content) == 0 {
		// Empty content is text-safe; don't classify as binary.
		return mediaText
	}

	// Heuristic: > 30% non-printable bytes (outside CR/LF/TAB/FF) => binary.
	sample := content
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	nonPrintable := 0
	for _, b := range sample {
		if b == 0 {
			return mediaBinary
		}
		if b < 9 || (b > 13 && b < 32) {
			nonPrintable++
		}
	}
	if float64(nonPrintable)/float64(len(sample)) > 0.3 {
		return mediaBinary
	}
	return mediaText
}

// mediaMimeForPath returns a mime type for image/PDF paths, or empty.
func mediaMimeForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if mime, ok := imageExtMedia[ext]; ok && ext != ".svg" {
		return mime
	}
	if ext == ".pdf" {
		return "application/pdf"
	}
	return ""
}

// fileContentFor returns the file bytes and any stat error. Used by read_file for
// binary/media classification and by did-you-mean recovery.
func fileContentFor(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
