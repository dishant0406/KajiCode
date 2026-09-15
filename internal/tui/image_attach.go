package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dishant0406/KajiCode/internal/imageinput"
	"github.com/dishant0406/KajiCode/internal/modelregistry"
)

// droppableImageExts are the image extensions a dragged-and-dropped file may
// carry (matched case-insensitively); PDFs and SVGs are recognized separately.
var droppableImageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
}

// droppedAttachmentPath recognizes a single drag-dropped (or pasted) file path
// that points at an existing image or PDF, returning the cleaned path. Terminals
// deliver a dropped file as its path with spaces/special chars backslash-escaped
// (or the whole path quoted); this undoes that so "Screenshot 2026 at 1.png"
// resolves. ok is false for anything that is not a single existing image/PDF
// file, so normal text pastes and real slash-commands are left untouched.
func droppedAttachmentPath(content, cwd string) (string, bool) {
	s := strings.TrimSpace(content)
	if s == "" || strings.ContainsAny(s, "\n\r") {
		return "", false // empty or multi-line: not a single dropped file
	}
	if unq, quoted := stripMatchingQuotes(s); quoted {
		s = unq // a quoted path is literal — do not unescape inside it
	} else {
		s = unescapeDroppedPath(s)
	}
	if s == "" {
		return "", false
	}
	resolved := s
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(cwd, resolved)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	ext := strings.ToLower(filepath.Ext(s))
	if droppableImageExts[ext] || ext == ".pdf" || ext == ".svg" || imageinput.LooksLikeDocumentFile(s, cwd) {
		return s, true
	}
	return "", false
}

