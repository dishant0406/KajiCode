package tui

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"io"
)

// Pasted images must stay visible in the transcript after the turn is sent, so a
// user transcript row carries its own decoded previews (transcriptRow.thumbs).
// The preview is a small colour grid, so it is stored compactly — the raw grid
// bytes deflated and base64-encoded — inside the user EventMessage payload. That
// keeps a resumed session's history identical without adding any new storage: the
// payload is the same JSON the session writer already persists.

// previewThumbEncodings encodes the previews for the session payload. Returns
// nil when no image is staged, so a text-only turn stores no extra field.
func previewThumbEncodings(attachments []stagedAttachment) []string {
	thumbs := historyThumbsFor(attachments)
	if len(thumbs) == 0 {
		return nil
	}
	out := make([]string, 0, len(thumbs))
	for _, t := range thumbs {
		if encoded, ok := encodeThumb(t); ok {
			out = append(out, encoded)
		}
	}
	return out
}

// userMessageSessionPayload is the EventMessage payload for a user turn: the
// prompt text plus any preview encodings, so rehydration can restore them.
func userMessageSessionPayload(content string, thumbs []string) map[string]any {
	payload := map[string]any{"role": "user", "content": content}
	if len(thumbs) > 0 {
		payload["thumbnails"] = thumbs
	}
	return payload
}

// encodeThumb serializes a preview grid: 8-byte header (little-endian w, h) then
// w*h RGB triples, deflated and base64-encoded. ok=false for an empty grid.
func encodeThumb(t *attachmentThumb) (string, bool) {
	if t == nil || t.w <= 0 || t.h <= 0 || len(t.px) < t.w*t.h {
		return "", false
	}
	raw := make([]byte, 8+3*t.w*t.h)
	binary.LittleEndian.PutUint32(raw[0:4], uint32(t.w))
	binary.LittleEndian.PutUint32(raw[4:8], uint32(t.h))
	off := 8
	for _, p := range t.px[:t.w*t.h] {
		raw[off], raw[off+1], raw[off+2] = p.r, p.g, p.b
		off += 3
	}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return "", false
	}
	if err := zw.Close(); err != nil {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), true
}

// decodeThumb reverses encodeThumb. It returns ok=false for anything malformed,
// so a corrupt payload degrades to a text-only row rather than failing resume.
func decodeThumb(encoded string) (*attachmentThumb, bool) {
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false
	}
	zr, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, false
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, 1<<22))
	if err != nil || len(raw) < 8 {
		return nil, false
	}
	w := int(binary.LittleEndian.Uint32(raw[0:4]))
	h := int(binary.LittleEndian.Uint32(raw[4:8]))
	// Bound the grid to the sample caps so a hostile payload cannot allocate an
	// unbounded slice; a legitimate preview never exceeds these.
	if w <= 0 || h <= 0 || w > thumbMaxW || h > thumbMaxH || len(raw) != 8+3*w*h {
		return nil, false
	}
	thumb := &attachmentThumb{w: w, h: h, px: make([]rgb, w*h)}
	off := 8
	for i := range thumb.px {
		thumb.px[i] = rgb{raw[off], raw[off+1], raw[off+2]}
		off += 3
	}
	return thumb, true
}

// thumbsFromPayload restores the previews a user EventMessage persisted. Missing
// or malformed entries are skipped, so a text-only row still renders.
func thumbsFromPayload(payload map[string]any) []*attachmentThumb {
	encoded := payloadStringSlice(payload, "thumbnails")
	if len(encoded) == 0 {
		return nil
	}
	thumbs := make([]*attachmentThumb, 0, len(encoded))
	for _, e := range encoded {
		if t, ok := decodeThumb(e); ok {
			thumbs = append(thumbs, t)
		}
	}
	return thumbs
}

// userThumbLines renders a user row's image previews as transcript lines: a
// horizontal queue in stored order, all sharing previewTargetRows so the row is
// one constant-height band. Nil when the row has no previews or previews are
// disabled (NO_COLOR). The caller indents the lines to sit under the prompt's
// gutter.
func userThumbLines(thumbs []*attachmentThumb, contentWidth int) []string {
	if !attachmentPreviewEnabled() {
		return nil
	}
	return renderPreviewRow(layoutPreviews(thumbs, contentWidth))
}
