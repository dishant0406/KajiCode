package tui

import "strconv"

type sidebarDerivedCache struct {
	touchedFilesKey string
	touchedFiles    []touchedFile
}

func newSidebarDerivedCache() *sidebarDerivedCache {
	return &sidebarDerivedCache{}
}

func (m model) cachedTouchedFiles() []touchedFile {
	key := m.touchedFilesCacheKey()
	if m.sidebarDerivedCache != nil && m.sidebarDerivedCache.touchedFilesKey == key {
		return m.sidebarDerivedCache.touchedFiles
	}
	files := m.computeTouchedFiles()
	if m.sidebarDerivedCache != nil {
		m.sidebarDerivedCache.touchedFilesKey = key
		m.sidebarDerivedCache.touchedFiles = files
	}
	return files
}

func (m model) touchedFilesCacheKey() string {
	hash := newTranscriptFingerprintHash()
	writeFingerprintField(&hash, "sidebar-touched-files-v1")
	writeFingerprintField(&hash, strconv.Itoa(len(m.transcript)))
	for _, row := range m.transcript {
		if row.kind != rowToolResult || len(row.changedFiles) == 0 {
			continue
		}
		writeFingerprintField(&hash, transcriptRowRenderFingerprint(row))
	}
	writeFingerprintField(&hash, strconv.Itoa(len(m.gitTouched)))
	for _, file := range m.gitTouched {
		writeFingerprintField(&hash, file.path)
		writeFingerprintField(&hash, strconv.FormatBool(file.created))
		writeFingerprintField(&hash, strconv.Itoa(file.adds))
		writeFingerprintField(&hash, strconv.Itoa(file.dels))
	}
	return hash.sumString()
}
