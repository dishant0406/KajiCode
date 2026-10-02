package tools

// MutationTargets returns the workspace-relative paths a tool call will write to,
// so the session layer can snapshot their before-state for safe rewind. It is a
// pure helper (no I/O beyond path resolution) and returns nil for read-only tools
// and for bash (whose affected paths are not knowable before execution).
//
// Deliberately WORKSPACE-ONLY, even though the tool itself may write outside the
// workspace under a boundary-lifting mode (bypass-all/read-write). The session
// checkpoint/rewind layer is workspace-confined: sessions.resolveWithinWorkspace
// does filepath.Join(root, rel), which maps an absolute path onto "<root>/<abs>"
// rather than rejecting it — so returning an outside path here would record a
// phantom in-workspace file and could make rewind touch the wrong path. Rewinding
// an outside write is therefore unsupported; outside writes are simply not
// snapshotted.
func MutationTargets(workspaceRoot string, name string, args map[string]any) []string {
	switch name {
	case "write_file", "edit_file":
		// Resolve the path via the SAME alias key list write_file/edit_file use,
		// so a checkpoint is captured even when the model writes via an alias key
		// (e.g. {"file": ...}); otherwise /thread revert could not undo the write.
		path, err := aliasedStringArg(args, []string{"path", "file", "file_path", "filename"}, "", true, false)
		if err != nil {
			return nil
		}
		_, relative, err := resolveWorkspaceTargetPath(workspaceRoot, path)
		if err != nil {
			return nil
		}
		return []string{relative}
	case "apply_patch":
		// Resolve the patch via the SAME alias key list apply_patch uses.
		patch, err := aliasedStringArg(args, []string{"patch", "diff"}, "", true, false)
		if err != nil {
			return nil
		}
		// Mirror apply_patch's cwd handling so the returned targets are
		// WORKSPACE-relative (cwd-prefixed) when cwd != ".". Without this, a
		// patch applied under a subdir would snapshot the wrong rewind path.
		cwd, err := stringArg(args, "cwd", ".", false)
		if err != nil {
			return nil
		}
		applyRoot, relativeRoot, err := resolveWorkspacePath(workspaceRoot, cwd)
		if err != nil {
			return nil
		}
		// Enforce the same workspace confinement apply_patch applies (against the
		// resolved apply dir), so a patch with a traversal path (../x) never yields
		// an out-of-workspace target.
		if err := validatePatchPaths(applyRoot, patch); err != nil {
			return nil
		}
		paths := changedFilesFromPatch(relativeRoot, patch)
		if len(paths) == 0 {
			return nil
		}
		return paths
	default:
		return nil
	}
}
