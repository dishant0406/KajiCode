package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dishant0406/KajiCode/internal/sandbox"
)

// defaultIgnoreDirs are directories skipped by default when listing. They are
// noise for navigation the same way opencode's assumed ignores hide vendored,
// generated, and tooling outputs. Callers may add to them via the `ignore`
// parameter.
var defaultIgnoreDirs = []string{
	".DS_Store", ".cache", ".git", ".hg", ".idea", ".kajicode", ".next",
	".svn", ".turbo", ".venv", ".vscode", ".worktrees", "__pycache__",
	"build", "coverage", "dist", "node_modules", "target", "temp", "tmp",
	"vendor",
}

// lsEntry is a single rendered tree entry: its display line plus sort keys.
type lsEntry struct {
	line   string
	dir    bool
	rel    string
	mtimeN int64
}

type lsTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
	specs         []*ArgSpec
}

func (lsTool) outputCategory(map[string]any) outputCategory { return outputCategorySearch }

func lsSpecs() []*ArgSpec {
	return []*ArgSpec{
		{Name: "path", Kind: ArgString, Description: "Directory to list. Defaults to workspace root.", Default: "."},
		{
			Name:        "depth",
			Kind:        ArgInt,
			Description: "Maximum recursion depth below the listed root (default 3).",
			Default:     3,
			Min:         intPtr(0),
			Max:         intPtr(10),
		},
		{Name: "include_dirs", Kind: ArgBool, Description: "Show directory lines even when depth is 0.", Default: true},
		{
			Name:        "ignore",
			Kind:        ArgStringSlice,
			Description: "Extra directory/file names or glob patterns to skip.",
			Default:     defaultIgnoreDirs,
			MinItems:    intPtr(1),
		},
		{
			Name:        "limit",
			Kind:        ArgInt,
			Description: "Maximum entries to render before truncating; 0 means unlimited.",
			Default:     100,
			Min:         intPtr(0),
			Max:         intPtr(5000),
		},
	}
}

func NewLsTool(workspaceRoot string) Tool { return NewScopedLsTool(workspaceRoot, nil) }

func NewScopedLsTool(workspaceRoot string, scope PathScope) Tool {
	return lsTool{
		baseTool: baseTool{
			name:         "ls",
			description:  "List a directory tree (directories first, indented) with default ignore globs, mirroring `tree`.",
			parameters:   SpecsToSchema(lsSpecs()),
			safety:       readOnlySafety("Lists directory entries without modifying files."),
			capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: true, ResourceKeys: directoryResourceKeys},
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
		specs:         lsSpecs(),
	}
}

func (t lsTool) Run(ctx context.Context, args map[string]any) Result {
	return t.run(ctx, args, readExcluder{})
}

func (t lsTool) RunWithOptions(ctx context.Context, args map[string]any, options RunOptions) Result {
	exclude := readExcluder{}
	if options.Sandbox != nil {
		exclude = sandboxReadExcluder(options.Sandbox)
	}
	return t.run(ctx, args, exclude)
}

func (t lsTool) RunWithSandbox(ctx context.Context, args map[string]any, engine *sandbox.Engine) Result {
	return t.run(ctx, args, sandboxReadExcluder(engine))
}

func (t lsTool) run(ctx context.Context, args map[string]any, exclude readExcluder) Result {
	parsed, err := ParseArgs(t.specs, args)
	if err != nil {
		return errorResult("Error: Invalid arguments for ls: " + err.Error())
	}
	requestedPath := parsed["path"].(string)
	if requestedPath == "" {
		requestedPath = "."
	}
	depth := parsed["depth"].(int)
	includeDirs := parsed["include_dirs"].(bool)
	ignore := parsed["ignore"].([]string)
	limit := parsed["limit"].(int)

	matchers := make([]*regexp.Regexp, 0, len(ignore))
	for _, pattern := range ignore {
		if strings.TrimSpace(pattern) == "" {
			continue
		}
		if matcher, cerr := compileIgnorePattern(pattern); cerr == nil {
			matchers = append(matchers, matcher)
		}
	}

	absolutePath, displayRoot, err := resolveScopedReadPath(t.workspaceRoot, t.scope, requestedPath)
	if err != nil {
		return errorResult("Error listing directory " + requestedPath + ": " + err.Error())
	}

	entries, err := walkLsTree(ctx, absolutePath, displayRoot, depth, includeDirs, matchers, exclude)
	if err != nil {
		if res, ok := searchCancelledResult("ls", err); ok {
			return res
		}
		return errorResult("Error listing directory " + displayRoot + ": " + err.Error())
	}
	if len(entries) == 0 {
		return okResult("Directory is empty: " + displayRoot)
	}

	total := len(entries)
	truncated := limit > 0 && total > limit
	shown := entries
	if truncated {
		shown = entries[:limit]
	}
	lines := make([]string, 0, len(shown))
	for _, entry := range shown {
		lines = append(lines, entry.line)
	}
	output := strings.Join(lines, "\n")
	if truncated {
		output += fmt.Sprintf("\n\n[truncated: showing %d of %d entries; use a narrower path/depth or higher limit]", limit, total)
	}
	meta := map[string]string{}
	if truncated {
		meta["truncated"] = "true"
		meta["truncation_reason"] = "limit"
		meta["total"] = fmt.Sprintf("%d", total)
	}
	return Result{Status: StatusOK, Output: output, Truncated: truncated, Meta: meta}
}

