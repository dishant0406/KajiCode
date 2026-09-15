package imageinput

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ReadClipboardImage returns the raw image bytes and media type from the OS
// clipboard, or (nil, "", nil) when the clipboard has no image. Called when
// text clipboard is empty (the user pasted a screenshot). The media type is
// sniffed from the bytes, not trusted from the clipboard, and the image is
// normalized into limits like every other input surface.
func ReadClipboardImage(limits Limits) ([]byte, string, error) {
	data, err := readClipboardImageBytes()
	if err != nil {
		return nil, "", err
	}
	if data == nil {
		return nil, "", nil
	}
	block, err := normalizeImage(data, limits)
	if err != nil {
		return nil, "", fmt.Errorf("clipboard %w", err)
	}
	return block.Data, block.MediaType, nil
}

// readClipboardImageBytes calls the platform-specific clipboard tool to extract
// image bytes. Returns (nil, nil) when no image is present.
func readClipboardImageBytes() ([]byte, error) {
	switch runtime.GOOS {
	case "windows":
		return readClipboardImageWindows()
	case "darwin":
		return readClipboardImageDarwin()
	case "linux":
		return readClipboardImageLinux()
	default:
		return nil, nil
	}
}

// readClipboardImageWindows uses PowerShell to check for and read a clipboard
// image. The image is saved as PNG to a temp file, read back, and the temp file
// deleted. Returns (nil, nil) when no image is on the clipboard.
func readClipboardImageWindows() ([]byte, error) {
	// Check if the clipboard contains an image.
	check := `Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; [System.Windows.Forms.Clipboard]::ContainsImage()`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", check).Output()
	if err != nil {
		return nil, nil // clipboard not available, treat as no image
	}
	if strings.TrimSpace(string(out)) != "True" {
		return nil, nil
	}
	// Save the clipboard image as PNG to a temp file, then read the bytes.
	// PowerShell stdout can't reliably emit raw binary — $ms.ToArray() prints
	// a .NET byte array as space-separated text, not raw bytes. A temp file
	// is the correct binary-safe path.
	tmpFile, err := os.CreateTemp("", "kajicode-clipboard-*.png")
	if err != nil {
		return nil, nil
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Stay on -Command: -File is subject to script execution policy (default
	// Restricted on Windows clients) and Windows PowerShell decodes BOM-less
	// UTF-8 .ps1 files as ANSI. Doubling single quotes is all the escaping the
	// single-quoted PowerShell literal needs.
	escapedTmpPath := strings.ReplaceAll(tmpPath, "'", "''")
	script := `Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; $img = [System.Windows.Forms.Clipboard]::GetImage(); if ($img -ne $null) { $img.Save('` + escapedTmpPath + `', [System.Drawing.Imaging.ImageFormat]::Png) }`
	cmd := exec.Command("powershell", "-NoProfile", "-Command", script)
	if err := cmd.Run(); err != nil {
		return nil, nil
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil || len(data) == 0 {
		return nil, nil
	}
	return data, nil
}

// readClipboardImageDarwin extracts image bytes from the macOS clipboard.
//
// It deliberately does NOT depend on pngpaste (a Homebrew formula that is
// usually absent) or on Python's AppKit bindings (pyobjc, also usually
// absent): both being missing made image paste fail silently on a stock Mac.
// Instead it uses `osascript` — always present on macOS — to address the
// clipboard via the native «class PNGf» AppleScript class and write the bytes
// straight to a temp file. `clipboard info` is a cheap pre-check so an
// image-less clipboard stays a fast no-op.
//
// The temp path is passed as an argv argument (`on run argv`), never
// interpolated into the script text, so a hostile path can't inject AppleScript.
func readClipboardImageDarwin() ([]byte, error) {
	// Fast pre-check: skip the extraction when the clipboard holds no image.
	out, err := exec.Command("osascript", "-e", "clipboard info").Output()
	if err != nil {
		return nil, nil
	}
	info := string(out)
	if !strings.Contains(info, "«class PNGf»") &&
		!strings.Contains(info, "JPEG picture") &&
		!strings.Contains(info, "TIFF picture") &&
		!strings.Contains(info, "GIF picture") {
		return nil, nil
	}

	// Address the clipboard as PNG — macOS re-encodes a JPEG/TIFF/GIF clipboard
	// to PNG on demand, which is why this one class covers every case. Run the
	// extraction in a loop over the image classes so a clipboard that lacks PNG
	// but has, say, only TIFF still works.
	for _, class := range []string{"«class PNGf»", "«class JPEG»", "TIFF picture"} {
		data, err := runDarwinClipboardExtract(class)
		if err != nil {
			continue
		}
		if len(data) > 0 {
			return data, nil
		}
	}
	return nil, nil
}

// darwinClipboardExtractScript writes the clipboard's `class` data to the file
// path given as argv item 1. The class is embedded (a fixed literal, never user
// input); the path is read from argv so it is not subject to script injection.
const darwinClipboardExtractScript = `on run argv
	set targetPath to item 1 of argv
	set imageData to (the clipboard as %s)
	set fileHandle to open for access (POSIX file targetPath) with write permission
	set eof fileHandle to 0
	write imageData to fileHandle
	close access fileHandle
end run`

// runDarwinClipboardExtract runs the extraction script for one clipboard class
// and returns the bytes it wrote, deleting the temp file afterward.
func runDarwinClipboardExtract(class string) ([]byte, error) {
	temp, err := os.CreateTemp("", "kajicode-clipboard-*.img")
	if err != nil {
		return nil, err
	}
	tempPath := temp.Name()
	temp.Close()
	defer os.Remove(tempPath)

	script := darwinClipboardExtractScript
	if len(class) > 0 {
		script = strings.Replace(script, "%s", class, 1)
	}
	cmd := exec.Command("osascript", "-e", script, tempPath)
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(tempPath)
	if err != nil || len(data) == 0 {
		return nil, nil
	}
	return data, nil
}

// readClipboardImageLinux tries wl-paste (Wayland) then xclip (X11) to read
// clipboard image bytes. Returns (nil, nil) when no image or no tool available.
//
// The MIME type comes from the clipboard (wl-paste --list-types / xclip
// TARGETS), so it is NEVER interpolated into a shell — every command runs via
// exec.Command(prog, args...) with the type passed as a discrete argument.
// (A hostile clipboard offerer could otherwise register a target like
// "image/png; rm -rf ~" that passes the "image/" prefix check.)
func readClipboardImageLinux() ([]byte, error) {
	// Try Wayland first.
	if types, err := runClipboardStdout("wl-paste", "--list-types"); err == nil {
		for _, t := range imageMIMETypes(types) {
			if data, err := runClipboardStdout("wl-paste", "--type", t); err == nil && len(data) > 0 {
				return data, nil
			}
		}
	}
	// Fall back to X11 xclip.
	if types, err := runClipboardStdout("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o"); err == nil {
		for _, t := range imageMIMETypes(types) {
			if data, err := runClipboardStdout("xclip", "-selection", "clipboard", "-t", t, "-o"); err == nil && len(data) > 0 {
				return data, nil
			}
		}
	}
	return nil, nil
}

// runClipboardStdout runs a clipboard helper and returns only its stdout.
// Stderr is discarded (the helpers are noisy when the clipboard is empty or the
// tool is missing); a missing tool surfaces as the command error, treated as
// "no image" by the callers.
func runClipboardStdout(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// imageMIMETypes extracts the "image/*" lines from a newline-separated type
// list, each safe to pass as a discrete argument (no shell).
func imageMIMETypes(list []byte) []string {
	var out []string
	for _, line := range strings.Split(string(list), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "image/") {
			out = append(out, line)
		}
	}
	return out
}
