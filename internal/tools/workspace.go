package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dishant0406/KajiCode/internal/sandbox"
	"github.com/dishant0406/KajiCode/internal/workspaceindex"
)

var ignoredDirectories = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
	".next":        true,
	".turbo":       true,
	"coverage":     true,
	".cache":       true,
	"tmp":          true,
	"temp":         true,
}

func normalizeWorkspaceRoot(workspaceRoot string) string {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return workspaceRoot
	}
	return root
}

func resolveWorkspacePath(workspaceRoot string, requestedPath string) (string, string, error) {
	if requestedPath == "" {
		requestedPath = "."
	}

	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}

	target := requestedPath
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}

	target, err = filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return "", "", err
	}

	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", "", outsideWorkspaceError(requestedPath)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", outsideWorkspaceError(requestedPath)
	}
	if relative == "." {
		return target, ".", nil
	}
	return target, filepath.ToSlash(relative), nil
}

func resolveWorkspaceTargetPath(workspaceRoot string, requestedPath string) (string, string, error) {
	if requestedPath == "" {
		requestedPath = "."
	}

	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}

	target := requestedPath
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	if err := recheckWorkspaceWriteTarget(root, target); err != nil {
		return "", "", err
	}

	existing := target
	missingSegments := []string{}
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if os.IsNotExist(err) {
			parent := filepath.Dir(existing)
			if parent == existing {
				return "", "", err
			}
			missingSegments = append([]string{filepath.Base(existing)}, missingSegments...)
			existing = parent
			continue
		} else {
			return "", "", err
		}
	}

	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", "", err
	}
	for _, segment := range missingSegments {
		resolved = filepath.Join(resolved, segment)
	}

	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", "", outsideWorkspaceError(requestedPath)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", outsideWorkspaceError(requestedPath)
	}
	if relative == "." {
		return resolved, ".", nil
	}
	return resolved, filepath.ToSlash(relative), nil
}

func recheckWorkspaceWriteTarget(workspaceRoot string, requestedPath string) error {
	if requestedPath == "" {
		requestedPath = "."
	}

	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}

	target := requestedPath
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}

	relative, err := filepath.Rel(root, target)
	if err != nil {
		return outsideWorkspaceError(requestedPath)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return outsideWorkspaceError(requestedPath)
	}
	if relative == "." {
		return nil
	}

	current := root
	for _, segment := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if segment == "." || segment == "" {
			continue
		}

		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			symlinkRelative, err := filepath.Rel(root, current)
			if err != nil {
				symlinkRelative = current
			}
			return fmt.Errorf("%s must not traverse symlink %s", requestedPath, filepath.ToSlash(symlinkRelative))
		}
	}

	return nil
}

func outsideWorkspaceError(requestedPath string) error {
	return fmt.Errorf("%s must stay inside the workspace", requestedPath)
}

// resolveOutsidePath resolves requestedPath with the workspace boundary lifted
// (a mode that grants unrestricted filesystem reach). Callers only reach this
// with an absolute path; a relative path is refused rather than resolved against
// the process CWD, which would be inconsistent with every other resolver's
// workspace-rooted relative semantics. Symlinks are resolved so the caller reads
// the canonical path and a symlink swap after resolution cannot change which file
// is read. The path must exist; the second return value is the absolute path
// because there is no single root to be relative to.
func resolveOutsidePath(requestedPath string) (string, string, error) {
	if !filepath.IsAbs(requestedPath) {
		return "", "", fmt.Errorf("%s must be an absolute path", requestedPath)
	}
	resolved, err := filepath.EvalSymlinks(requestedPath)
	if err != nil {
		return "", "", err
	}
	return resolved, resolved, nil
}

// observeProjectGuideline forwards absDir to the run's ProjectGuidelineObserver
// (if any). It is a nil-safe no-op when no observer is wired, so tools keep
// their exact prior behavior when the feature is disabled. The absolute
// directory is always used so the observer can walk upward to the git root
// without dependence on a workspace-relative base.
func observeProjectGuideline(options RunOptions, absDir string) {
	if options.ProjectGuidelines == nil {
		return
	}
	if absDir == "" {
		return
	}
	options.ProjectGuidelines.ObservePath(absDir)
}

func shouldSkipDirectory(name string) bool {
	return ignoredDirectories[name] || workspaceindex.ShouldSkipDir(name)
}

func shouldSkipWorkspaceFile(path string) bool {
	return workspaceindex.ShouldSkipFile(path)
}

// PathScope is the multi-root write scope shared with the sandbox engine.
// *sandbox.Scope satisfies it; nil means workspace-only (today's behavior).
// Roots()[0] must be the workspace root (sandbox.Scope guarantees this
// ordering); relative paths and error messages key off it.
type PathScope interface {
	Roots() []string
}

type readPathScope interface {
	ReadRoots() []string
}

