// Package imageinput is the single shared loader for local image files used by
// every input surface (CLI exec --image, TUI /image, clipboard paste, PDF page
// rendering). It reads the bytes, sniffs the media type against the allow-list,
// normalizes the image into the provider-safe size envelope (resizing and
// re-encoding when needed), and returns a raw-bytes ImageBlock. Keeping it here
// means no surface duplicates the read/sniff/normalize logic.
package imageinput

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// LoadFile reads the image at path (resolved against workspaceRoot when
// relative), validates its type, and normalizes it into limits. Errors are plain
// (callers wrap them into surface-specific usage/notice text).
func LoadFile(path string, workspaceRoot string, limits Limits) (kajicoderuntime.ImageBlock, error) {
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(workspaceRoot, resolved)
	}

	// Reject oversized files via Stat BEFORE reading them into memory, so a huge
	// file never allocates a multi-gigabyte buffer just to be discarded by the
	// post-read cap. A missing file surfaces the same "not found" notice as a
	// failed read.
	info, err := os.Stat(resolved)
	if err != nil {
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("image file not found: %s", path)
	}
	// Reject non-regular files (directories, FIFOs, devices) up front. os.Stat
	// follows symlinks, so a symlink to a regular file still passes. This guards
	// against os.Open blocking forever on a writerless FIFO (an --image/ /image
	// path pointing at a named pipe would otherwise hang the process/UI).
	if !info.Mode().IsRegular() {
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("image file must be a regular file: %s", path)
	}
	if info.Size() > maxSourceBytes {
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("image %s is larger than the %d MiB limit", path, maxSourceBytes>>20)
	}

	// Bounded read: the os.Stat above is only a fast-path hint (a non-regular
	// file reports a misleading size, and the file can grow between Stat and the
	// read). A LimitReader of maxSourceBytes+1 is the real bound — at most one byte
	// past the cap is ever buffered, so an oversized or unbounded source (e.g. a
	// FIFO) can never allocate a multi-gigabyte buffer just to be discarded.
	file, err := os.Open(resolved)
	if err != nil {
		// Keep the real cause: a permission or I/O failure reported as "not
		// found" sends users hunting for a file that exists.
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("cannot open image %s: %w", path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxSourceBytes+1))
	if err != nil {
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("cannot read image %s: %w", path, err)
	}

	// The LimitReader yields at most maxSourceBytes+1 bytes; more than the cap
	// means the source was oversized (caught here regardless of any stat/read race).
	if len(data) > maxSourceBytes {
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("image %s is larger than the %d MiB limit", path, maxSourceBytes>>20)
	}

	block, err := normalizeImage(data, limits)
	if err != nil {
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("%s: %w", path, err)
	}
	return block, nil
}
