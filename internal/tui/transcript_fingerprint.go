package tui

import (
	"fmt"
	"strconv"
	"time"
)

const (
	fnv64Offset = 14695981039346656037
	fnv64Prime  = 1099511628211
)

type transcriptFingerprintHash uint64

func newTranscriptFingerprintHash() transcriptFingerprintHash {
	return transcriptFingerprintHash(fnv64Offset)
}

func (h *transcriptFingerprintHash) writeString(value string) {
	for i := 0; i < len(value); i++ {
		*h ^= transcriptFingerprintHash(value[i])
		*h *= fnv64Prime
	}
}

func (h transcriptFingerprintHash) sumString() string {
	return strconv.FormatUint(uint64(h), 16)
}

func prepareTranscriptRow(row transcriptRow) transcriptRow {
	if row.renderFingerprint == "" {
		row.renderFingerprint = transcriptRowFingerprint(row)
	}
	return row
}

func transcriptRowRenderFingerprint(row transcriptRow) string {
	if row.renderFingerprint != "" {
		return row.renderFingerprint
	}
	return transcriptRowFingerprint(row)
}

func transcriptRowFingerprint(row transcriptRow) string {
	hash := newTranscriptFingerprintHash()
	writeFingerprintField(&hash, "render-row-content-v1")
	writeFingerprintField(&hash, strconv.Itoa(int(row.kind)))
	writeFingerprintField(&hash, row.id)
	writeFingerprintField(&hash, row.text)
	writeFingerprintField(&hash, row.tool)
	writeFingerprintField(&hash, fmt.Sprint(row.status))
	writeFingerprintField(&hash, row.detail)
	writeFingerprintField(&hash, row.hint)
	writeFingerprintField(&hash, row.arg)
	writeFingerprintField(&hash, strconv.Itoa(row.runID))
	writeFingerprintField(&hash, strconv.FormatBool(row.expanded))
	writeFingerprintField(&hash, strconv.FormatBool(row.final))
	writeFingerprintField(&hash, strconv.Itoa(row.turnTools))
	writeFingerprintField(&hash, strconv.FormatInt(int64(row.turnElapsed), 10))
	for _, file := range row.changedFiles {
		writeFingerprintField(&hash, file)
	}
	writeFingerprintField(&hash, permissionCacheFingerprint(row.permission))
	writeFingerprintField(&hash, askUserCacheFingerprint(row.askUser))
	writeSpecialistFingerprintFields(&hash, row.specialistInfo)
	return hash.sumString()
}

func writeSpecialistFingerprintFields(hash *transcriptFingerprintHash, info *specialistInfo) {
	if info == nil {
		writeFingerprintField(hash, "")
		return
	}
	writeFingerprintField(hash, info.name)
	writeFingerprintField(hash, info.description)
	writeFingerprintField(hash, info.childSessionID)
	writeFingerprintField(hash, strconv.Itoa(int(info.status)))
	writeFingerprintTime(hash, info.startedAt)
	writeFingerprintTime(hash, info.completedAt)
	writeFingerprintField(hash, strconv.Itoa(info.exitCode))
	writeFingerprintField(hash, info.errorMsg)
	writeFingerprintField(hash, strconv.Itoa(info.toolCount))
	writeFingerprintField(hash, strconv.Itoa(info.tokenCount))
	writeFingerprintField(hash, info.currentTool)
	writeFingerprintField(hash, info.currentDetail)
}

func writeFingerprintTime(hash *transcriptFingerprintHash, value time.Time) {
	if value.IsZero() {
		writeFingerprintField(hash, "")
		return
	}
	writeFingerprintField(hash, value.UTC().Format(time.RFC3339Nano))
}

func writeFingerprintField(hash *transcriptFingerprintHash, value string) {
	hash.writeString(strconv.Itoa(len(value)))
	hash.writeString(":")
	hash.writeString(value)
	hash.writeString("|")
}