// walkLsTree walks the target root honoring depth, default ignores, caller
// ignores, and sandbox read exclusions. It renders directory lines (indented,
// slash-suffixed) and file lines, then sorts directories-first then by name.
func walkLsTree(ctx context.Context, root, displayRoot string, depth int, includeDirs bool, ignore []*regexp.Regexp, exclude readExcluder) ([]lsEntry, error) {
	var entries []lsEntry
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		normalized := filepath.ToSlash(rel)
		segments := strings.Split(normalized, "/")
		relDepth := len(segments) - 1

		if entry.IsDir() {
			base := entry.Name()
			if isDefaultIgnoreDir(base) || matchesIgnore(ignore, normalized) || exclude.dirExcluded(path) {
				return filepath.SkipDir
			}
			if includeDirs && depth >= 0 && relDepth <= depth {
				info, _ := entry.Info()
				mtime := int64(0)
				if info != nil {
					mtime = info.ModTime().UnixNano()
				}
				entries = append(entries, lsEntry{
					line:   fmt.Sprintf("%s%s/", indentForDepth(relDepth), displayName(displayRoot, normalized)),
					dir:    true,
					rel:    normalized,
					mtimeN: mtime,
				})
			}
			if relDepth >= depth {
				return filepath.SkipDir
			}
			return nil
		}

		if shouldSkipWorkspaceFile(normalized) || matchesIgnore(ignore, normalized) || exclude.fileExcluded(path) {
			return nil
		}
		if depth >= 0 && relDepth > depth {
			return nil
		}
		info, _ := entry.Info()
		mtime := int64(0)
		if info != nil {
			mtime = info.ModTime().UnixNano()
		}
		entries = append(entries, lsEntry{
			line:   fmt.Sprintf("%s%s", indentForDepth(relDepth), displayName(displayRoot, normalized)),
			dir:    false,
			rel:    normalized,
			mtimeN: mtime,
		})
		return nil
	})
	if err != nil {
		return entries, err
	}

	// Directories first, then name, matching tree(1) / opencode ordering.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].dir != entries[j].dir {
			return entries[i].dir
		}
		return entries[i].rel < entries[j].rel
	})
	return entries, nil
}

func indentForDepth(depth int) string {
	if depth <= 0 {
		return ""
	}
	return strings.Repeat("  ", depth)
}

func displayName(displayRoot, normalized string) string {
	if filepath.IsAbs(displayRoot) {
		return normalized
	}
	return normalized
}

func isDefaultIgnoreDir(name string) bool {
	for _, item := range defaultIgnoreDirs {
		if item == name {
			return true
		}
	}
	return false
}

func matchesIgnore(ignore []*regexp.Regexp, normalized string) bool {
	for _, matcher := range ignore {
		if matcher.MatchString(normalized) {
			return true
		}
	}
	return false
}

// compileIgnorePattern converts a glob-style ignore into a regexp. When the
// pattern contains a slash it is anchored to the full relative path; otherwise
// it matches any path segment.
func compileIgnorePattern(pattern string) (*regexp.Regexp, error) {
	pattern = filepath.ToSlash(pattern)
	pattern = strings.TrimSuffix(pattern, "/")
	if !strings.Contains(pattern, "/") {
		pattern = "(^|/)" + pattern + "(/|$)"
	} else {
		pattern = "^" + pattern + "(/|$)"
	}
	return regexp.Compile(pattern)
}
