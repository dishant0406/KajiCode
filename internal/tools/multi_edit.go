package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MultiEditInput is one atomic find/replace in a multi_edit call.
type MultiEditInput struct {
	OldString  string `json:"old_string,omitempty"`
	NewString  string `json:"new_string,omitempty"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type multiEditTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
}

func NewMultiEditTool(workspaceRoot string) Tool {
	return NewScopedMultiEditTool(workspaceRoot, nil)
}

func NewScopedMultiEditTool(workspaceRoot string, scope PathScope) Tool {
	return multiEditTool{
		baseTool: baseTool{
			name: "multi_edit",
			description: "Apply multiple find/replace edits to a single file atomically: " +
				"all edits are validated and applied to an in-memory copy first, and the file is " +
				"written only once if every edit succeeds. No partial writes.",
			parameters: SpecsToSchema([]*ArgSpec{
				{Name: "path", Kind: ArgString, Required: true, Aliases: []string{"file", "file_path", "filepath", "filename"}, Description: "Path of the file to edit."},
				{
					Name:        "edits",
					Kind:        ArgObjectSlice,
					Required:    true,
					MinItems:    intPtr(1),
					Description: "Ordered find/replace edits applied to the file in sequence.",
					Items: &ArgSpec{
						Kind: ArgObject,
						Properties: []*ArgSpec{
							{Name: "old_string", Kind: ArgString, Required: true, Aliases: []string{"old", "search", "find"}, Description: "Exact (or fuzzy-matching) string to replace. Must match byte-for-byte or be unique via fuzzy matching."},
							{Name: "new_string", Kind: ArgString, Default: "", Aliases: []string{"new", "replace", "replacement"}, Description: "Replacement string. May be empty."},
							{Name: "replace_all", Kind: ArgBool, Aliases: []string{"replaceAll"}, Default: false, Description: "Replace every occurrence instead of requiring uniqueness."},
						},
					},
				},
			}),
			safety:       promptSafety(SideEffectWrite, "Edits files in place."),
			capabilities: ToolCapabilities{Effect: EffectWorkspaceWrite, ThreadSafe: false, ResourceKeys: fileResourceKeys},
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
	}
}

func (tool multiEditTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.RunWithOptions(ctx, args, RunOptions{})
}

func (tool multiEditTool) RunWithOptions(ctx context.Context, args map[string]any, options RunOptions) Result {
	parsed, err := ParseArgs([]*ArgSpec{
		{Name: "path", Kind: ArgString, Required: true, Aliases: []string{"file", "file_path", "filepath", "filename"}},
		{
			Name:     "edits",
			Kind:     ArgObjectSlice,
			Required: true,
			MinItems: intPtr(1),
			Items: &ArgSpec{
				Kind: ArgObject,
				Properties: []*ArgSpec{
					{Name: "old_string", Kind: ArgString, Required: true, Aliases: []string{"old", "search", "find"}},
					{Name: "new_string", Kind: ArgString, Default: "", Aliases: []string{"new", "replace", "replacement"}},
					{Name: "replace_all", Kind: ArgBool, Aliases: []string{"replaceAll"}, Default: false},
				},
			},
		},
	}, args)
	if err != nil {
		return errorResult("Error: Invalid arguments for multi_edit: " + err.Error())
	}
	requestedPath := parsed["path"].(string)
	editObjects := parsed["edits"].([]map[string]any)

	edits := make([]MultiEditInput, 0, len(editObjects))
	for _, object := range editObjects {
		edits = append(edits, MultiEditInput{
			OldString:  object["old_string"].(string),
			NewString:  firstStringKey(object, "new_string"),
			ReplaceAll: object["replace_all"].(bool),
		})
	}

	absolutePath, relativePath, err := resolveScopedPath(tool.workspaceRoot, tool.scope, requestedPath)
	if err != nil {
		return errorResult("Error editing " + requestedPath + ": " + err.Error())
	}
	// Report the target directory so the agent can discover AGENTS.md rules in
	// the tree whose file it is editing.
	observeProjectGuideline(options, filepath.Dir(absolutePath))
	contentBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		return errorResult("Error: Could not find or open " + relativePath + ": " + err.Error())
	}
	if cerr := options.FileTracker.CheckConflict(absolutePath, contentBytes); cerr != nil {
		return errorResult(fileConflictMessage(relativePath))
	}
	content := string(contentBytes)
	before := content

	// Apply every edit to the in-memory copy. Any failure aborts without writing.
	for index := range edits {
		var matchErr error
		content, matchErr = applySingleEdit(content, edits[index])
		if matchErr != nil {
			return errorResult(fmt.Sprintf("Error editing %s (edit %d): %s", relativePath, index+1, matchErr.Error()))
		}
	}

	if content == before {
		return okResult(fmt.Sprintf("No changes to %s: all new_string values were identical to old_string.", relativePath))
	}

	if err := recheckScopedWriteTarget(tool.workspaceRoot, tool.scope, requestedPath); err != nil {
		return errorResult("Error writing " + relativePath + ": " + err.Error())
	}
	if err := os.WriteFile(absolutePath, []byte(content), 0o644); err != nil {
		return errorResult("Error writing " + relativePath + ": " + err.Error())
	}
	content = maybeFormatWrittenFile(ctx, absolutePath, content)

	newInfo, _ := os.Stat(absolutePath)
	options.FileTracker.Record(absolutePath, []byte(content), newInfo)

	summary := fmt.Sprintf("Successfully edited %s (%d edits applied).", relativePath, len(edits))
	result := okResult(summary)
	result.ChangedFiles = []string{relativePath}
	result.Display = Display{Summary: fmt.Sprintf("Edited %s (%d edits)", relativePath, len(edits)), Kind: "diff", Preview: boundedUnifiedDiff(relativePath, before, content)}
	return result
}

// applySingleEdit runs the exact-match → CRLF → fuzzy cascade for one edit and
// returns the updated content. It is shared by multi_edit so callers get the same
// tolerant matching behavior as edit_file.
func applySingleEdit(content string, edit MultiEditInput) (string, error) {
	oldString := edit.OldString
	newString := edit.NewString
	replaceAll := edit.ReplaceAll

	occurrences := strings.Count(content, oldString)

	// CRLF fallback (mirrors edit_file.run).
	if occurrences == 0 && strings.Contains(content, "\r\n") && !strings.Contains(oldString, "\r\n") {
		crlfOld := strings.ReplaceAll(oldString, "\n", "\r\n")
		if n := strings.Count(content, crlfOld); n > 0 {
			occurrences = n
			oldString = crlfOld
			if !strings.Contains(newString, "\r\n") {
				newString = strings.ReplaceAll(newString, "\n", "\r\n")
			}
		}
	}

	if occurrences == 0 {
		findOld, findNew := oldString, newString
		if strings.Contains(content, "\r\n") && !strings.Contains(findOld, "\r\n") {
			findOld = strings.ReplaceAll(findOld, "\n", "\r\n")
			findNew = strings.ReplaceAll(findNew, "\n", "\r\n")
		}
		search, ferr := fuzzyEditMatch(content, findOld, replaceAll)
		switch {
		case ferr == nil:
			oldString = search
			newString = adaptReplacementToSpan(search, findOld, findNew)
			occurrences = strings.Count(content, search)
		case errors.Is(ferr, errEditFuzzyAmbiguous):
			return "", fmt.Errorf("old_string matches multiple locations even after fuzzy matching. Provide more surrounding context to make the match unique, or pass replace_all: true.")
		case errors.Is(ferr, errEditFuzzyNotFound):
			// fall through to exact-match error below.
		default:
			return "", ferr
		}
	}

	if occurrences == 0 {
		return "", errors.New("could not find the exact string to replace; the old_string must match the file byte-for-byte")
	}
	if !replaceAll && occurrences > 1 {
		return "", fmt.Errorf("old_string matches %d locations; either make it more specific or pass replace_all: true", occurrences)
	}

	if replaceAll {
		return strings.ReplaceAll(content, oldString, newString), nil
	}
	return strings.Replace(content, oldString, newString, 1), nil
}