// scopedRoots returns the ordered roots to try for an absolute path: the
// scope's roots when present, else just the workspace root. A non-nil scope
// must expose at least one root (the workspace root first, per PathScope); an
// empty Roots() is a contract block that fails closed with an error so the
// scoped helpers never silently accept a path with no root to validate against.
// Returning success there would resolve to an empty path and, e.g., run bash
// with Cmd.Dir == "" (the process cwd) instead of an allowed root.
func scopedRoots(workspaceRoot string, scope PathScope) ([]string, error) {
	if scope == nil {
		return []string{workspaceRoot}, nil
	}
	roots := scope.Roots()
	if len(roots) == 0 {
		return nil, fmt.Errorf("invalid path scope: no write roots configured")
	}
	return roots, nil
}

func scopedReadRoots(workspaceRoot string, scope PathScope) ([]string, error) {
	if scope == nil {
		return []string{workspaceRoot}, nil
	}
	if reader, ok := scope.(readPathScope); ok {
		roots := reader.ReadRoots()
		if len(roots) == 0 {
			return nil, fmt.Errorf("invalid path scope: no read roots configured")
		}
		return roots, nil
	}
	return scopedRoots(workspaceRoot, scope)
}

// modeLiftsBoundary reports whether the active permission mode lifts the
// workspace boundary for a read or a write. It mirrors the sandbox engine's own
// mode handling (internal/sandbox/engine.go), which disables the policy for
// bypass-all and clears enforceWorkspace for reads under read-only/read-write —
// the native tools must not impose a stricture the engine does not, or the model
// routes around them with bash, which runs unsandboxed.
//
// ask-all (and empty/unknown) return false so their containment is unchanged.
func modeLiftsBoundary(mode string, write bool) bool {
	switch sandbox.NormalizePermissionMode(sandbox.PermissionMode(mode)) {
	case sandbox.PermissionModeBypassAll:
		return true
	case sandbox.PermissionModeReadOnly:
		return !write
	case sandbox.PermissionModeReadWrite:
		return true
	default:
		return false
	}
}

// boundaryNote is appended to the description of every path-taking tool. The
// path boundary is decided by the ACTIVE PERMISSION MODE, not by the tool (see
// modeLiftsBoundary). Without this note the descriptions read as workspace-only,
// so a model handed an outside path reaches for bash — the one tool that always
// reaches — instead of the native tool that would have worked. Keep it a single
// constant so the wording cannot drift between tools.
const boundaryNote = " The path boundary follows the active permission mode:" +
	" under bypass-all any absolute path is allowed, and under read-only/read-write" +
	" reads outside the workspace are allowed; under ask-all the path must stay" +
	" inside the workspace or a granted extra root."

func resolveScopedReadPath(workspaceRoot string, scope PathScope, mode string, requestedPath string) (string, string, error) {
	// Spill files (truncated tool output saved under the per-uid temp dir) are
	// readable regardless of scope: the truncation notice tells the model to
	// read_file/grep them, which must actually work. resolveSpillReadPath
	// verifies containment after symlink resolution, so this cannot be used to
	// reach anything outside the spill dir.
	if spillPath, ok := resolveSpillReadPath(requestedPath); ok {
		return spillPath, spillPath, nil
	}
	if requestedPath == "" || !filepath.IsAbs(requestedPath) || scope == nil {
		return resolveWorkspacePath(workspaceRoot, requestedPath)
	}
	roots, err := scopedReadRoots(workspaceRoot, scope)
	if err != nil {
		return "", "", err
	}
	var firstErr error
	for index, root := range roots {
		candidate := sandbox.NormalizePrefixForRoot(requestedPath, root)
		absolute, relative, err := resolveWorkspacePath(root, candidate)
		if err == nil {
			if index > 0 {
				return absolute, absolute, nil
			}
			return absolute, relative, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if modeLiftsBoundary(mode, false) {
		// An explicit relative path still resolves against the workspace; only an
		// absolute path can meaningfully leave it.
		if absolute, relative, err := resolveWorkspacePath(workspaceRoot, requestedPath); err == nil {
			return absolute, relative, nil
		}
		return resolveOutsidePath(requestedPath)
	}
	return "", "", firstErr
}

// resolveOutsideTargetPath mirrors resolveOutsidePath for a write target that may
// not exist yet: the nearest existing ancestor is symlink-resolved and the
// missing tail is re-appended, so a create-through-missing-dirs write lands on
// the canonical path.
func resolveOutsideTargetPath(requestedPath string) (string, string, error) {
	if !filepath.IsAbs(requestedPath) {
		return "", "", fmt.Errorf("%s must be an absolute path", requestedPath)
	}

	existing := requestedPath
	var missing []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if os.IsNotExist(err) {
			parent := filepath.Dir(existing)
			if parent == existing {
				return "", "", err
			}
			missing = append([]string{filepath.Base(existing)}, missing...)
			existing = parent
			continue
		} else {
			return "", "", err
		}
	}

	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", "", err
	}
	for _, segment := range missing {
		resolved = filepath.Join(resolved, segment)
	}
	return resolved, resolved, nil
}

