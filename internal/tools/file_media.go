package tools

import (
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

// imageExtSet lists extensions read_file returns as a real image part. bmp/avif
// are intentionally excluded: imageinput's Normalize allow-list (png, jpeg, gif,
// webp) is authoritative, so an unsupported raster is refused up front rather
// than mislabelled.
var imageExtSet = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true,
}

// classifyFileKind determines how read_file should present the file. It mirrors
// opencode's read.ts: image/PDF return as media, an extension denylist plus a
// non-printable-byte heuristic mark binary, everything else is text.
func classifyFileKind(path string, content []byte) mediaKind {
	ext := strings.ToLower(filepath.Ext(path))

	if imageExtSet[ext] {
		return mediaImage
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
