package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// plainTranscriptText renders the conversation as a plain, role-prefixed text
// document for /export — the readable content (user prompts, assistant answers,
// system notes, errors), skipping tool-call/permission UI noise.
func (m model) plainTranscriptText() string {
	var b strings.Builder
	for _, row := range m.transcript {
		var prefix string
		switch row.kind {
		case rowUser:
			prefix = "you: "
		case rowAssistant:
			prefix = "kajicode: "
		case rowSystem:
			prefix = "· "
		case rowError:
			prefix = "error: "
		default:
			continue
		}
		text := strings.TrimRight(row.text, "\n")
		if strings.TrimSpace(text) == "" {
			continue
		}
		b.WriteString(prefix)
		b.WriteString(text)
		// Exported text carries no terminal colors, so a user row with images
		// records how many, keeping the export readable instead of dropping them.
		for i := range row.thumbs {
			b.WriteString(fmt.Sprintf("\n[image #%d]", i+1))
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

// handleExportCommand writes the transcript to a file and returns a status
// message. With no argument it derives a timestamped filename in the workspace;
// a relative path is resolved against the workspace root.
func (m model) handleExportCommand(args string) string {
	body := m.plainTranscriptText()
	if strings.TrimSpace(body) == "" {
		return "Export\nnothing to export yet."
	}

	path := strings.TrimSpace(args)
	if path == "" {
		stamp := m.now().Format("20060102-150405")
		path = fmt.Sprintf("kajicode-transcript-%s.txt", stamp)
	}
	if !filepath.IsAbs(path) && m.cwd != "" {
		path = filepath.Join(m.cwd, path)
	}
	// 0o600: a transcript can include tool/bash output that echoed secrets, so it
	// must not be world/group-readable on a shared machine.
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "Export\nfailed to write " + path + ": " + err.Error()
	}
	return "Export\nwrote transcript to " + path
}