// resolveScopedPath is resolveWorkspacePath generalized to a scope: relative
// paths resolve against the workspace root only; an absolute path resolves
// against the first root that contains it. The workspace root's error is
// returned when no root matches so messages stay stable. Prefix symlinks
// outside a root are resolved per root (macOS /var aliasing); symlinks INSIDE
// a root stay visible to the single-root checks, so a write target may resolve
// through a symlink only when its final location lies inside a DIFFERENT
// granted root — mirroring sandbox.Scope.validate's documented widening.
// When the matched root is an extra (non-workspace) root the second return
// value is the absolute path rather than a per-root-relative path; "relative
// to which root" is ambiguous for downstream consumers (ChangedFiles, cwd
// meta, display summaries) that document workspace-relative paths.
// When all roots deny, the workspace root's error is returned; unlike
// sandbox.Scope.validate this does not prefer traversal blocks — the
// engine layer reports those with full fidelity before tools run.
func resolveScopedPath(workspaceRoot string, scope PathScope, mode string, write bool, requestedPath string) (string, string, error) {
	if requestedPath == "" || !filepath.IsAbs(requestedPath) || scope == nil {
		return resolveWorkspacePath(workspaceRoot, requestedPath)
	}
	roots, err := scopedRoots(workspaceRoot, scope)
	if err != nil {
		return "", "", err
	}
	var firstErr error
	for index, root := range roots {
		// Normalize platform-level symlinks (e.g. macOS /var -> /private/var)
		// in the prefix outside this root only, leaving in-root components
		// verbatim for the single-root symlink checks.
		candidate := sandbox.NormalizePrefixForRoot(requestedPath, root)
		absolute, relative, err := resolveWorkspacePath(root, candidate)
		if err == nil {
			if index > 0 {
				// Extra-root matches report the absolute path: "relative to
				// which root" is ambiguous downstream (ChangedFiles, cwd
				// meta, display), and consumers document workspace-relative.
				return absolute, absolute, nil
			}
			return absolute, relative, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if modeLiftsBoundary(mode, write) {
		return resolveOutsidePath(requestedPath)
	}
	return "", "", firstErr
}

// resolveScopedTargetPath mirrors resolveWorkspaceTargetPath for write targets
// (the target may not exist yet) across all scope roots. When the matched root
// is an extra (non-workspace) root the second return value is the absolute
// path rather than a per-root-relative path; "relative to which root" is
// ambiguous for downstream consumers (ChangedFiles, cwd meta, display
// summaries) that document workspace-relative paths.
// When all roots deny, the workspace root's error is returned; unlike
// sandbox.Scope.validate this does not prefer traversal blocks — the
// engine layer reports those with full fidelity before tools run.
func resolveScopedTargetPath(workspaceRoot string, scope PathScope, mode string, requestedPath string) (string, string, error) {
	if requestedPath == "" || !filepath.IsAbs(requestedPath) || scope == nil {
		return resolveWorkspaceTargetPath(workspaceRoot, requestedPath)
	}
	roots, err := scopedRoots(workspaceRoot, scope)
	if err != nil {
		return "", "", err
	}
	var firstErr error
	for index, root := range roots {
		// Normalize platform-level symlinks (e.g. macOS /var -> /private/var)
		// in the prefix outside this root only; in-root components stay
		// verbatim so the per-segment write-target symlink checks apply.
		candidate := sandbox.NormalizePrefixForRoot(requestedPath, root)
		absolute, relative, err := resolveWorkspaceTargetPath(root, candidate)
		if err == nil {
			if index > 0 {
				// Extra-root matches report the absolute path: "relative to
				// which root" is ambiguous downstream (ChangedFiles, cwd
				// meta, display), and consumers document workspace-relative.
				return absolute, absolute, nil
			}
			return absolute, relative, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if modeLiftsBoundary(mode, true) {
		return resolveOutsideTargetPath(requestedPath)
	}
	return "", "", firstErr
}

// recheckScopedWriteTarget mirrors recheckWorkspaceWriteTarget across roots.
// Prefix symlinks outside a root are resolved per root (macOS /var aliasing);
// symlinks INSIDE a root stay visible to the single-root checks, so a write
// target may resolve through a symlink only when its final location lies
// inside a DIFFERENT granted root — mirroring sandbox.Scope.validate's
// documented widening.
func recheckScopedWriteTarget(workspaceRoot string, scope PathScope, mode string, requestedPath string) error {
	if requestedPath == "" || !filepath.IsAbs(requestedPath) || scope == nil {
		return recheckWorkspaceWriteTarget(workspaceRoot, requestedPath)
	}
	roots, err := scopedRoots(workspaceRoot, scope)
	if err != nil {
		return err
	}
	var firstErr error
	for _, root := range roots {
		candidate := sandbox.NormalizePrefixForRoot(requestedPath, root)
		err := recheckWorkspaceWriteTarget(root, candidate)
		if err == nil {
			return nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if modeLiftsBoundary(mode, true) {
		return nil
	}
	return firstErr
}