// unescapeDroppedPath drops a backslash before any following byte, undoing the
// terminal's drag-drop escaping ("\ " -> " ", "\(" -> "(", "\\" -> "\").
func unescapeDroppedPath(s string) string {
	if runtime.GOOS == "windows" {
		// On Windows the backslash is the path separator, not a drag-drop escape;
		// stripping it would corrupt real paths (C:\Users\… -> C:Users…). Dropped
		// paths there arrive quoted (handled by stripMatchingQuotes) or plain.
		return s
	}
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// stripMatchingQuotes removes a single pair of surrounding ' or " quotes.
func stripMatchingQuotes(s string) (string, bool) {
	if len(s) >= 2 {
		q := s[0]
		if (q == '"' || q == '\'') && s[len(s)-1] == q {
			return s[1 : len(s)-1], true
		}
	}
	return s, false
}

// effectiveModelName returns the model that will actually serve the current
// turn's request. With an explicit active role that resolves to a provider +
// model, that role-routed model wins (the loop swaps the provider to it per
// turn). Otherwise the session default (m.modelName) is used. This matters for
// capability gates like the vision check: attaching an image while the "vision"
// role routes to a vision-capable model must not be refused based on the
// default (non-vision) model.
func (m model) effectiveModelName() string {
	if role := strings.TrimSpace(m.activeRole); role != "" {
		if router := m.roleRouter(); router != nil {
			if profile, ok := router.ProfileFor(role); ok && strings.TrimSpace(profile.Model) != "" {
				return strings.TrimSpace(profile.Model)
			}
		}
	}
	return strings.TrimSpace(m.modelName)
}

// modelSupportsVisionTUI reports whether the model serving the current turn can
// accept image input. It defers to the single vision authority,
// modelregistry.SupportsVision, which consults the curated registry first and
// models.dev (live snapshot, then the embedded seed) for anything it does not
// know. There is no separate name heuristic here — one authority, one answer,
// shared by the headless and interactive surfaces.
func (m model) modelSupportsVisionTUI() bool {
	trimmed := m.effectiveModelName()
	if trimmed == "" {
		return false
	}
	return modelregistry.SupportsVision(m.modelCatalog, trimmed)
}

// attachClipboardImage attaches an image read from the OS clipboard (a
// screenshot paste). The bytes were already normalized into the size envelope by
// ReadClipboardImage, so this only applies the vision gate.
func (m model) attachClipboardImage(data []byte, mediaType string) model {
	if !m.modelSupportsVisionTUI() {
		name := m.effectiveModelName()
		if name == "" {
			name = "the active model"
		}
		return m.appendImageNotice("Model " + name + " does not support image input; clipboard image refused.")
	}
	m = m.attachStaged(newImageAttachment("clipboard", mediaType, data))
	return m
}

// handleImageCommand processes "/image <path>" and "/image clear". A bare
// "/image" prints usage. PDFs are routed to the document path (text layer always
// attaches; pages rasterize to images only for vision models with a rasterizer).
// Image files attach only to vision models. Attachment failures (missing file,
// unsupported type, oversize) surface as an inline notice and attach nothing.
func (m model) handleImageCommand(arg string) model {
	trimmed := strings.TrimSpace(arg)
	switch {
	case trimmed == "":
		return m.appendImageNotice("Usage: /image <path>  (image, PDF, or SVG; or /image clear)")
	case strings.EqualFold(trimmed, "clear"):
		m.pendingAttachments = nil
		m.composerPastePreviews = composerPastePreviewsWithoutAttachments(m.composerPastePreviews)
		return m.appendImageNotice("Cleared pending attachments.")
	}

	// A PDF carries a text layer every model can read, so it is not gated on
	// vision the way a raw image is; the optional rasterized pages are. Route by
	// the ".pdf" hint OR a content sniff so a real PDF whose name lacks the
	// extension still reaches the document path rather than the vision-only image
	// path. The cheap header sniff runs before the vision gate.
	if imageinput.IsProbablyDocumentPath(trimmed) || imageinput.LooksLikeDocumentFile(trimmed, m.cwd) {
		return m.handleDocumentAttach(trimmed)
	}

	// An SVG is text a model reads directly, so like a PDF it is not gated on
	// vision; attach its markup rather than sending image/svg+xml, which no
	// vision provider accepts.
	if imageinput.IsSVGPath(trimmed) {
		return m.handleSVGAttach(trimmed)
	}

	if !m.modelSupportsVisionTUI() {
		name := m.effectiveModelName()
		if name == "" {
			name = "the active model"
		}
		return m.appendImageNotice("Model " + name + " does not support image input; attachment refused.")
	}

	block, err := imageinput.LoadFile(trimmed, m.cwd, m.imageLimits)
	if err != nil {
		return m.appendImageNotice(err.Error())
	}

	m = m.attachStaged(newImageAttachment(filepath.Base(trimmed), block.MediaType, block.Data))
	// No "attached" system message: the inline [Image #N] token in the composer is
	// the confirmation, matching the compact attach UX.
	return m
}

// handleDocumentAttach loads a PDF through imageinput.LoadDocument. The text
// layer is staged for every model; when the active model supports vision and a
// rasterizer is available, the rendered pages are staged through the existing
// pending-image pipeline too. A scanned PDF with no text (and no rasterizer)
// surfaces LoadDocument's explicit "no extractable text" notice and attaches
// nothing.
func (m model) handleDocumentAttach(path string) model {
	doc, err := imageinput.LoadDocument(path, m.cwd, imageinput.DocumentOptions{
		Vision: m.modelSupportsVisionTUI(),
		Limits: m.imageLimits,
	})
	if err != nil {
		return m.appendImageNotice(err.Error())
	}

	label := filepath.Base(path)
	if strings.TrimSpace(doc.Text) != "" {
		m = m.attachStaged(stagedAttachment{Label: label, DocText: doc.Text})
	}
	for _, block := range doc.Images {
		m = m.attachStaged(newImageAttachment(label, block.MediaType, block.Data))
	}
	// The inline [Doc #N] / [Image #N] token in the composer is the confirmation;
	// no "attached" system message.
	return m
}

// handleSVGAttach loads an SVG's markup through imageinput.LoadSVG and stages it
// as document text, so it flows through the same [Doc #N] token and preamble path
// a PDF text layer uses. SVG is not vision-gated: it is source text.
func (m model) handleSVGAttach(path string) model {
	text, err := imageinput.LoadSVG(path, m.cwd)
	if err != nil {
		return m.appendImageNotice(err.Error())
	}
	return m.attachStaged(stagedAttachment{Label: filepath.Base(path), DocText: text})
}

// appendImageNotice appends an image-related notice to the transcript. Image
// errors (vision gate refusal, oversize, unsupported type) render with a red
// error border + red text so they stand out from ordinary grey system notes.
func (m model) appendImageNotice(text string) model {
	row := transcriptRow{
		kind: rowError,
		text: text,
	}
	m.transcript = appendTranscriptRow(m.transcript, row)
	return m
}

// visionDropWarning returns a one-line notice when images are staged but the
// (now active) model can't accept them, so switching to a non-vision model warns
// the user immediately at switch time instead of silently dropping the images at
// submit. Empty when there is nothing staged or the model supports vision.
func (m model) visionDropWarning() string {
	if len(m.turnImages()) == 0 || m.modelSupportsVisionTUI() {
		return ""
	}
	return fmt.Sprintf("⚠ %d staged image(s) will be dropped — %s has no vision support.",
		len(m.turnImages()), displayValue(m.modelName, "the active model"))
}
